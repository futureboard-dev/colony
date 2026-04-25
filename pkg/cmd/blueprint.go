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

	"github.com/jirateep/colony/pkg/llm"
	"github.com/jirateep/colony/pkg/module"
	"github.com/jirateep/colony/pkg/prompt"
	"github.com/spf13/cobra"
)

var blueprintCmd = &cobra.Command{
	Use:   "blueprint",
	Short: "Run an agent pipeline to implement a spec in an isolated worktree",
	Long: `Runs a strict pipeline: setup worktree → agent writes code → quality gates → commit.

Supports --resume (re-run gates on existing worktree) and --continue (continue
interrupted codegen). Use --headless to run in the background.`,
	RunE: runBlueprint,
}

var (
	bpSpec     string
	bpLang     string
	bpModel    string
	bpResume   string
	bpContinue string
	bpBase     string
	bpHeadless bool
	bpLogFile  string // internal: set by headless re-exec
)

func init() {
	blueprintCmd.Flags().StringVar(&bpSpec, "spec", "", "spec markdown file")
	blueprintCmd.Flags().StringVar(&bpLang, "lang", "", "language: typescript, python, go")
	blueprintCmd.Flags().StringVar(&bpModel, "model", "", "override model from config")
	blueprintCmd.Flags().StringVar(&bpResume, "resume", "", "worktree path: re-run gates only")
	blueprintCmd.Flags().StringVar(&bpContinue, "continue", "", "worktree path: continue codegen then gates")
	blueprintCmd.Flags().StringVar(&bpBase, "base", "", "base branch (must not be main/master)")
	blueprintCmd.Flags().BoolVar(&bpHeadless, "headless", false, "run in background, tail log for output")
	blueprintCmd.Flags().StringVar(&bpLogFile, "_log", "", "")
	blueprintCmd.Flags().MarkHidden("_log") //nolint:errcheck
}

func runBlueprint(cmd *cobra.Command, args []string) error {
	// ── Validate args ──────────────────────────────────────────────────────────
	if bpResume != "" || bpContinue != "" {
		if bpLang == "" {
			return fmt.Errorf("--lang required with --resume / --continue")
		}
	} else {
		if bpSpec == "" || bpLang == "" {
			return fmt.Errorf("--spec and --lang required\n\nExample:\n  colony blueprint --spec SPEC.md --lang typescript")
		}
		if _, err := os.Stat(bpSpec); err != nil {
			return fmt.Errorf("spec file not found: %s", bpSpec)
		}
	}
	if bpBase == "main" || bpBase == "master" {
		return fmt.Errorf("--base cannot be 'main' or 'master' — agents must target feature branches")
	}

	// ── Load config ────────────────────────────────────────────────────────────
	cfg, root, err := loadConfig()
	if err != nil {
		return err
	}
	llmCfg := cfg.Role("engineer")
	if bpModel != "" {
		llmCfg.Model = bpModel
	}
	ex := llm.New(llmCfg)

	// ── Prepare log file ───────────────────────────────────────────────────────
	ts := time.Now().Format("20060102-150405")
	if err := module.EnsureLogDir(root); err != nil {
		return err
	}
	logPath := bpLogFile
	if logPath == "" {
		prefix := "blueprint"
		if bpResume != "" {
			prefix = "blueprint-resume"
		} else if bpContinue != "" {
			prefix = "blueprint-continue"
		}
		logPath = filepath.Join(module.LogDir(root), fmt.Sprintf("%s-%s.log", prefix, ts))
	}

	// ── Headless mode: re-exec without --headless, pipe output to log ──────────
	if bpHeadless {
		return bpRunHeadless(logPath)
	}

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer logFile.Close()
	out := io.MultiWriter(os.Stdout, logFile)

	langs, err := module.CommandsFor(bpLang)
	if err != nil {
		return err
	}

	ctx := cmd.Context()

	// ── RESUME MODE ────────────────────────────────────────────────────────────
	if bpResume != "" {
		branch, _ := gitBranch(bpResume)
		bpBanner(out, "🔄 BLUEPRINT RESUME", map[string]string{
			"Worktree": bpResume, "Branch": branch, "Language": bpLang,
		})
		if err := runGates(ctx, bpResume, langs, ex, out); err != nil {
			return bpBlocked(bpResume, logPath, err, out)
		}
		return bpCommit(bpResume, branch, "fix: resume after manual review — gates passed", out)
	}

	// ── CONTINUE MODE ──────────────────────────────────────────────────────────
	if bpContinue != "" {
		branch, _ := gitBranch(bpContinue)
		bpBanner(out, "🔄 BLUEPRINT CONTINUE", map[string]string{
			"Worktree": bpContinue, "Branch": branch, "Language": bpLang,
		})
		fmt.Fprintf(out, "\n▶ STEP 1/3  Agent: continue writing code\n")
		contPrompt, err := prompt.BuildContinue(bpLang)
		if err != nil {
			return err
		}
		if err := ex.RunAgent(ctx, bpContinue, contPrompt, out); err != nil {
			return fmt.Errorf("continue agent failed: %w", err)
		}
		fmt.Fprintf(out, "✓ Agent finished continuing code\n")
		if err := runGates(ctx, bpContinue, langs, ex, out); err != nil {
			return bpBlocked(bpContinue, logPath, err, out)
		}
		return bpCommit(bpContinue, branch, "feat: continue after interruption — gates passed", out)
	}

	// ── NORMAL MODE ────────────────────────────────────────────────────────────
	if err := ex.Preflight(); err != nil {
		return err
	}

	projectName := module.ProjectName(root)
	baseBranch := module.DefaultBranch()
	if bpBase != "" {
		baseBranch = bpBase
	}

	specData, err := os.ReadFile(bpSpec)
	if err != nil {
		return err
	}
	taskDesc := module.ExtractTaskDesc(string(specData), bpSpec)
	branch := module.NewBranch(taskDesc)

	bpBanner(out, "🤖 BLUEPRINT PIPELINE STARTING", map[string]string{
		"Project": projectName, "Language": bpLang,
		"Model":  fmt.Sprintf("%s (%s)", llmCfg.Model, llmCfg.Provider),
		"Branch": branch, "Spec": bpSpec, "Log": logPath,
	})

	// Step 1: Setup worktree
	fmt.Fprintf(out, "▶ STEP 1/4  Setup isolated worktree\n")
	worktreePath, err := module.SetupWorktree(root, projectName, branch, baseBranch)
	if err != nil {
		return err
	}
	if err := module.CopyFile(bpSpec, filepath.Join(worktreePath, "SPEC.md")); err != nil {
		return err
	}
	if bpLang == "typescript" || bpLang == "ts" {
		fmt.Fprintf(out, "   Installing dependencies...\n")
		module.RunShell("pnpm install --frozen-lockfile", worktreePath, out) //nolint:errcheck
	}
	fmt.Fprintf(out, "✓ Worktree ready: %s\n", worktreePath)

	// Step 2: Agent writes code
	fmt.Fprintf(out, "\n▶ STEP 2/4  Agent: write code\n")
	writePrompt, err := prompt.Build(bpLang)
	if err != nil {
		return err
	}
	if err := ex.RunAgent(ctx, worktreePath, writePrompt, out); err != nil {
		return fmt.Errorf("agent failed: %w", err)
	}
	fmt.Fprintf(out, "✓ Agent finished writing code\n")

	// Steps 3–4: Quality gates
	if err := runGates(ctx, worktreePath, langs, ex, out); err != nil {
		return bpBlocked(worktreePath, logPath, err, out)
	}

	commitMsg := fmt.Sprintf("feat: %s\n\nBlueprint: %s\nLanguage: %s\nGates: format, typecheck, tests\nLog: %s",
		taskDesc, branch, bpLang, logPath)
	if err := bpCommit(worktreePath, branch, commitMsg, out); err != nil {
		return err
	}

	remoteURL := gitRemoteURL(worktreePath)
	fmt.Fprintf(out, "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(out, "✅ BLUEPRINT COMPLETE\n\nBranch:   %s\nWorktree: %s\n\n", branch, worktreePath)
	fmt.Fprintf(out, "Next:\n  Review:  git diff %s..%s\n", baseBranch, branch)
	fmt.Fprintf(out, "  Push:    cd %s && git push -u origin %s\n", worktreePath, branch)
	if remoteURL != "" {
		fmt.Fprintf(out, "  PR:      %s/compare/%s...%s?expand=1\n", remoteURL, baseBranch, branch)
	}
	fmt.Fprintf(out, "  Cleanup: colony task done %s\n", branch)
	fmt.Fprintf(out, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	return nil
}

// runGates runs format → typecheck (with fix) → tests (with fix).
// Exported so swarm.go can reuse it.
func runGates(ctx context.Context, worktreePath string, langs module.LangCommands, ex *llm.Executor, out io.Writer) error {
	const maxAttempts = 2
	fmt.Fprintf(out, "\n▶ GATES  format → typecheck → tests\n")
	module.RunFormat(langs.Format, worktreePath, out)
	if err := gateWithFix(ctx, "Type check", langs.TypeCheck, worktreePath, maxAttempts, ex, out); err != nil {
		return err
	}
	return gateWithFix(ctx, "Tests", langs.Test, worktreePath, maxAttempts, ex, out)
}

func gateWithFix(ctx context.Context, name, gateCmd, workdir string, maxAttempts int, ex *llm.Executor, out io.Writer) error {
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		fmt.Fprintf(out, "   %s (attempt %d/%d): %s\n", name, attempt, maxAttempts, gateCmd)
		errOut, err := module.RunGateCapture(gateCmd, workdir)
		if err == nil {
			fmt.Fprintf(out, "✓ %s passed\n", name)
			return nil
		}
		fmt.Fprintf(out, "✗ %s failed\n%s\n", name, errOut)
		if attempt < maxAttempts {
			fixP, ferr := prompt.Fix(name, errOut)
			if ferr != nil {
				return ferr
			}
			fmt.Fprintf(out, "   Asking agent to fix %s errors...\n", name)
			if rerr := ex.RunAgent(ctx, workdir, fixP, out); rerr != nil {
				fmt.Fprintf(out, "⚠ fix agent error: %v\n", rerr)
			}
		}
	}
	return fmt.Errorf("%s failed after %d attempts — human review required\n  Resume: colony blueprint --resume %s --lang <lang>",
		name, maxAttempts, workdir)
}

func bpCommit(worktreePath, branch, msg string, out io.Writer) error {
	fmt.Fprintf(out, "\n▶ COMMIT\n")
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
	os.Remove(filepath.Join(worktreePath, "SPEC.md")) //nolint:errcheck
	fmt.Fprintf(out, "✓ Committed on branch: %s\n", branch)
	return nil
}

func bpBlocked(worktreePath, logPath string, err error, out io.Writer) error {
	fmt.Fprintf(out, "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(out, "🚫 BLUEPRINT BLOCKED\n%v\nWorktree: %s\nLog:      %s\n", err, worktreePath, logPath)
	fmt.Fprintf(out, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	return err
}

func bpBanner(out io.Writer, title string, fields map[string]string) {
	fmt.Fprintf(out, "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n%s\n", title)
	for k, v := range fields {
		fmt.Fprintf(out, "%-10s %s\n", k+":", v)
	}
	fmt.Fprintf(out, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")
}

func bpRunHeadless(logPath string) error {
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
	fmt.Printf("🚀 BLUEPRINT RUNNING HEADLESS\n")
	fmt.Printf("   PID:  %d\n   Log:  tail -f %s\n   Stop: kill %d\n", cmd.Process.Pid, logPath, cmd.Process.Pid)
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")
	return nil
}

func gitBranch(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "unknown", err
	}
	return strings.TrimSpace(string(out)), nil
}

func gitRemoteURL(dir string) string {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	url := strings.TrimSpace(string(out))
	if strings.HasPrefix(url, "git@") {
		url = strings.Replace(url, ":", "/", 1)
		url = strings.Replace(url, "git@", "https://", 1)
	}
	return strings.TrimSuffix(url, ".git")
}
