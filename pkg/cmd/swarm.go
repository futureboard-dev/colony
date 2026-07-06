package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/futureboard-dev/colony/pkg/config"
	"github.com/futureboard-dev/colony/pkg/llm"
	"github.com/futureboard-dev/colony/pkg/module"
	"github.com/futureboard-dev/colony/pkg/output"
	"github.com/futureboard-dev/colony/pkg/prompt"
	"github.com/futureboard-dev/colony/pkg/storage"
	"github.com/spf13/cobra"
)

var swarmCmd = &cobra.Command{
	Use:   "swarm",
	Short: "Multi-agent pipeline: coordinator → scout → build → review",
	Long: `Modes:
  quick     build only (small tasks, 1 agent)
  standard  scout → build → review (default)
  full      coordinator → scout → build → review (epics, multi-subtask)

Each role can use a different model. Configure in .colony/config.json:
  { "roles": { "coordinator": {...}, "engineer": {...}, "reviewer": {...} } }`,
	RunE: runSwarm,
}

var (
	swarmSpec        string
	swarmLang        string
	swarmMode        string
	swarmNoFormat    bool
	swarmNoPR        bool
	swarmReviewDepth string
)

func init() {
	swarmCmd.Flags().StringVar(&swarmSpec, "spec", "", "spec markdown file")
	swarmCmd.Flags().StringVar(&swarmLang, "lang", "", "language: typescript, python, go")
	swarmCmd.Flags().StringVar(&swarmMode, "mode", "standard", "quick | standard | full")
	swarmCmd.Flags().BoolVar(&swarmNoFormat, "no-format", false, "skip the format gate")
	swarmCmd.Flags().BoolVar(&swarmNoPR, "no-pr", false, "skip push and PR creation after successful completion")
	swarmCmd.Flags().StringVar(&swarmReviewDepth, "review-depth", "deep", "fast (1 LLM call/subtask) | deep (4 lenses + synth)")
}

func runSwarm(cmd *cobra.Command, args []string) error {
	if swarmSpec == "" || swarmLang == "" {
		return fmt.Errorf("--spec and --lang required\n\nExamples:\n  colony swarm --spec SPEC.md --lang typescript\n  colony swarm --spec SPEC.md --lang go --mode full")
	}
	if _, err := os.Stat(swarmSpec); err != nil {
		return fmt.Errorf("spec file not found: %s", swarmSpec)
	}
	if swarmMode != "quick" && swarmMode != "standard" && swarmMode != "full" {
		return fmt.Errorf("--mode must be quick, standard, or full")
	}
	if swarmReviewDepth != "fast" && swarmReviewDepth != "deep" {
		return fmt.Errorf("--review-depth must be fast or deep")
	}

	cfg, root, err := loadConfig()
	if err != nil {
		return err
	}
	if err := module.EnsureLogDir(root); err != nil {
		return err
	}

	ts := time.Now().Format("20060102-150405")
	swarmDir := filepath.Join(module.LogDir(root), "swarm-"+ts)
	for _, d := range []string{"subtasks", "scouted", "reviews"} {
		if err := os.MkdirAll(filepath.Join(swarmDir, d), 0755); err != nil {
			return err
		}
	}

	logFile, err := os.Create(filepath.Join(swarmDir, "swarm.log"))
	if err != nil {
		return err
	}
	defer logFile.Close()
	heatmap := output.NewHeatmapWriter(os.Stdout)
	statusLine = output.NewStatusLine(os.Stdout, heatmap)
	defer statusLine.Close()
	statusLine.SetState(output.StateWorking)
	statusLine.SetMessage("swarm starting")
	out := io.MultiWriter(statusLine, logFile)

	specData, err := os.ReadFile(swarmSpec)
	if err != nil {
		return err
	}
	specContent := string(specData)
	projectName := module.ProjectName(root)
	baseBranch := module.DefaultBranch()
	langs, err := module.CommandsFor(swarmLang)
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	// ── Record run facts (status/tally in SQLite; raw output stays in swarmDir) ──
	runID := "swarm-" + ts
	store := openRunStore(root, out)
	if store != nil {
		defer func() { _ = store.Close() }()
		_ = store.InsertRun(storage.Run{
			ID: runID, Kind: "swarm", Project: projectName,
			Language: swarmLang, Mode: swarmMode, Status: "running",
			LogPath: swarmDir, StartedAt: time.Now(),
		})
	}

	fmt.Fprintf(out, "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(out, "🐝 SWARM STARTING\nProject: %s | Language: %s | Mode: %s\nSpec: %s | Base: %s\nLog dir: %s\n",
		projectName, swarmLang, swarmMode, swarmSpec, baseBranch, swarmDir)
	fmt.Fprintf(out, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	// ── QUICK MODE ─────────────────────────────────────────────────────────────
	if swarmMode == "quick" {
		statusLine.SetState(output.StateWorking)
		statusLine.SetMessage("quick build")
		fmt.Fprintf(out, "▶ QUICK MODE: single build agent\n")
		subtaskFile := filepath.Join(swarmDir, "subtasks", "subtask-1.md")
		if err := module.CopyFile(swarmSpec, subtaskFile); err != nil {
			return err
		}
		branch, worktreePath, err := swarmBuild(ctx, root, projectName, baseBranch, swarmLang, langs, subtaskFile, cfg, out, swarmNoFormat)
		if err != nil {
			return err
		}
		prURL, prErr := pushAndCreatePR(worktreePath, branch, baseBranch, swarmNoPR, out)
		finishRun(store, runID, "complete", branch)
		statusLine.SetState(output.StateIdle)
		statusLine.SetMessage("")
		fmt.Fprintf(out, "\n✅ Quick build complete\n  Branch: %s\n", branch)
		if prErr != nil {
			fmt.Fprintf(out, "  %s⚠ push/PR failed: %v%s\n", ansiYellow, prErr, ansiReset)
		} else if prURL != "" {
			fmt.Fprintf(out, "  PR:      %s\n", prURL)
		}
		fmt.Fprintf(out, "  Cleanup: colony task done %s\n", branch)
		return nil
	}

	// ── STEP 1: Coordinator (full mode only) ───────────────────────────────────
	if swarmMode == "full" {
		statusLine.SetState(output.StateThinking)
		statusLine.SetMessage("coordinator decomposing")
		fmt.Fprintf(out, "▶ STEP 1/4  Coordinator: decomposing task\n")
		coordExec := llm.New(cfg.Role("coordinator"))
		coordP, err := prompt.Coordinator(specContent)
		if err != nil {
			return err
		}
		var coordOut strings.Builder
		if err := coordExec.RunHeadless(ctx, root, coordP, io.MultiWriter(&coordOut, out)); err != nil {
			return fmt.Errorf("coordinator failed: %w", err)
		}
		subtasks := parseSubtasks(coordOut.String())
		if len(subtasks) == 0 {
			return fmt.Errorf("coordinator produced no subtasks")
		}
		for i, st := range subtasks {
			path := filepath.Join(swarmDir, "subtasks", fmt.Sprintf("subtask-%d.md", i+1))
			if err := os.WriteFile(path, []byte(st), 0644); err != nil {
				return err
			}
		}
		fmt.Fprintf(out, "✓ Coordinator produced %d subtask(s)\n", len(subtasks))
	} else {
		// standard: treat whole spec as subtask-1
		statusLine.SetState(output.StateWorking)
		statusLine.SetMessage("preparing subtasks")
		if err := module.CopyFile(swarmSpec, filepath.Join(swarmDir, "subtasks", "subtask-1.md")); err != nil {
			return err
		}
	}

	// ── Collect subtask files ──────────────────────────────────────────────────
	subtaskFiles, err := filepath.Glob(filepath.Join(swarmDir, "subtasks", "subtask-*.md"))
	if err != nil || len(subtaskFiles) == 0 {
		return fmt.Errorf("no subtask files found")
	}
	fmt.Fprintf(out, "  Subtasks to process: %d\n", len(subtaskFiles))

	total := 3
	step := 1
	if swarmMode == "full" {
		total = 4
		step = 2
	}

	// ── Scout ──────────────────────────────────────────────────────────────────
	statusLine.SetState(output.StateThinking)
	statusLine.SetMessage("scouting specs")
	fmt.Fprintf(out, "\n▶ STEP %d/%d  Scout: enriching specs\n", step, total)
	scoutExec := llm.New(cfg.Role("scout"))
	for _, sf := range subtaskFiles {
		id := subtaskID(sf)
		fmt.Fprintf(out, "  🔍 Scouting subtask %s...\n", id)
		stData, err := os.ReadFile(sf)
		if err != nil {
			return err
		}
		scoutP, err := prompt.Scout(string(stData))
		if err != nil {
			return err
		}
		var scoutOut strings.Builder
		if err := scoutExec.RunHeadless(ctx, root, scoutP, io.MultiWriter(&scoutOut, out)); err != nil {
			fmt.Fprintf(out, "  ⚠ scout failed for subtask %s — using original spec\n", id)
			continue
		}
		scoutedPath := filepath.Join(swarmDir, "scouted", fmt.Sprintf("subtask-%s-scouted.md", id))
		os.WriteFile(scoutedPath, []byte(scoutOut.String()), 0644) //nolint:errcheck
	}
	step++

	// ── Build ──────────────────────────────────────────────────────────────────
	statusLine.SetState(output.StateWorking)
	statusLine.SetMessage("building subtasks")
	fmt.Fprintf(out, "\n▶ STEP %d/%d  Build: implementing subtasks\n", step, total)
	type built struct{ id, branch, worktreePath string }
	var builtList []built

	for _, sf := range subtaskFiles {
		id := subtaskID(sf)
		statusLine.SetMessage("building subtask " + id)
		fmt.Fprintf(out, "\n  🔨 Building subtask %s...\n", id)

		buildSpec := sf
		scoutedPath := filepath.Join(swarmDir, "scouted", fmt.Sprintf("subtask-%s-scouted.md", id))
		if _, err := os.Stat(scoutedPath); err == nil {
			buildSpec = scoutedPath
		}

		branch, worktreePath, err := swarmBuild(ctx, root, projectName, baseBranch, swarmLang, langs, buildSpec, cfg, out, swarmNoFormat)
		if err != nil {
			fmt.Fprintf(out, "✗ Subtask %s FAILED: %v\n", id, err)
			return err
		}
		statusFile := filepath.Join(swarmDir, fmt.Sprintf("build-subtask-%s.status", id))
		os.WriteFile(statusFile, []byte("DONE:"+branch), 0644) //nolint:errcheck
		builtList = append(builtList, built{id, branch, worktreePath})
	}
	step++

	// ── Review ─────────────────────────────────────────────────────────────────
	statusLine.SetState(output.StateThinking)
	statusLine.SetMessage("reviewing builds")
	fmt.Fprintf(out, "\n▶ STEP %d/%d  Review: checking all builds\n", step, total)
	allApproved := true
	type result struct{ id, branch, worktreePath, decision string }
	var results []result

	fmt.Fprintf(out, "  (review-depth=%s)\n", swarmReviewDepth)
	reviewExec := llm.New(cfg.Role("reviewer"))

	for _, b := range builtList {
		statusLine.SetMessage("reviewing subtask " + b.id)
		fmt.Fprintf(out, "  🔎 Reviewing subtask %s (%s)...\n", b.id, b.branch)
		diff, _ := module.GitDiff(root, baseBranch, b.branch)

		var decision, verdict, reviewPath string
		var reviewErr error

		if swarmReviewDepth == "fast" {
			decision, verdict, reviewPath, reviewErr = swarmReviewFast(ctx, reviewExec, root, swarmDir, b.id, diff, out)
		} else {
			decision, verdict, reviewPath, reviewErr = swarmReviewDeep(ctx, cfg, swarmDir, b.id, diff, statusLine)
		}

		if reviewErr != nil {
			fmt.Fprintf(out, "  ✗ Subtask %s ERROR: %v\n", b.id, reviewErr)
			allApproved = false
			continue
		}

		_ = os.WriteFile(filepath.Join(swarmDir, "reviews", fmt.Sprintf("subtask-%s.decision", b.id)), []byte(decision), 0644)
		results = append(results, result{b.id, b.branch, b.worktreePath, decision})
		switch {
		case decision != "APPROVED":
			allApproved = false
			fmt.Fprintf(out, "  ✗ Subtask %s REJECTED (verdict=%s)\n    Review: %s\n", b.id, verdict, reviewPath)
		case verdict == "WARN":
			fmt.Fprintf(out, "  ⚠ Subtask %s APPROVED with warnings\n    Review: %s\n", b.id, reviewPath)
		default:
			fmt.Fprintf(out, "  ✓ Subtask %s APPROVED\n", b.id)
		}
	}

	// ── Summary ────────────────────────────────────────────────────────────────
	statusLine.SetState(output.StateIdle)
	statusLine.SetMessage("")
	approved, rejected := 0, 0
	for _, r := range results {
		if r.decision == "APPROVED" {
			approved++
		} else {
			rejected++
		}
	}
	if store != nil {
		now := time.Now()
		_ = store.UpdateRun(storage.Run{
			ID: runID, Status: "complete", Approved: approved, Rejected: rejected, FinishedAt: &now,
		})
	}
	fmt.Fprintf(out, "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(out, "🐝 SWARM COMPLETE\nMode: %s | Subtasks: %d | Language: %s\n\n", swarmMode, len(subtaskFiles), swarmLang)
	for _, r := range results {
		mark := "✓"
		if r.decision != "APPROVED" {
			mark = "✗"
		}
		fmt.Fprintf(out, "  %s Subtask %s: %s (%s)\n", mark, r.id, r.decision, r.branch)
	}
	fmt.Fprintln(out)
	if allApproved {
		for _, r := range results {
			if prURL, prErr := pushAndCreatePR(r.worktreePath, r.branch, baseBranch, swarmNoPR, out); prErr != nil {
				fmt.Fprintf(out, "  %s⚠ push/PR failed for %s: %v%s\n", ansiYellow, r.branch, prErr, ansiReset)
			} else if prURL != "" {
				fmt.Fprintf(out, "  PR: %s\n", prURL)
			}
		}
		fmt.Fprintf(out, "\nClean up:\n")
		for _, r := range results {
			fmt.Fprintf(out, "  colony task done %s\n", r.branch)
		}
	} else {
		fmt.Fprintf(out, "Some subtasks need attention.\n  Reviews: %s/reviews/\n", swarmDir)
	}
	fmt.Fprintf(out, "\nFull swarm log: %s/swarm.log\n", swarmDir)
	fmt.Fprintf(out, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	return nil
}

// swarmBuild runs the build pipeline for one subtask spec file.
// Returns (branch, worktreePath, error).
func swarmBuild(ctx context.Context, root, projectName, baseBranch, lang string, langs module.LangCommands, specFile string, cfg *config.Config, out io.Writer, skipFormat bool) (string, string, error) {
	specData, err := os.ReadFile(specFile)
	if err != nil {
		return "", "", err
	}
	taskDesc := module.ExtractTaskDesc(string(specData), specFile)
	branch := module.NewBranch(taskDesc)
	ex := llm.New(cfg.Role("engineer"))

	if err := ex.Preflight(); err != nil {
		return "", "", err
	}
	worktreePath, err := module.SetupWorktree(root, projectName, branch, baseBranch)
	if err != nil {
		return "", "", err
	}
	if err := module.CopyFile(specFile, filepath.Join(worktreePath, "SPEC.md")); err != nil {
		return "", "", err
	}
	module.InstallDeps(lang, worktreePath, out)
	writePrompt, err := prompt.Build(lang)
	if err != nil {
		return "", "", err
	}
	if err := ex.RunAgent(ctx, worktreePath, writePrompt, out); err != nil {
		return "", "", fmt.Errorf("build agent: %w", err)
	}
	if err := runGates(ctx, worktreePath, langs, ex, out, skipFormat); err != nil {
		return "", "", err
	}
	commitMsg := fmt.Sprintf("feat: %s\n\nSwarm subtask | Language: %s | Gates: format, typecheck, tests", taskDesc, lang)
	if err := craftCommit(worktreePath, branch, commitMsg, out); err != nil {
		return "", "", err
	}
	return branch, worktreePath, nil
}

func parseSubtasks(output string) []string {
	var subtasks []string
	scanner := bufio.NewScanner(strings.NewReader(output))
	var current strings.Builder
	inSubtask := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "## SUBTASK ") {
			if inSubtask && current.Len() > 0 {
				subtasks = append(subtasks, strings.TrimSpace(current.String()))
				current.Reset()
			}
			inSubtask = true
		}
		if inSubtask {
			current.WriteString(line)
			current.WriteByte('\n')
		}
	}
	if inSubtask && current.Len() > 0 {
		subtasks = append(subtasks, strings.TrimSpace(current.String()))
	}
	return subtasks
}

func parseDecision(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "DECISION: APPROVED") {
			return "APPROVED"
		}
		if strings.HasPrefix(line, "DECISION: REJECTED") {
			return "REJECTED"
		}
	}
	return "UNKNOWN"
}

func subtaskID(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, ".md")
	base = strings.TrimSuffix(base, "-scouted")
	return strings.TrimPrefix(base, "subtask-")
}

// swarmReviewFast runs the legacy single-prompt reviewer (1 LLM call/subtask).
// Returns (decision, verdict, reviewArtifactPath, error).
func swarmReviewFast(ctx context.Context, exec *llm.Executor, root, swarmDir, id, diff string, out io.Writer) (string, string, string, error) {
	specForReview := filepath.Join(swarmDir, "subtasks", fmt.Sprintf("subtask-%s.md", id))
	if sp := filepath.Join(swarmDir, "scouted", fmt.Sprintf("subtask-%s-scouted.md", id)); fileExists(sp) {
		specForReview = sp
	}
	stData, _ := os.ReadFile(specForReview)
	reviewP, err := prompt.Review(string(stData), diff)
	if err != nil {
		return "", "", "", fmt.Errorf("render review prompt: %w", err)
	}
	var reviewOut strings.Builder
	if err := exec.RunHeadless(ctx, root, reviewP, io.MultiWriter(&reviewOut, out)); err != nil {
		return "", "", "", fmt.Errorf("run reviewer: %w", err)
	}
	decision := parseDecision(reviewOut.String())
	path := filepath.Join(swarmDir, "reviews", fmt.Sprintf("subtask-%s-review.md", id))
	_ = os.WriteFile(path, []byte(reviewOut.String()), 0644)
	verdict := decision // fast path doesn't produce PASS/WARN/FAIL — mirror the decision
	return decision, verdict, path, nil
}

// swarmReviewDeep runs the multi-lens reviewer + synthesizer (5 LLM calls/subtask).
func swarmReviewDeep(ctx context.Context, cfg *config.Config, swarmDir, id, diff string, statusLine *output.StatusLine) (string, string, string, error) {
	lenses := []string{"bugs", "slop", "duplication", "security"}
	rawReports, err := runReviewLenses(ctx, cfg, diff, lenses, statusLine, nil, true)
	if err != nil {
		return "", "", "", err
	}
	synthReport, rawSynth, err := synthesizeReview(ctx, cfg, rawReports, statusLine)
	if err != nil {
		return "", "", "", fmt.Errorf("synthesis: %w", err)
	}
	decision := "APPROVED"
	if synthReport.Verdict == "FAIL" {
		decision = "REJECTED"
	}
	path := filepath.Join(swarmDir, "reviews", fmt.Sprintf("subtask-%s-review.json", id))
	_ = os.WriteFile(path, []byte(rawSynth), 0644)
	return decision, synthReport.Verdict, path, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
