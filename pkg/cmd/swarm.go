package cmd

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jirateep/colony/pkg/config"
	"github.com/jirateep/colony/pkg/llm"
	"github.com/jirateep/colony/pkg/module"
	"github.com/jirateep/colony/pkg/prompt"
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
	swarmSpec     string
	swarmLang     string
	swarmMode     string
	swarmNoFormat bool
)

func init() {
	swarmCmd.Flags().StringVar(&swarmSpec, "spec", "", "spec markdown file")
	swarmCmd.Flags().StringVar(&swarmLang, "lang", "", "language: typescript, python, go")
	swarmCmd.Flags().StringVar(&swarmMode, "mode", "standard", "quick | standard | full")
	swarmCmd.Flags().BoolVar(&swarmNoFormat, "no-format", false, "skip the format gate")
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
	out := io.MultiWriter(os.Stdout, logFile)

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

	fmt.Fprintf(out, "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(out, "🐝 SWARM STARTING\nProject: %s | Language: %s | Mode: %s\nSpec: %s | Base: %s\nLog dir: %s\n",
		projectName, swarmLang, swarmMode, swarmSpec, baseBranch, swarmDir)
	fmt.Fprintf(out, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	// ── QUICK MODE ─────────────────────────────────────────────────────────────
	if swarmMode == "quick" {
		fmt.Fprintf(out, "▶ QUICK MODE: single build agent\n")
		subtaskFile := filepath.Join(swarmDir, "subtasks", "subtask-1.md")
		if err := module.CopyFile(swarmSpec, subtaskFile); err != nil {
			return err
		}
		branch, err := swarmBuild(ctx, root, projectName, baseBranch, swarmLang, langs, subtaskFile, cfg, out, swarmNoFormat)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "\n✅ Quick build complete\n  Branch: %s\n  Review: git diff %s..%s\n  Cleanup: colony task done %s\n",
			branch, baseBranch, branch, branch)
		return nil
	}

	// ── STEP 1: Coordinator (full mode only) ───────────────────────────────────
	if swarmMode == "full" {
		fmt.Fprintf(out, "▶ STEP 1/4  Coordinator: decomposing task\n")
		coordExec := llm.New(cfg.Role("coordinator"))
		coordP, err := prompt.Coordinator(specContent)
		if err != nil {
			return err
		}
		var coordOut bytes.Buffer
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
		var scoutOut bytes.Buffer
		if err := scoutExec.RunHeadless(ctx, root, scoutP, io.MultiWriter(&scoutOut, out)); err != nil {
			fmt.Fprintf(out, "  ⚠ scout failed for subtask %s — using original spec\n", id)
			continue
		}
		scoutedPath := filepath.Join(swarmDir, "scouted", fmt.Sprintf("subtask-%s-scouted.md", id))
		os.WriteFile(scoutedPath, scoutOut.Bytes(), 0644) //nolint:errcheck
	}
	step++

	// ── Build ──────────────────────────────────────────────────────────────────
	fmt.Fprintf(out, "\n▶ STEP %d/%d  Build: implementing subtasks\n", step, total)
	type built struct{ id, branch string }
	var builtList []built

	for _, sf := range subtaskFiles {
		id := subtaskID(sf)
		fmt.Fprintf(out, "\n  🔨 Building subtask %s...\n", id)

		buildSpec := sf
		scoutedPath := filepath.Join(swarmDir, "scouted", fmt.Sprintf("subtask-%s-scouted.md", id))
		if _, err := os.Stat(scoutedPath); err == nil {
			buildSpec = scoutedPath
		}

		branch, err := swarmBuild(ctx, root, projectName, baseBranch, swarmLang, langs, buildSpec, cfg, out, swarmNoFormat)
		if err != nil {
			fmt.Fprintf(out, "✗ Subtask %s FAILED: %v\n", id, err)
			return err
		}
		statusFile := filepath.Join(swarmDir, fmt.Sprintf("build-subtask-%s.status", id))
		os.WriteFile(statusFile, []byte("DONE:"+branch), 0644) //nolint:errcheck
		builtList = append(builtList, built{id, branch})
	}
	step++

	// ── Review ─────────────────────────────────────────────────────────────────
	fmt.Fprintf(out, "\n▶ STEP %d/%d  Review: checking all builds\n", step, total)
	reviewExec := llm.New(cfg.Role("reviewer"))
	allApproved := true
	type result struct{ id, branch, decision string }
	var results []result

	for _, b := range builtList {
		fmt.Fprintf(out, "  🔎 Reviewing subtask %s (%s)...\n", b.id, b.branch)
		specForReview := filepath.Join(swarmDir, "subtasks", fmt.Sprintf("subtask-%s.md", b.id))
		if sp := filepath.Join(swarmDir, "scouted", fmt.Sprintf("subtask-%s-scouted.md", b.id)); fileExists(sp) {
			specForReview = sp
		}
		stData, _ := os.ReadFile(specForReview)
		diff, _ := module.GitDiff(root, baseBranch, b.branch)
		reviewP, err := prompt.Review(string(stData), diff)
		if err != nil {
			return err
		}
		var reviewOut bytes.Buffer
		reviewExec.RunHeadless(ctx, root, reviewP, io.MultiWriter(&reviewOut, out)) //nolint:errcheck
		decision := parseDecision(reviewOut.String())
		os.WriteFile(filepath.Join(swarmDir, "reviews", fmt.Sprintf("subtask-%s-review.md", b.id)), reviewOut.Bytes(), 0644) //nolint:errcheck
		os.WriteFile(filepath.Join(swarmDir, "reviews", fmt.Sprintf("subtask-%s.decision", b.id)), []byte(decision), 0644)   //nolint:errcheck
		results = append(results, result{b.id, b.branch, decision})
		if decision != "APPROVED" {
			allApproved = false
			fmt.Fprintf(out, "  ✗ Subtask %s REJECTED\n    Review: %s/reviews/subtask-%s-review.md\n", b.id, swarmDir, b.id)
		} else {
			fmt.Fprintf(out, "  ✓ Subtask %s APPROVED\n", b.id)
		}
	}

	// ── Summary ────────────────────────────────────────────────────────────────
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
		fmt.Fprintf(out, "All subtasks approved. Merge in order:\n")
		for _, r := range results {
			fmt.Fprintf(out, "  git merge %s\n", r.branch)
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
func swarmBuild(ctx context.Context, root, projectName, baseBranch, lang string, langs module.LangCommands, specFile string, cfg *config.Config, out io.Writer, skipFormat bool) (string, error) {
	specData, err := os.ReadFile(specFile)
	if err != nil {
		return "", err
	}
	taskDesc := module.ExtractTaskDesc(string(specData), specFile)
	branch := module.NewBranch(taskDesc)
	ex := llm.New(cfg.Role("engineer"))

	if err := ex.Preflight(); err != nil {
		return "", err
	}
	worktreePath, err := module.SetupWorktree(root, projectName, branch, baseBranch)
	if err != nil {
		return "", err
	}
	if err := module.CopyFile(specFile, filepath.Join(worktreePath, "SPEC.md")); err != nil {
		return "", err
	}
	writePrompt, err := prompt.Build(lang)
	if err != nil {
		return "", err
	}
	if err := ex.RunAgent(ctx, worktreePath, writePrompt, out); err != nil {
		return "", fmt.Errorf("build agent: %w", err)
	}
	if err := runGates(ctx, worktreePath, langs, ex, out, skipFormat); err != nil {
		return "", err
	}
	commitMsg := fmt.Sprintf("feat: %s\n\nSwarm subtask | Language: %s | Gates: format, typecheck, tests", taskDesc, lang)
	if err := bpCommit(worktreePath, branch, commitMsg, out); err != nil {
		return "", err
	}
	return branch, nil
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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
