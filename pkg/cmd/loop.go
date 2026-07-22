package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/futureboard-dev/colony/pkg/config"
	"github.com/futureboard-dev/colony/pkg/llm"
	"github.com/futureboard-dev/colony/pkg/mission/blueprint"
	"github.com/futureboard-dev/colony/pkg/mission/graph"
	"github.com/futureboard-dev/colony/pkg/mission/nodes"
	"github.com/futureboard-dev/colony/pkg/module"
	"github.com/futureboard-dev/colony/pkg/observe"
	"github.com/futureboard-dev/colony/pkg/prompt"
	"github.com/futureboard-dev/colony/pkg/storage"
	"github.com/spf13/cobra"
)

// loopPollInterval is the sleep between passes in --watch daemon mode.
const loopPollInterval = 30 * time.Second

var loopCmd = &cobra.Command{
	Use:   "loop",
	Short: "Autonomous steward: build→gate→fix loop",
	Long: `Runs the autonomous build-gate-fix loop on open tasks from the queue.
Picks the next actionable task, runs BuildGateFix on it, and records the outcome.

Flags:
  --once          Run a single pass (pick one task, process it, exit)
  --watch         Run as a long-lived daemon (polls queue, observes CI/PR)
  --max-passes    Stop after N total passes (0 = unlimited)
  --max-cycles    Cap the inner fix loop per task (default 3)
  --escalate-to   Model to use for escalation role (default: off)
  --lang          Language for gates (default: go)
  --idle          Consecutive idle passes before stopping (default 10)`,
	RunE: runLoop,
}

var (
	loopOnce       bool
	loopWatch      bool
	loopMaxPasses  int
	loopMaxCycles  int
	loopEscalateTo string
	loopLang       string
	loopIdleLimit  int
	loopRetryBlock bool
	loopReview     bool
)

func init() {
	loopCmd.Flags().BoolVar(&loopOnce, "once", false, "run a single pass and exit")
	loopCmd.Flags().BoolVar(&loopWatch, "watch", false, "run as a long-lived daemon (polls queue, observes CI/PR)")
	loopCmd.Flags().IntVar(&loopMaxPasses, "max-passes", 0, "stop after N total passes (0 = unlimited)")
	loopCmd.Flags().IntVar(&loopMaxCycles, "max-cycles", 3, "cap the inner fix loop per task")
	loopCmd.Flags().StringVar(&loopEscalateTo, "escalate-to", "", "model for escalation role (default: off)")
	loopCmd.Flags().StringVar(&loopLang, "lang", "go", "language for gates")
	loopCmd.Flags().IntVar(&loopIdleLimit, "idle", 10, "consecutive idle passes before stopping")
	loopCmd.Flags().BoolVar(&loopRetryBlock, "retry-blocked", false, "re-queue blocked tasks (needs-fix) to continue them in their existing worktree")
	loopCmd.Flags().BoolVar(&loopReview, "review", false, "run an LLM review gate before marking a task done (also auto-enabled when a 'review' role is configured)")

	loopCmd.AddCommand(loopStatusCmd)
	loopCmd.AddCommand(loopScheduleCmd)
	loopCmd.AddCommand(loopClearCmd)
	loopCmd.AddCommand(loopRetryReviewCmd)
	loopCmd.AddCommand(loopRetryGateCmd)

	loopRunCmd.Flags().StringVar(&loopRunLang, "lang", "", "language for gates: typescript, python, go (required)")
	loopCmd.AddCommand(loopRunCmd)
}

// loopRetryReviewCmd runs just the review step on a blocked task's existing
// worktree, skipping builder and gate. Useful when review failed due to an
// auth/config issue and the built code is already correct.
var loopRetryReviewCmd = &cobra.Command{
	Use:   "retry-review <task-id>",
	Short: "Re-run only the review gate on a blocked task's existing worktree",
	Args:  cobra.ExactArgs(1),
	RunE:  runLoopRetryReview,
}

func runLoopRetryReview(cmd *cobra.Command, args []string) error {
	taskID := args[0]

	cfg, root, err := loadConfig()
	if err != nil {
		return err
	}

	store, err := openLoopStore(root)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	tasks, err := store.QueryTasks(storage.TaskFilter{ID: taskID})
	if err != nil {
		return fmt.Errorf("query task: %w", err)
	}
	if len(tasks) == 0 {
		return fmt.Errorf("task %q not found", taskID)
	}
	task := &tasks[0]

	if task.Branch == "" {
		return fmt.Errorf("task %q has no recorded branch — has it been built yet?", taskID)
	}

	projectName := module.ProjectName(root)
	workdir := module.WorktreePath(projectName, task.Branch)
	if info, statErr := os.Stat(workdir); statErr != nil || !info.IsDir() {
		return fmt.Errorf("worktree %q not found — cannot retry review without existing build", workdir)
	}

	reviewCfg := cfg.CommandRole("loop", graph.RoleReview)
	if reviewCfg.Provider == "" {
		return fmt.Errorf("no review role configured in .colony/config.json under commands.loop.roles.review")
	}

	fmt.Fprintf(os.Stderr, "%sloop: retrying review for task %q on worktree %s%s\n", ansiBlue, taskID, workdir, ansiReset)

	node := nodes.NewReviewNode("review", reviewCfg)
	out, runErr := node.Run(cmd.Context(), graph.Input{
		Params: map[string]any{"workdir": workdir},
	})

	if runErr != nil {
		_ = store.UpdateTaskState(task.ID, "blocked", runErr.Error())
		return fmt.Errorf("review failed: %w", runErr)
	}

	if out.Envelope.Decision != graph.APPROVED {
		feedback := out.Envelope.Feedback
		_ = store.UpdateTaskState(task.ID, "blocked", feedback)
		fmt.Fprintf(os.Stderr, "%sloop: review REJECTED — task blocked\n%s%s\n", ansiRed, feedback, ansiReset)
		return nil
	}

	fmt.Fprintf(os.Stderr, "%sloop: review APPROVED — integrating%s\n", ansiGreen, ansiReset)

	lang := task.Lang
	if lang == "" {
		lang = loopLang
	}
	baseBranch := task.BaseBranch
	if baseBranch == "" {
		baseBranch = module.DefaultBranch()
	}

	if err := integrateTask(cmd.Context(), cfg, workdir, task.Branch, baseBranch, lang, task, store); err != nil {
		return nil // integrateTask already marked blocked
	}

	prURL, finishErr := finishTask(workdir, task.Branch, baseBranch, missionLabel(task))
	if finishErr != nil {
		fmt.Fprintf(os.Stderr, "%sloop: task %q done (review passed) but delivery failed: %v%s\n", ansiYellow, task.ID, finishErr, ansiReset)
		_ = store.UpdateTaskState(task.ID, "done", "delivery failed: "+finishErr.Error())
		return nil
	}

	fmt.Fprintf(os.Stderr, "%sloop: task %q done → %s%s\n", ansiGreen, task.ID, prURL, ansiReset)
	_ = store.UpdateTaskState(task.ID, "done", prURL)
	return nil
}

// loopRetryGateCmd re-runs the gate→fix flow on a task's existing worktree,
// skipping the builder. Useful when the code is already written and only the
// gate (and optional review) needs to pass.
var loopRetryGateCmd = &cobra.Command{
	Use:   "retry-gate <task-id>",
	Short: "Re-run from the gate step on a blocked task's existing worktree",
	Args:  cobra.ExactArgs(1),
	RunE:  runLoopRetryGate,
}

func runLoopRetryGate(cmd *cobra.Command, args []string) error {
	taskID := args[0]

	cfg, root, err := loadConfig()
	if err != nil {
		return err
	}

	store, err := openLoopStore(root)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	tasks, err := store.QueryTasks(storage.TaskFilter{ID: taskID})
	if err != nil {
		return fmt.Errorf("query task: %w", err)
	}
	if len(tasks) == 0 {
		return fmt.Errorf("task %q not found", taskID)
	}
	task := &tasks[0]

	if task.Branch == "" {
		return fmt.Errorf("task %q has no recorded branch — has it been built yet?", taskID)
	}

	projectName := module.ProjectName(root)
	workdir := module.WorktreePath(projectName, task.Branch)
	if info, statErr := os.Stat(workdir); statErr != nil || !info.IsDir() {
		return fmt.Errorf("worktree %q not found — cannot retry gate without existing build", workdir)
	}

	lang := task.Lang
	if lang == "" {
		lang = loopLang
	}

	fmt.Fprintf(os.Stderr, "%sloop: retrying from gate for task %q on worktree %s%s\n", ansiBlue, taskID, workdir, ansiReset)

	input, err := taskInput(task)
	if err != nil {
		return err
	}

	baseBranch := task.BaseBranch
	if baseBranch == "" {
		baseBranch = module.DefaultBranch()
	}
	opts := blueprint.BuildGateFixOpts{
		Name:        "retry-gate-" + missionLabel(task),
		Input:       input,
		Lang:        lang,
		Base:        baseBranch,
		Workdir:     workdir,
		MaxCycles:   loopMaxCycles,
		SkipBuilder: true,
	}
	if loopReview || cfg.HasCommandRole("loop", graph.RoleReview) {
		opts.ReviewRole = graph.RoleReview
	}

	m := blueprint.BuildGateFix(opts)

	reg := graph.NewRegistry()
	nodes.Register(reg)
	g, buildErr := graph.BuildGraph(m, reg, func(role string) config.LLMConfig {
		return cfg.CommandRole("loop", role)
	})
	if buildErr != nil {
		return fmt.Errorf("build graph: %w", buildErr)
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sessID := "retry-gate-" + strings.TrimPrefix(task.Branch, "agent/")
	if err := store.InsertSession(storage.Session{
		ID:          sessID,
		MissionName: m.Name,
		StartedAt:   time.Now(),
		Status:      "running",
	}); err != nil {
		return fmt.Errorf("insert session: %w", err)
	}

	runner := graph.NewRunner()
	out, runErr := runner.Run(ctx, m, g, sessID, store)

	if runErr != nil {
		_ = store.UpdateSession(sessID, "failed", time.Now())
		feedback := extractFeedback(out, runErr)
		_ = store.UpdateTaskState(task.ID, "blocked", feedback)
		return fmt.Errorf("gate/fix failed: %w", runErr)
	}

	_ = store.UpdateSession(sessID, "completed", time.Now())

	if err := integrateTask(cmd.Context(), cfg, workdir, task.Branch, baseBranch, lang, task, store); err != nil {
		return nil // integrateTask already marked blocked
	}

	prURL, finishErr := finishTask(workdir, task.Branch, baseBranch, missionLabel(task))
	if finishErr != nil {
		fmt.Fprintf(os.Stderr, "%sloop: task %q done (gate passed) but delivery failed: %v%s\n", ansiYellow, task.ID, finishErr, ansiReset)
		_ = store.UpdateTaskState(task.ID, "done", "delivery failed: "+finishErr.Error())
		return nil
	}

	fmt.Fprintf(os.Stderr, "%sloop: task %q done → %s%s\n", ansiGreen, task.ID, prURL, ansiReset)
	_ = store.UpdateTaskState(task.ID, "done", prURL)
	return nil
}

var loopRunLang string

// loopRunCmd processes a single task by ID through the full build-gate-fix
// flow, bypassing queue selection. The --lang flag is required and is persisted
// to the task so later resumes use the same toolchain.
var loopRunCmd = &cobra.Command{
	Use:   "run <task-id>",
	Short: "Run a single task by ID with an explicit gate language",
	Long: `Processes one task by its ID through the full build-gate-fix flow,
regardless of its current queue position. --lang is required and overrides any
language stored on the task (and is persisted for later resumes).`,
	Args: cobra.ExactArgs(1),
	RunE: runLoopRun,
}

func runLoopRun(cmd *cobra.Command, args []string) error {
	taskID := args[0]

	if loopRunLang == "" {
		return fmt.Errorf("--lang is required (typescript, python, go)")
	}
	if _, err := module.CommandsFor(loopRunLang); err != nil {
		return err
	}

	cfg, root, err := loadConfig()
	if err != nil {
		return err
	}

	store, err := openLoopStore(root)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	tasks, err := store.QueryTasks(storage.TaskFilter{ID: taskID})
	if err != nil {
		return fmt.Errorf("query task: %w", err)
	}
	if len(tasks) == 0 {
		return fmt.Errorf("task %q not found", taskID)
	}
	task := &tasks[0]

	if task.Lang != loopRunLang {
		if err := store.UpdateTaskLang(task.ID, loopRunLang); err != nil {
			return fmt.Errorf("persist lang: %w", err)
		}
		task.Lang = loopRunLang
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := processTask(ctx, cfg, root, store, task); err != nil {
		_ = markTaskBlocked(store, task.ID)
		return fmt.Errorf("task %q failed: %w", task.ID, err)
	}
	return nil
}

func runLoop(cmd *cobra.Command, args []string) error {
	cfg, root, err := loadConfig()
	if err != nil {
		return err
	}

	if loopWatch {
		return runWatchDaemon(cmd.Context(), cfg, root)
	}

	_ = removeSentinel(root)

	store, err := openLoopStore(root)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	if loopRetryBlock {
		n, rqErr := requeueBlocked(store)
		if rqErr != nil {
			return fmt.Errorf("retry-blocked: %w", rqErr)
		}
		fmt.Fprintf(os.Stderr, "%sloop: re-queued %d blocked task(s)%s\n", ansiBlue, n, ansiReset)
	}

	maxPasses := loopMaxPasses
	idleLimit := loopIdleLimit
	if idleLimit <= 0 {
		idleLimit = 10
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	totalPasses := 0
	consecutiveIdle := 0

	for {
		if maxPasses > 0 && totalPasses >= maxPasses {
			fmt.Fprintf(os.Stderr, "%sloop: reached max-passes (%d), exiting%s\n", ansiBlue, maxPasses, ansiReset)
			break
		}

		task, err := pickNextTask(ctx, store)
		if err != nil {
			return fmt.Errorf("pick task: %w", err)
		}
		if task == nil {
			consecutiveIdle++
			fmt.Fprintf(os.Stderr, "%sidle: no tasks ready (%d/%d)%s\n", ansiBlue, consecutiveIdle, idleLimit, ansiReset)
			if consecutiveIdle >= idleLimit {
				fmt.Fprintf(os.Stderr, "%sloop: idle limit reached (%d consecutive), exiting%s\n", ansiBlue, idleLimit, ansiReset)
				break
			}
			if loopOnce {
				break
			}
			if err := sleepInterruptibly(ctx); err != nil {
				return nil
			}
			if sentinelExists(root) {
				fmt.Fprintf(os.Stderr, "%sloop: stop sentinel detected, exiting%s\n", ansiBlue, ansiReset)
				_ = removeSentinel(root)
				break
			}
			continue
		}

		consecutiveIdle = 0
		totalPasses++

		if err := processTask(ctx, cfg, root, store, task); err != nil {
			fmt.Fprintf(os.Stderr, "%sloop: task %q failed: %v%s\n", ansiRed, task.ID, err, ansiReset)
			_ = markTaskBlocked(store, task.ID)
		}

		if loopOnce {
			break
		}

		if sentinelExists(root) {
			fmt.Fprintf(os.Stderr, "%sloop: stop sentinel detected, exiting%s\n", ansiBlue, ansiReset)
			_ = removeSentinel(root)
			break
		}

		if ctx.Err() != nil {
			fmt.Fprintf(os.Stderr, "%sloop: interrupted, exiting%s\n", ansiBlue, ansiReset)
			break
		}
	}

	return nil
}

func sleepInterruptibly(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return nil
	}
}

func sentinelPath(root string) string {
	return filepath.Join(root, ".colony", "loop.stop")
}

func sentinelExists(root string) bool {
	_, err := os.Stat(sentinelPath(root))
	return err == nil
}

func removeSentinel(root string) error {
	err := os.Remove(sentinelPath(root))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func pickNextTask(ctx context.Context, store *storage.SQLiteStore) (*storage.Task, error) {
	tasks, err := store.QueryTasks(storage.TaskFilter{States: []string{"open", "needs-fix"}})
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, nil
	}
	return &tasks[0], nil
}

// parseGateOverrides converts a task's comma-joined GateOverrides (e.g.
// "format,lint", as set by `task add --no-format`) into a skip set. Blank
// entries are ignored. Always returns a non-nil map so callers can add to it.
func parseGateOverrides(s string) map[string]bool {
	skip := map[string]bool{}
	for _, name := range strings.Split(s, ",") {
		if name = strings.TrimSpace(name); name != "" {
			skip[name] = true
		}
	}
	return skip
}

func processTask(ctx context.Context, cfg *config.Config, root string, store *storage.SQLiteStore, task *storage.Task) error {
	lang := task.Lang
	if lang == "" {
		lang = loopLang
	}

	input, err := taskInput(task)
	if err != nil {
		return err
	}

	projectName := module.ProjectName(root)
	workdir, branch, err := resolveWorktree(root, projectName, task)
	if err != nil {
		return fmt.Errorf("setup worktree: %w", err)
	}
	module.InstallDeps(lang, workdir, os.Stderr)
	if task.Branch != branch {
		if err := store.UpdateTaskBranch(task.ID, branch); err != nil {
			return fmt.Errorf("record task branch: %w", err)
		}
	}

	if err := store.IncrementCycle(task.ID); err != nil {
		return fmt.Errorf("increment cycle: %w", err)
	}

	if err := os.WriteFile(filepath.Join(workdir, "SPEC.md"), []byte(input), 0644); err != nil {
		return fmt.Errorf("write SPEC.md: %w", err)
	}

	baseBranch := task.BaseBranch
	if baseBranch == "" {
		baseBranch = module.DefaultBranch()
	}
	opts := blueprint.BuildGateFixOpts{
		Name:      "loop-" + missionLabel(task),
		Input:     input,
		Lang:      lang,
		Base:      baseBranch,
		Workdir:   workdir,
		MaxCycles: loopMaxCycles,
		SkipGates: parseGateOverrides(task.GateOverrides),
	}
	if loopEscalateTo != "" {
		opts.EscalationRole = graph.RoleEscalation
	}
	if loopReview || cfg.HasCommandRole("loop", graph.RoleReview) {
		opts.ReviewRole = graph.RoleReview
	}

	m := blueprint.BuildGateFix(opts)

	reg := graph.NewRegistry()
	nodes.Register(reg)
	g, err := graph.BuildGraph(m, reg, func(role string) config.LLMConfig {
		return cfg.CommandRole("loop", role)
	})
	if err != nil {
		return fmt.Errorf("build graph: %w", err)
	}

	sessID := "loop-" + strings.TrimPrefix(branch, "agent/")
	if err := store.InsertSession(storage.Session{
		ID:          sessID,
		MissionName: m.Name,
		StartedAt:   time.Now(),
		Status:      "running",
	}); err != nil {
		return fmt.Errorf("insert session: %w", err)
	}

	runner := graph.NewRunner()
	out, runErr := runner.Run(ctx, m, g, sessID, store)

	if runErr != nil {
		if ctx.Err() != nil {
			_ = store.UpdateSession(sessID, "interrupted", time.Now())
			_ = store.UpdateTaskState(task.ID, "needs-fix", "")
			return nil
		}

		_ = store.UpdateSession(sessID, "failed", time.Now())

		if _, ok := runErr.(*graph.ErrStuck); ok {
			feedback := extractFeedback(out, runErr)
			_ = store.UpdateTaskState(task.ID, "blocked", feedback)
			fmt.Fprintf(os.Stderr, "%sloop: task %q stuck (identical failure repeated), blocked%s\n", ansiRed, task.ID, ansiReset)
			return nil
		}

		if _, ok := runErr.(*graph.ErrMaxCycles); ok && loopEscalateTo != "" {
			if err := escalateTask(ctx, cfg, root, store, task, lang, out); err != nil {
				return fmt.Errorf("escalation failed: %w", err)
			}
			return nil
		}

		if _, ok := runErr.(*graph.ErrMaxCycles); ok {
			feedback := extractFeedback(out, runErr)
			_ = store.UpdateTaskState(task.ID, "blocked", feedback)
			fmt.Fprintf(os.Stderr, "%sloop: task %q hit max-cycles, blocked (use --retry-blocked to resume)%s\n", ansiRed, task.ID, ansiReset)
			return nil
		}

		return runErr
	}

	_ = store.UpdateSession(sessID, "completed", time.Now())

	if err := integrateTask(ctx, cfg, workdir, branch, baseBranch, lang, task, store); err != nil {
		return nil
	}

	prURL, finishErr := finishTask(workdir, branch, baseBranch, missionLabel(task))
	if finishErr != nil {
		fmt.Fprintf(os.Stderr, "%sloop: task %q done (gate passed) but delivery failed: %v%s\n", ansiYellow, task.ID, finishErr, ansiReset)
		_ = store.UpdateTaskState(task.ID, "done", "delivery failed: "+finishErr.Error())
		return nil
	}

	fmt.Fprintf(os.Stderr, "%sloop: task %q done → %s%s\n", ansiGreen, task.ID, prURL, ansiReset)
	_ = store.UpdateTaskState(task.ID, "done", prURL)
	return nil
}

// maxFixerOutput bounds the gate output handed to the fixer agent. crush takes
// its prompt as a CLI argument, so an unbounded gate dump (e.g. thousands of
// lint errors) blows past ARG_MAX and the exec fails with "argument list too
// long". Scoping the gate already keeps this small; the cap is a hard backstop.
const maxFixerOutput = 16 * 1024

func capGateOutput(s string) string {
	if len(s) <= maxFixerOutput {
		return s
	}
	return s[:maxFixerOutput] + "\n…(truncated — fix the errors shown above first)\n"
}

func integrateTask(ctx context.Context, cfg *config.Config, workdir, branch, baseBranch, lang string, task *storage.Task, store *storage.SQLiteStore) error {
	out := os.Stderr
	fmt.Fprintf(out, "%sloop: integrating %s with origin/%s%s\n", ansiBlue, branch, baseBranch, ansiReset)

	fetch := exec.Command("git", "fetch", "origin", baseBranch)
	fetch.Dir = workdir
	fetch.Stdout = out
	fetch.Stderr = out
	if err := fetch.Run(); err != nil {
		_ = store.UpdateTaskState(task.ID, "blocked", fmt.Sprintf("git fetch origin/%s failed: %v", baseBranch, err))
		return fmt.Errorf("git fetch: %w", err)
	}

	merge := exec.Command("git", "merge", "FETCH_HEAD")
	merge.Dir = workdir
	merge.Stdout = out
	merge.Stderr = out
	if err := merge.Run(); err != nil {
		abort := exec.Command("git", "merge", "--abort")
		abort.Dir = workdir
		_ = abort.Run()

		conflictFiles := getConflictFiles(workdir)
		feedback := fmt.Sprintf("merge conflict with origin/%s. Conflicting files: %s", baseBranch, strings.Join(conflictFiles, ", "))
		_ = store.UpdateTaskState(task.ID, "blocked", feedback)
		fmt.Fprintf(out, "%sloop: task %q blocked — merge conflict with origin/%s%s\n", ansiRed, task.ID, baseBranch, ansiReset)
		return fmt.Errorf("merge conflict with origin/%s", baseBranch)
	}

	fmt.Fprintf(out, "%sloop: merge clean — re-running gates%s\n", ansiBlue, ansiReset)
	isTS := false
	if l := strings.ToLower(lang); l == "typescript" || l == "ts" {
		isTS = true
		module.InstallDeps(lang, workdir, out)
	}

	// Before gating, auto-fix deterministically and scope format/lint to the
	// task's own changed files. This keeps formatting and mechanically-fixable
	// lint off the (token-costing) fixer agent, and stops pre-existing whole-repo
	// violations on the base branch from failing every integration.
	var changed []string
	skip := parseGateOverrides(task.GateOverrides)
	gate := func() (string, error) { return module.RunGateCaptureAll(lang, workdir, skip) }
	if isTS {
		changed = module.ChangedFiles(workdir, "origin/"+baseBranch)
		if len(changed) > 0 {
			module.AutoFix(lang, workdir, changed, skip["format"], out)
		} else {
			skip["format"], skip["lint"] = true, true // nothing lintable changed
		}
		gate = func() (string, error) { return module.RunGateCaptureScoped(lang, workdir, changed, skip) }
	}

	gateOutput, gateErr := gate()
	if gateErr != nil {
		fmt.Fprintf(out, "%sloop: integration gate failed — asking fixer agent to repair%s\n", ansiYellow, ansiReset)
		fixerCfg := cfg.CommandRole("loop", graph.RoleFixer)
		ex := llm.New(fixerCfg)
		fixPrompt, ferr := prompt.Fix("integration gate", capGateOutput(gateOutput))
		if ferr == nil {
			if aerr := ex.RunAgent(ctx, workdir, fixPrompt, out); aerr != nil {
				fmt.Fprintf(out, "%sloop: fixer agent error: %v%s\n", ansiYellow, aerr, ansiReset)
			}
		}
		if isTS && len(changed) > 0 {
			module.AutoFix(lang, workdir, changed, skip["format"], out) // re-apply deterministic fixes after the agent's edits
		}
		gateOutput, gateErr = gate()
	}
	if gateErr != nil {
		feedback := fmt.Sprintf("integration gate failed after merge with origin/%s:\n%s", baseBranch, gateOutput)
		_ = store.UpdateTaskState(task.ID, "blocked", feedback)
		fmt.Fprintf(out, "%sloop: task %q blocked — integration gate failed after merge%s\n", ansiRed, task.ID, ansiReset)
		return fmt.Errorf("integration gate after merge: %w\n%s", gateErr, gateOutput)
	}

	fmt.Fprintf(out, "%sloop: integration gate passed%s\n", ansiBlue, ansiReset)
	return nil
}

func getConflictFiles(workdir string) []string {
	cmd := exec.Command("git", "diff", "--name-only", "--diff-filter=U")
	cmd.Dir = workdir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var files []string
	for _, f := range lines {
		if f != "" {
			files = append(files, f)
		}
	}
	return files
}

func finishTask(workdir, branch, baseBranch, label string) (string, error) {
	out := os.Stderr

	_ = os.Remove(filepath.Join(workdir, "SPEC.md"))

	mergeBase := exec.Command("git", "merge-base", "HEAD", "origin/"+baseBranch)
	mergeBase.Dir = workdir
	mbOut, mbErr := mergeBase.Output()
	if mbErr == nil {
		mb := strings.TrimSpace(string(mbOut))
		reset := exec.Command("git", "reset", "--soft", mb)
		reset.Dir = workdir
		if err := reset.Run(); err != nil {
			return "", fmt.Errorf("git reset --soft: %w", err)
		}
	}

	add := exec.Command("git", "add", "-A")
	add.Dir = workdir
	if err := add.Run(); err != nil {
		return "", fmt.Errorf("git add: %w", err)
	}

	if diff := exec.Command("git", "diff", "--cached", "--quiet"); runInDir(diff, workdir) == nil {
		return "", fmt.Errorf("no changes to commit — agent produced an empty diff")
	}

	commit := exec.Command("git", "commit", "-m", "loop: "+label)
	commit.Dir = workdir
	commit.Stdout = out
	commit.Stderr = out
	if err := commit.Run(); err != nil {
		return "", fmt.Errorf("git commit: %w", err)
	}
	fmt.Fprintf(out, "%sloop: committed on branch %s%s\n", ansiBlue, branch, ansiReset)

	push := exec.Command("git", "push", "-u", "origin", branch)
	push.Dir = workdir
	push.Stdout = out
	push.Stderr = out
	if err := push.Run(); err != nil {
		return "", fmt.Errorf("git push: %w", err)
	}
	fmt.Fprintf(out, "%sloop: pushed branch %s%s\n", ansiBlue, branch, ansiReset)

	args := []string{
		"pr", "create",
		"--base", baseBranch,
		"--head", branch,
		"--title", branch,
		"--body", fmt.Sprintf("Automated by colony loop.\n\nBranch: `%s`\nBase: `%s`", branch, baseBranch),
	}
	if repo := remoteRepo(workdir); repo != "" {
		args = append(args, "--repo", repo)
	}
	gh := exec.Command("gh", args...)
	gh.Dir = workdir
	var prURL strings.Builder
	gh.Stdout = io.MultiWriter(out, &prURL)
	gh.Stderr = out
	if err := gh.Run(); err != nil {
		return "", fmt.Errorf("gh pr create: %w", err)
	}
	return strings.TrimSpace(prURL.String()), nil
}

func runInDir(cmd *exec.Cmd, dir string) error {
	cmd.Dir = dir
	return cmd.Run()
}

func escalateTask(ctx context.Context, cfg *config.Config, root string, store *storage.SQLiteStore, task *storage.Task, lang string, prevOut *graph.Output) error {
	input, err := taskInput(task)
	if err != nil {
		return err
	}
	opts := blueprint.BuildGateFixOpts{
		Name:      "escalation-" + missionLabel(task),
		Input:     input,
		Lang:      lang,
		MaxCycles: 1,
	}
	opts.EscalationRole = graph.RoleEscalation

	m := blueprint.BuildGateFix(opts)
	reg := graph.NewRegistry()
	nodes.Register(reg)
	g, err := graph.BuildGraph(m, reg, func(role string) config.LLMConfig {
		return cfg.CommandRole("loop", role)
	})
	if err != nil {
		return fmt.Errorf("escalation build graph: %w", err)
	}

	sessID := fmt.Sprintf("escalation-%s-%s", task.ID, time.Now().Format("20060102-150405"))
	if err := store.InsertSession(storage.Session{
		ID:          sessID,
		MissionName: m.Name,
		StartedAt:   time.Now(),
		Status:      "running",
	}); err != nil {
		return err
	}

	runner := graph.NewRunner()
	_, runErr := runner.Run(ctx, m, g, sessID, store)
	_ = store.UpdateSession(sessID, "completed", time.Now())

	if runErr != nil {
		fmt.Fprintf(os.Stderr, "%sescalation failed for task %q, marking blocked%s\n", ansiRed, task.ID, ansiReset)
		_ = store.UpdateTaskState(task.ID, "blocked", extractFeedback(nil, runErr))
		return markTaskBlocked(store, task.ID)
	}

	_ = store.UpdateTaskState(task.ID, "done", "")
	return nil
}

func markTaskBlocked(store *storage.SQLiteStore, taskID string) error {
	return store.UpdateTaskState(taskID, "blocked", "")
}

func requeueBlocked(store *storage.SQLiteStore) (int, error) {
	blocked, err := store.QueryTasks(storage.TaskFilter{States: []string{"blocked"}})
	if err != nil {
		return 0, err
	}
	for _, t := range blocked {
		if err := store.UpdateTaskState(t.ID, "needs-fix", t.LastFeedback); err != nil {
			return 0, err
		}
	}
	return len(blocked), nil
}

func openLoopStore(root string) (*storage.SQLiteStore, error) {
	dbPath := storage.DefaultDBPath()
	if root != "" {
		dbPath = filepath.Join(root, ".colony", "missions.db")
	}
	return storage.Open(dbPath)
}

func resolveWorktree(root, projectName string, task *storage.Task) (workdir, branch string, err error) {
	if task.Branch != "" {
		path := module.WorktreePath(projectName, task.Branch)
		if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
			return path, task.Branch, nil
		}
	}

	baseBranch := task.BaseBranch
	if baseBranch == "" {
		baseBranch = module.DefaultBranch()
	}
	branch = module.NewBranch(branchDesc(task))
	workdir, err = module.SetupWorktreeLocal(root, projectName, branch, baseBranch)
	return workdir, branch, err
}

func branchDesc(task *storage.Task) string {
	if task.Description != "" {
		return task.Description
	}
	if task.SpecPath != "" {
		base := filepath.Base(task.SpecPath)
		if data, err := os.ReadFile(task.SpecPath); err == nil {
			if d := module.ExtractTaskDesc(string(data), base); d != "" {
				return d
			}
		}
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	return task.ID
}

func missionLabel(task *storage.Task) string {
	if label := module.Slugify(branchDesc(task)); label != "" {
		return label
	}
	return task.ID
}

func taskInput(task *storage.Task) (string, error) {
	if task.SpecPath == "" {
		return task.Description, nil
	}
	data, err := os.ReadFile(task.SpecPath)
	if err != nil {
		return "", fmt.Errorf("read spec file %q: %w", task.SpecPath, err)
	}
	spec := string(data)
	if task.Description == "" {
		return spec, nil
	}
	return task.Description + "\n\n" + spec, nil
}

func extractFeedback(out *graph.Output, runErr error) string {
	var last *graph.Output
	switch e := runErr.(type) {
	case *graph.ErrMaxCycles:
		last = e.LastOutput
	case *graph.ErrStuck:
		last = e.LastOutput
	}
	if last != nil {
		if txt := last.Envelope.OutputText(); txt != "" {
			return txt
		}
		if last.Raw != "" {
			return last.Raw
		}
		if last.Envelope.Feedback != "" {
			return last.Envelope.Feedback
		}
	}

	if out != nil {
		if txt := out.Envelope.OutputText(); txt != "" {
			return txt
		}
		if out.Raw != "" {
			return out.Raw
		}
		if out.Envelope.Feedback != "" {
			return out.Envelope.Feedback
		}
	}
	if runErr != nil {
		return runErr.Error()
	}
	return ""
}

func runWatchDaemon(ctx context.Context, cfg *config.Config, root string) error {
	colonyPath := filepath.Join(root, ".colony")
	if err := os.MkdirAll(colonyPath, 0755); err != nil {
		return fmt.Errorf("create .colony dir: %w", err)
	}

	pidPath := filepath.Join(colonyPath, "loop.pid")
	logPath := filepath.Join(colonyPath, "loop.log")

	if err := writePidfile(pidPath); err != nil {
		return err
	}
	defer func() { _ = os.Remove(pidPath) }()

	_ = removeSentinel(root)

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	multiOut := io.MultiWriter(os.Stderr, logFile)
	slog.SetDefault(slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo})))

	store, err := openLoopStore(root)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(multiOut, "%sloop: daemon started (pid=%d)%s\n", ansiBlue, os.Getpid(), ansiReset)
	slog.Info("daemon started", "pid", os.Getpid())

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintf(multiOut, "%sloop: daemon interrupted, exiting%s\n", ansiBlue, ansiReset)
			return nil
		default:
		}

		if sentinelExists(root) {
			fmt.Fprintf(multiOut, "%sloop: stop sentinel detected, exiting%s\n", ansiBlue, ansiReset)
			_ = removeSentinel(root)
			return nil
		}

		if err := maybeRotate(logPath, 10<<20, 3); err != nil {
			slog.Warn("log rotation failed", "error", err)
		}

		task, err := pickNextTask(ctx, store)
		if err != nil {
			slog.Error("pick task failed", "error", err)
		} else if task != nil {
			if err := processTask(ctx, cfg, root, store, task); err != nil {
				slog.Error("task failed", "id", task.ID, "error", err)
				_ = markTaskBlocked(store, task.ID)
			}
		}

		observeAllTasks(ctx, store)

		select {
		case <-ctx.Done():
			fmt.Fprintf(multiOut, "%sloop: daemon interrupted, exiting%s\n", ansiBlue, ansiReset)
			return nil
		case <-time.After(loopPollInterval):
		}
	}
}

func observeAllTasks(ctx context.Context, store *storage.SQLiteStore) {
	results, err := observe.ObserveTasksForPR(ctx, store)
	if err != nil {
		slog.Warn("observe failed", "error", err)
		return
	}
	for _, r := range results {
		if r.Changed {
			slog.Info("task observed", "id", r.TaskID, "status", r.NewStatus, "feedback", r.NewFeedback)
		}
	}
}
