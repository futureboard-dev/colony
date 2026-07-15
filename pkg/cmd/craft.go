package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/futureboard-dev/colony/pkg/llm"
	"github.com/futureboard-dev/colony/pkg/module"
	"github.com/futureboard-dev/colony/pkg/output"
	"github.com/futureboard-dev/colony/pkg/prompt"
	"github.com/futureboard-dev/colony/pkg/storage"
	"github.com/spf13/cobra"
)

const (
	ansiReset  = "\033[0m"
	ansiGreen  = "\033[32m"
	ansiRed    = "\033[31m"
	ansiCyan   = "\033[36m"
	ansiYellow = "\033[33m"
	ansiBlue   = "\033[34m"
)

var (
	statusLine *output.StatusLine

	craftCmd = &cobra.Command{
		Use:   "craft",
		Short: "Run an agent pipeline to implement a spec in an isolated worktree",
		Long: `Runs a strict pipeline: setup worktree → agent writes code → quality gates → commit → PR.

Supports --resume (re-run gates on existing worktree) and --continue (continue
interrupted codegen). Use --headless to run in the background.`,
		RunE: runCraft,
	}

	craftSpec        string
	craftLang        string
	craftModel       string
	craftProvider    string
	craftResume      string
	craftContinue    string
	craftBase        string
	craftHeadless    bool
	craftNoFormat    bool
	craftInteractive bool
	craftNoPR        bool
	craftLogFile     string // internal: set by headless re-exec
)

func init() {
	craftCmd.Flags().StringVar(&craftSpec, "spec", "", "spec markdown file")
	craftCmd.Flags().StringVar(&craftLang, "lang", "", "language: typescript, python, go")
	craftCmd.Flags().StringVar(&craftModel, "model", "", "override model from config")
	craftCmd.Flags().StringVar(&craftProvider, "provider", "deepseek", "model provider: deepseek (default, deepseek-reasoner) or anthropic (engineer role, claude-sonnet-4-6)")
	craftCmd.Flags().StringVar(&craftResume, "resume", "", "worktree path: re-run gates only")
	craftCmd.Flags().StringVar(&craftContinue, "continue", "", "worktree path: continue codegen then gates")
	craftCmd.Flags().StringVar(&craftBase, "base", "", "base branch (must not be main/master)")
	craftCmd.Flags().BoolVar(&craftHeadless, "headless", false, "run in background, tail log for output")
	craftCmd.Flags().BoolVar(&craftNoFormat, "no-format", false, "skip the format gate")
	craftCmd.Flags().BoolVar(&craftInteractive, "interactive", false, "run the codegen step as a live agent session you can watch and steer")
	craftCmd.Flags().BoolVar(&craftNoPR, "no-pr", false, "skip push and PR creation after successful completion")
	craftCmd.Flags().StringVar(&craftLogFile, "_log", "", "")
	craftCmd.Flags().MarkHidden("_log") //nolint:errcheck
}

func runCraft(cmd *cobra.Command, args []string) error {
	// ── Validate args ──────────────────────────────────────────────────────────
	if err := validateProvider(craftProvider); err != nil {
		return err
	}
	if craftResume != "" || craftContinue != "" {
		if craftLang == "" {
			return fmt.Errorf("--lang required with --resume / --continue")
		}
	} else {
		if craftSpec == "" || craftLang == "" {
			return fmt.Errorf("--spec and --lang required\n\nExample:\n  colony craft --spec SPEC.md --lang typescript")
		}
		if _, err := os.Stat(craftSpec); err != nil {
			return fmt.Errorf("spec file not found: %s", craftSpec)
		}
	}
	if craftBase == "main" || craftBase == "master" {
		return fmt.Errorf("--base cannot be 'main' or 'master' — agents must target feature branches")
	}
	if craftInteractive {
		if craftHeadless {
			return fmt.Errorf("--interactive cannot be combined with --headless (interactive needs a terminal)")
		}
		if craftResume != "" {
			return fmt.Errorf("--interactive has no effect with --resume (resume only re-runs gates)")
		}
	}

	// ── Load config ────────────────────────────────────────────────────────────
	cfg, root, err := loadConfig()
	if err != nil {
		return err
	}
	llmCfg := providerLLM(cfg, craftProvider, "engineer")
	if craftModel != "" {
		llmCfg.Model = craftModel
	}
	ex := llm.New(llmCfg)

	// ── Prepare log file ───────────────────────────────────────────────────────
	ts := time.Now().Format("20060102-150405")
	if err := module.EnsureLogDir(root); err != nil {
		return err
	}
	logPath := craftLogFile
	if logPath == "" {
		prefix := "craft"
		if craftResume != "" {
			prefix = "craft-resume"
		} else if craftContinue != "" {
			prefix = "craft-continue"
		}
		logPath = filepath.Join(module.LogDir(root), fmt.Sprintf("%s-%s.log", prefix, ts))
	}

	// ── Headless mode: re-exec without --headless, pipe output to log ──────────
	if craftHeadless {
		return craftRunHeadless(logPath)
	}

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer logFile.Close()
	heatmap := output.NewHeatmapWriter(os.Stdout)
	statusLine = output.NewStatusLine(os.Stdout, heatmap)
	defer statusLine.Close()
	out := io.MultiWriter(statusLine, logFile)

	langs, err := module.CommandsFor(craftLang)
	if err != nil {
		return err
	}

	// ── Record run facts (status/branch in SQLite; raw output stays in logPath) ──
	runID := strings.TrimSuffix(filepath.Base(logPath), ".log")
	store := openRunStore(root, out)
	if store != nil {
		defer func() { _ = store.Close() }()
		_ = store.InsertRun(storage.Run{
			ID: runID, Kind: "craft", Project: module.ProjectName(root),
			Language: craftLang, Model: llmCfg.Model, Status: "running",
			LogPath: logPath, StartedAt: time.Now(),
		})
	}

	ctx := cmd.Context()

	baseBranch := module.DefaultBranch()
	if craftBase != "" {
		baseBranch = craftBase
	}

	// ── RESUME MODE ────────────────────────────────────────────────────────────
	if craftResume != "" {
		statusLine.SetState(output.StateWorking)
		statusLine.SetMessage("resuming gates")
		branch, _ := module.CurrentBranch(craftResume)
		craftBanner(out, "🔄 CRAFT RESUME", map[string]string{
			"Worktree": craftResume, "Branch": branch, "Language": craftLang,
		})
		if err := runGates(ctx, craftResume, craftBase, craftLang, langs, ex, out, craftNoFormat); err != nil {
			return craftBlocked(craftResume, logPath, err, out, store, runID)
		}
		fmt.Fprintf(out, "%s✓ Resumed and passed gates on branch: %s%s\n", ansiGreen, branch, ansiReset)
		if err := craftCommit(craftResume, branch, "fix: resume after manual review — gates passed", out); err != nil {
			return err
		}
		if _, err := pushAndCreatePR(craftResume, branch, baseBranch, craftNoPR, out); err != nil {
			fmt.Fprintf(out, "%s⚠ push/PR skipped: %v%s\n", ansiYellow, err, ansiReset)
		}
		finishRun(store, runID, "complete", branch)
		return nil
	}

	// ── CONTINUE MODE ──────────────────────────────────────────────────────────
	if craftContinue != "" {
		statusLine.SetState(output.StateWorking)
		statusLine.SetMessage("continuing codegen")
		branch, _ := module.CurrentBranch(craftContinue)
		craftBanner(out, "🔄 CRAFT CONTINUE", map[string]string{
			"Worktree": craftContinue, "Branch": branch, "Language": craftLang,
		})
		fmt.Fprintf(out, "\n%s▶ STEP 1/3  Agent: continue writing code%s\n", ansiCyan, ansiReset)
		contPrompt, err := prompt.BuildContinue(craftLang)
		if err != nil {
			return err
		}
		if err := runCodegen(ctx, ex, craftContinue, contPrompt, out); err != nil {
			return fmt.Errorf("continue agent failed: %w", err)
		}
		fmt.Fprintf(out, "%s✓ Agent finished continuing code%s\n", ansiGreen, ansiReset)
		if err := runGates(ctx, craftContinue, craftBase, craftLang, langs, ex, out, craftNoFormat); err != nil {
			return craftBlocked(craftContinue, logPath, err, out, store, runID)
		}
		if err := craftCommit(craftContinue, branch, "feat: continue after interruption — gates passed", out); err != nil {
			return err
		}
		if _, err := pushAndCreatePR(craftContinue, branch, baseBranch, craftNoPR, out); err != nil {
			fmt.Fprintf(out, "%s⚠ push/PR skipped: %v%s\n", ansiYellow, err, ansiReset)
		}
		finishRun(store, runID, "complete", branch)
		return nil
	}

	statusLine.SetState(output.StateWorking)
	statusLine.SetMessage("starting pipeline")

	// ── NORMAL MODE ────────────────────────────────────────────────────────────
	if err := ex.Preflight(); err != nil {
		return err
	}

	projectName := module.ProjectName(root)

	specData, err := os.ReadFile(craftSpec)
	if err != nil {
		return err
	}
	taskDesc := module.ExtractTaskDesc(string(specData), craftSpec)
	branch := module.NewBranch(taskDesc)

	craftBanner(out, "🤖 CRAFT PIPELINE STARTING", map[string]string{
		"Project": projectName, "Language": craftLang,
		"Model":  fmt.Sprintf("%s (%s)", llmCfg.Model, llmCfg.Provider),
		"Branch": branch, "Spec": craftSpec, "Log": logPath,
	})

	// Step 1: Setup worktree
	statusLine.SetState(output.StateWorking)
	statusLine.SetMessage("setting up worktree")
	fmt.Fprintf(out, "%s▶ STEP 1/4  Setup isolated worktree%s\n", ansiCyan, ansiReset)
	worktreePath, err := module.SetupWorktree(root, projectName, branch, baseBranch)
	if err != nil {
		return err
	}
	if err := module.CopyFile(craftSpec, filepath.Join(worktreePath, "SPEC.md")); err != nil {
		return err
	}
	module.InstallDeps(craftLang, worktreePath, out)
	fmt.Fprintf(out, "%s✓ Worktree ready: %s%s\n", ansiGreen, worktreePath, ansiReset)

	// Step 2: Agent writes code
	statusLine.SetState(output.StateThinking)
	statusLine.SetMessage("agent writing code")
	fmt.Fprintf(out, "\n%s▶ STEP 2/4  Agent: write code%s\n", ansiCyan, ansiReset)
	writePrompt, err := prompt.Build(craftLang)
	if err != nil {
		return err
	}
	if err := runCodegen(ctx, ex, worktreePath, writePrompt, out); err != nil {
		return fmt.Errorf("agent failed: %w", err)
	}
	statusLine.SetState(output.StateWorking)
	statusLine.SetMessage("running quality gates")
	fmt.Fprintf(out, "%s✓ Agent finished writing code%s\n", ansiGreen, ansiReset)

	// Steps 3–4: Quality gates
	if err := runGates(ctx, worktreePath, baseBranch, craftLang, langs, ex, out, craftNoFormat); err != nil {
		return craftBlocked(worktreePath, logPath, err, out, store, runID)
	}

	commitMsg := fmt.Sprintf("feat: %s\n\nCraft: %s\nLanguage: %s\nGates: format, typecheck, tests\nLog: %s",
		taskDesc, branch, craftLang, logPath)
	statusLine.SetState(output.StateWorking)
	statusLine.SetMessage("committing")
	if err := craftCommit(worktreePath, branch, commitMsg, out); err != nil {
		return err
	}

	prURL, prErr := pushAndCreatePR(worktreePath, branch, baseBranch, craftNoPR, out)
	finishRun(store, runID, "complete", branch)

	statusLine.SetState(output.StateIdle)
	statusLine.SetMessage("")
	fmt.Fprintf(out, "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(out, "%s✅ CRAFT COMPLETE\n\nBranch:   %s\nWorktree: %s\n%s", ansiGreen, branch, worktreePath, ansiReset)
	if prErr != nil {
		fmt.Fprintf(out, "%s⚠ push/PR failed: %v%s\n", ansiYellow, prErr, ansiReset)
		fmt.Fprintf(out, "  Push:    cd %s && git push -u origin %s\n", worktreePath, branch)
	} else if prURL != "" {
		fmt.Fprintf(out, "  PR:      %s\n", prURL)
	}
	fmt.Fprintf(out, "  Cleanup: colony task done %s\n", branch)
	fmt.Fprintf(out, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	return nil
}

// runCodegen runs the code-writing agent step. With --interactive it launches a
// live agent session (claude or crush) the user can watch and steer; that output
// goes to the terminal, not the log file. Otherwise it runs headless via RunAgent.
func runCodegen(ctx context.Context, ex *llm.Executor, workdir, agentPrompt string, out io.Writer) error {
	statusLine.SetState(output.StateThinking)
	statusLine.SetMessage("agent working")
	if craftInteractive {
		fmt.Fprintf(out, "%s   --interactive: launching live agent session (terminal only, not captured to log)%s\n", ansiYellow, ansiReset)
		return ex.RunInteractive(workdir, agentPrompt)
	}
	return ex.RunAgent(ctx, workdir, agentPrompt, out)
}

// runGates runs format → lint (with fix) → typecheck (with fix) → tests (with fix).
// Lint is skipped when its linter isn't installed, mirroring the CLI hook.
// Exported so swarm.go can reuse it.
//
// base is the branch this worktree was cut from. The format and lint commands are
// scoped to the files that differ from origin/base so a whole-repo lint (pnpm
// eslint .) can't fail the gate on pre-existing violations the task never touched.
// base may be empty (e.g. --resume without --base): module.ChangedFiles then falls
// back to the worktree's own origin/HEAD. When the scoped set is empty (nothing
// lintable changed vs base), format/lint are skipped entirely — running them
// unscoped would re-introduce the whole-repo problem. vet/typecheck/test stay
// whole-repo — they can't be scoped to a subset.
func runGates(ctx context.Context, worktreePath, base, lang string, langs module.LangCommands, ex *llm.Executor, out io.Writer, skipFormat bool) error {
	statusLine.SetState(output.StateWorking)
	statusLine.SetMessage("running gates")
	const maxAttempts = 2
	fmt.Fprintf(out, "\n%s▶ GATES  format → lint → typecheck → tests%s\n", ansiCyan, ansiReset)

	// Scope format/lint to the task's own changed files. ChangedFiles resolves
	// base against the worktree (falling back to its origin/HEAD when base is empty
	// or unknown), so this works even for --resume without --base. AutoFix runs the
	// deterministic fixers first so mechanical issues never reach the fix agent.
	scope := module.ChangedFiles(worktreePath, base)
	if len(scope) > 0 {
		module.AutoFix(lang, worktreePath, scope, out) // no-op for non-TS langs
	} else {
		fmt.Fprintf(out, "%s⚠ format/lint skipped — no lintable files changed vs base%s\n", ansiYellow, ansiReset)
		skipFormat = true
	}
	formatCmd := module.ScopeCommand(langs.Format, scope)
	lintCmd := module.ScopeCommand(langs.Lint, scope)

	if !skipFormat {
		module.RunFormat(formatCmd, worktreePath, out)
	}
	if len(scope) > 0 && lintCmd != "" {
		if module.CommandAvailable(lintCmd) {
			if err := gateWithFix(ctx, "Lint", lintCmd, worktreePath, maxAttempts, ex, out); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(out, "%s⚠ Lint skipped — %q not installed%s\n", ansiYellow, strings.Fields(lintCmd)[0], ansiReset)
		}
	}
	if err := gateWithFix(ctx, "Type check", langs.TypeCheck, worktreePath, maxAttempts, ex, out); err != nil {
		return err
	}
	return gateWithFix(ctx, "Tests", langs.Test, worktreePath, maxAttempts, ex, out)
}

func gateWithFix(ctx context.Context, name, gateCmd, workdir string, maxAttempts int, ex *llm.Executor, out io.Writer) error {
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			statusLine.SetMessage("self-correcting " + name)
		}
		fmt.Fprintf(out, "   %s (attempt %d/%d): %s\n", name, attempt, maxAttempts, gateCmd)
		errOut, err := module.RunGateCapture(gateCmd, workdir)
		if err == nil {
			fmt.Fprintf(out, "%s✓ %s passed%s\n", ansiGreen, name, ansiReset)
			return nil
		}
		fmt.Fprintf(out, "%s✗ %s failed%s\n", ansiRed, name, ansiReset)
		fmt.Fprintf(out, "%s\n", errOut)
		if attempt < maxAttempts {
			fixP, ferr := prompt.Fix(name, errOut)
			if ferr != nil {
				return ferr
			}
			statusLine.SetState(output.StateThinking)
			statusLine.SetMessage("fixing " + name + " errors")
			fmt.Fprintf(out, "   Asking agent to fix %s errors...\n", name)
			if rerr := ex.RunAgent(ctx, workdir, fixP, out); rerr != nil {
				fmt.Fprintf(out, "%s⚠ fix agent error: %v%s\n", ansiYellow, rerr, ansiReset)
			}
			statusLine.SetState(output.StateWorking)
		}
	}
	return fmt.Errorf("%s failed after %d attempts — human review required\n  Resume: colony craft --resume %s --lang <lang>",
		name, maxAttempts, workdir)
}

func craftCommit(worktreePath, branch, msg string, out io.Writer) error {
	statusLine.SetState(output.StateWorking)
	statusLine.SetMessage("committing")
	fmt.Fprintf(out, "\n▶ COMMIT\n")
	os.Remove(filepath.Join(worktreePath, "SPEC.md")) //nolint:errcheck
	add := exec.Command("git", "add", "-A")
	add.Dir = worktreePath
	if err := add.Run(); err != nil {
		return fmt.Errorf("git add: %w", err)
	}
	commit := exec.Command("git", "commit", "-m", msg)
	commit.Dir = worktreePath
	commit.Stdout = out
	commit.Stderr = out
	if err := commit.Run(); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	fmt.Fprintf(out, "✓ Committed on branch: %s\n", branch)
	return nil
}

// pushAndCreatePR pushes branch to origin and opens a PR against baseBranch.
// Returns the PR URL on success. Skips both operations when noPR is true.
func pushAndCreatePR(worktreePath, branch, baseBranch string, noPR bool, out io.Writer) (string, error) {
	if noPR {
		return "", nil
	}
	statusLine.SetState(output.StateWorking)
	statusLine.SetMessage("pushing branch")
	fmt.Fprintf(out, "\n▶ PUSH\n")
	push := exec.Command("git", "push", "-u", "origin", branch)
	push.Dir = worktreePath
	push.Stdout = out
	push.Stderr = out
	if err := push.Run(); err != nil {
		return "", fmt.Errorf("git push: %w", err)
	}
	fmt.Fprintf(out, "✓ Pushed branch: %s\n", branch)

	statusLine.SetMessage("creating PR")
	fmt.Fprintf(out, "\n▶ PR\n")

	// Extract OWNER/REPO from the remote URL so gh works even when the remote
	// uses a custom SSH host alias (e.g. git@github.com-fraia:ORG/REPO.git).
	repo := remoteRepo(worktreePath)
	args := []string{
		"pr", "create",
		"--base", baseBranch,
		"--head", branch,
		"--title", branch,
		"--body", fmt.Sprintf("Automated by colony craft.\n\nBranch: `%s`\nBase: `%s`", branch, baseBranch),
	}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	gh := exec.Command("gh", args...)
	gh.Dir = worktreePath
	var prURL strings.Builder
	var ghStderr strings.Builder
	gh.Stdout = io.MultiWriter(out, &prURL)
	gh.Stderr = io.MultiWriter(out, &ghStderr)
	if err := gh.Run(); err != nil {
		fmt.Fprintf(out, "%s✗ PR creation failed: %v%s\n", ansiRed, err, ansiReset)
		if msg := strings.TrimSpace(ghStderr.String()); msg != "" {
			fmt.Fprintf(out, "  gh said: %s\n", msg)
		}
		if repo != "" {
			fmt.Fprintf(out, "  Open manually: https://github.com/%s/compare/%s...%s?expand=1\n", repo, baseBranch, branch)
		}
		return "", fmt.Errorf("gh pr create: %w", err)
	}
	url := strings.TrimSpace(prURL.String())
	fmt.Fprintf(out, "✓ PR created: %s\n", url)
	return url, nil
}

// remoteRepo returns "OWNER/REPO" extracted from the git origin remote URL.
// Handles both SSH (git@<host>:OWNER/REPO.git) and HTTPS URLs.
// Returns "" if the remote can't be read or parsed.
func remoteRepo(dir string) string {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	raw := strings.TrimSpace(string(out))
	// SSH: git@github.com-alias:OWNER/REPO.git or git@github.com:OWNER/REPO.git
	if strings.HasPrefix(raw, "git@") {
		idx := strings.Index(raw, ":")
		if idx < 0 {
			return ""
		}
		return strings.TrimSuffix(raw[idx+1:], ".git")
	}
	// HTTPS: https://github.com/OWNER/REPO.git
	// Strip scheme and host, keep /OWNER/REPO
	idx := strings.Index(raw, "//")
	if idx >= 0 {
		raw = raw[idx+2:]
	}
	slash := strings.Index(raw, "/")
	if slash < 0 {
		return ""
	}
	return strings.TrimSuffix(raw[slash+1:], ".git")
}

func craftBlocked(worktreePath, logPath string, err error, out io.Writer, store *storage.SQLiteStore, runID string) error {
	statusLine.SetState(output.StateIdle)
	statusLine.SetMessage("")
	finishRun(store, runID, "blocked", "")
	fmt.Fprintf(out, "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(out, "%s🚫 CRAFT BLOCKED\n%v\nWorktree: %s\nLog:      %s%s\n", ansiRed, err, worktreePath, logPath, ansiReset)
	fmt.Fprintf(out, "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	return err
}

// openRunStore opens the project's missions.db for recording run facts.
// Recording is best-effort: on failure it warns and returns nil so the
// pipeline still runs.
func openRunStore(root string, out io.Writer) *storage.SQLiteStore {
	store, err := storage.Open(filepath.Join(root, ".colony", "missions.db"))
	if err != nil {
		fmt.Fprintf(out, "%s⚠ run not recorded: %v%s\n", ansiYellow, err, ansiReset)
		return nil
	}
	return store
}

// finishRun marks a run's terminal status and branch. Safe with a nil store.
func finishRun(store *storage.SQLiteStore, runID, status, branch string) {
	if store == nil {
		return
	}
	now := time.Now()
	_ = store.UpdateRun(storage.Run{ID: runID, Status: status, Branch: branch, FinishedAt: &now})
}

func craftBanner(out io.Writer, title string, fields map[string]string) {
	statusLine.SetMessage(title)
	fmt.Fprintf(out, "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n%s\n", title)
	for k, v := range fields {
		fmt.Fprintf(out, "%-10s %s\n", k+":", v)
	}
	fmt.Fprintf(out, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")
}

func craftRunHeadless(logPath string) error {
	var newArgs []string
	for _, arg := range os.Args[1:] {
		if arg != "--headless" && arg != "-headless" {
			newArgs = append(newArgs, arg)
		}
	}
	newArgs = append(newArgs, "--_log", logPath)
	logFile, err := os.Create(logPath)
	if err != nil {
		return err
	}
	cmd := exec.Command(os.Args[0], newArgs...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return err
	}
	fmt.Printf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("%s🚀 CRAFT RUNNING HEADLESS%s\n", ansiCyan, ansiReset)
	fmt.Printf("   PID:  %d\n   Log:  tail -f %s\n   Stop: kill %d\n", cmd.Process.Pid, logPath, cmd.Process.Pid)
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")
	return nil
}
