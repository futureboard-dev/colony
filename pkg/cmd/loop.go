package cmd

import (
	"context"
	"errors"
	"fmt"
	"github.com/futureboard-dev/colony/pkg/handoff"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/futureboard-dev/colony/pkg/config"
	"github.com/futureboard-dev/colony/pkg/llm"
	"github.com/futureboard-dev/colony/pkg/mission/blueprint"
	"github.com/futureboard-dev/colony/pkg/mission/graph"
	"github.com/futureboard-dev/colony/pkg/mission/nodes"
	"github.com/futureboard-dev/colony/pkg/module"
	"github.com/futureboard-dev/colony/pkg/observe"
	"github.com/futureboard-dev/colony/pkg/output"
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
  --lang          Language for gates on tasks that have none recorded (each task's
                  own language, set at 'task add', is used when it has one)
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
	// loopLangExplicit records whether --lang was actually typed, so a task
	// with no recorded language is never gated with the flag's default.
	loopLangExplicit bool
	loopRetryLang    string
	loopIdleLimit    int
	loopRetryBlock   bool
	loopReview       bool
	loopParallel     int
)

func init() {
	loopCmd.Flags().BoolVar(&loopOnce, "once", false, "run a single pass and exit")
	loopCmd.Flags().BoolVar(&loopWatch, "watch", false, "run as a long-lived daemon (polls queue, observes CI/PR)")
	loopCmd.Flags().IntVar(&loopMaxPasses, "max-passes", 0, "stop after N total passes (0 = unlimited)")
	loopCmd.Flags().IntVar(&loopMaxCycles, "max-cycles", 3, "cap the inner fix loop per task")
	loopCmd.Flags().StringVar(&loopEscalateTo, "escalate-to", "", "model for escalation role (default: off)")
	loopCmd.Flags().StringVar(&loopLang, "lang", "", "language for gates on tasks with none recorded (legacy tasks are skipped unless this is passed)")
	loopCmd.Flags().IntVar(&loopIdleLimit, "idle", 10, "consecutive idle passes before stopping")
	loopCmd.Flags().BoolVar(&loopRetryBlock, "retry-blocked", false, "re-queue blocked tasks (needs-fix) to continue them in their existing worktree")
	loopCmd.Flags().BoolVar(&loopReview, "review", false, "run an LLM review gate before marking a task done (also auto-enabled when a 'review' role is configured)")
	loopCmd.Flags().IntVar(&loopParallel, "parallel", 1, "number of tasks to build concurrently (dependencies are still respected)")

	loopCmd.AddCommand(loopStatusCmd)
	loopCmd.AddCommand(loopScheduleCmd)
	loopCmd.AddCommand(loopClearCmd)
	loopCmd.AddCommand(loopRetryReviewCmd)
	loopCmd.AddCommand(loopRetryGateCmd)

	loopRetryGateCmd.Flags().StringVar(&loopRetryLang, "lang", "", "language for gates, required only when the task has none recorded")
	loopRetryReviewCmd.Flags().StringVar(&loopRetryLang, "lang", "", "language for gates, required only when the task has none recorded")

	loopRunCmd.Flags().StringVar(&loopRunLang, "lang", "", "language for gates, required only when the task has none recorded")
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

	// Resolved before the review runs so a task with no language fails fast
	// rather than after an approved review it would then be unable to gate.
	lang, err := resolveGateLang(store, task, loopRetryLang, cmd.Flags().Changed("lang"))
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "%sloop: retrying review for task %q on worktree %s%s\n", ansiBlue, taskID, workdir, ansiReset)

	baseBranch := task.BaseBranch
	if baseBranch == "" {
		baseBranch = module.DefaultBranch()
	}

	node := nodes.NewReviewNode("review", reviewCfg)
	out, runErr := node.Run(cmd.Context(), graph.Input{
		Params: map[string]any{"workdir": workdir, "base": baseBranch, "prior_feedback": reviewChecklist(task)},
	})

	if runErr != nil {
		_ = store.UpdateTaskState(task.ID, "blocked", runErr.Error())
		return fmt.Errorf("review failed: %w", runErr)
	}
	recordReview(store, task.ID, out)

	if out.Envelope.Decision != graph.APPROVED {
		feedback := out.Envelope.Feedback
		_ = store.UpdateTaskState(task.ID, "blocked", feedback)
		fmt.Fprintf(os.Stderr, "%sloop: review REJECTED — task blocked\n%s%s\n", ansiRed, feedback, ansiReset)
		return nil
	}

	fmt.Fprintf(os.Stderr, "%sloop: review APPROVED — integrating%s\n", ansiGreen, ansiReset)

	return finishOrLand(cmd.Context(), cfg, workdir, task.Branch, baseBranch, lang, task, store, os.Stderr)
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

	lang, err := resolveGateLang(store, task, loopRetryLang, cmd.Flags().Changed("lang"))
	if err != nil {
		return err
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
		Name:          "retry-gate-" + missionLabel(task),
		Input:         input,
		Lang:          lang,
		Base:          baseBranch,
		Workdir:       workdir,
		MaxCycles:     loopMaxCycles,
		SkipBuilder:   true,
		PriorFeedback: reviewChecklist(task),
	}
	if loopReview || cfg.HasCommandRole("loop", graph.RoleReview) {
		opts.ReviewRole = graph.RoleReview
	}

	m := blueprint.BuildGateFix(opts)

	reg := graph.NewRegistry()
	nodes.Register(reg)
	registerReviewRecorder(reg, store, task.ID)
	g, buildErr := graph.BuildGraph(m, reg, func(role string) config.LLMConfig {
		return cfg.CommandRole("loop", role)
	})
	if buildErr != nil {
		return fmt.Errorf("build graph: %w", buildErr)
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sessID := "retry-gate-" + strings.TrimPrefix(task.Branch, "agent/") + "-" + fmt.Sprint(time.Now().UnixNano())
	if err := store.InsertSession(storage.Session{
		ID:          sessID,
		MissionName: m.Name,
		TaskID:      task.ID,
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

	return finishOrLand(cmd.Context(), cfg, workdir, task.Branch, baseBranch, lang, task, store, os.Stderr)
}

var loopRunLang string

// loopRunCmd processes a single task by ID through the full build-gate-fix
// flow, bypassing queue selection. --lang defaults to the language recorded on
// the task at `task add` time; passing it overrides and persists a new one.
var loopRunCmd = &cobra.Command{
	Use:   "run <task-id>",
	Short: "Run a single task by ID",
	Long: `Processes one task by its ID through the full build-gate-fix flow,
regardless of its current queue position. Gates run under the language recorded
on the task; --lang is only needed for tasks that have none, and overrides (and
persists) the recorded language when passed.`,
	Args: cobra.ExactArgs(1),
	RunE: runLoopRun,
}

func runLoopRun(cmd *cobra.Command, args []string) error {
	taskID := args[0]

	if loopRunLang != "" {
		if _, err := module.CommandsFor(loopRunLang); err != nil {
			return err
		}
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

	if loopRunLang == "" {
		if task.Lang == "" {
			return fmt.Errorf("task %q has no recorded language — re-run with --lang <typescript|python|go|none>", task.ID)
		}
	} else if task.Lang != loopRunLang {
		if err := store.UpdateTaskLang(task.ID, loopRunLang); err != nil {
			return fmt.Errorf("persist lang: %w", err)
		}
		task.Lang = loopRunLang
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := processTask(ctx, cfg, root, store, task, os.Stderr); err != nil {
		_ = markTaskBlocked(store, task.ID, err.Error())
		return fmt.Errorf("task %q failed: %w", task.ID, err)
	}
	return nil
}

func runLoop(cmd *cobra.Command, args []string) error {
	loopLangExplicit = cmd.Flags().Changed("lang")

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
	reapStaleSessions(store)

	if err := resetRunningTasks(store); err != nil {
		return fmt.Errorf("reset running tasks: %w", err)
	}

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

	if resolveParallelCount(loopParallel) == 1 {
		return runLoopSequential(ctx, cfg, root, store, maxPasses, idleLimit)
	}
	work := func(ctx context.Context, t *storage.Task, logw io.Writer) error {
		return processTask(ctx, cfg, root, store, t, logw)
	}
	return runLoopParallel(ctx, root, store, maxPasses, idleLimit, resolveParallelCount(loopParallel), work)
}

// resolveParallelCount normalizes --parallel: anything less than 1 (including
// the zero value) means "no concurrency", i.e. 1.
func resolveParallelCount(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// resetRunningTasks moves every "running" task back to "needs-fix" at loop
// start, so a crash or kill -9 mid-build doesn't strand a task claimed but
// never finished. This assumes one loop process per project — no locking is
// added here for multiple concurrent loops.
func resetRunningTasks(store *storage.SQLiteStore) error {
	running, err := store.QueryTasks(storage.TaskFilter{States: []string{"running"}})
	if err != nil {
		return err
	}
	for _, t := range running {
		if err := store.UpdateTaskState(t.ID, "needs-fix", t.LastFeedback); err != nil {
			return err
		}
	}
	return nil
}

// runLoopSequential is the original single-task-at-a-time loop. --parallel
// defaults to 1 and always takes this path, so its behavior is unchanged.
func runLoopSequential(ctx context.Context, cfg *config.Config, root string, store *storage.SQLiteStore, maxPasses, idleLimit int) error {
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

		if err := processTask(ctx, cfg, root, store, task, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "%sloop: task %q failed: %v%s\n", ansiRed, task.ID, err, ansiReset)
			_ = markTaskBlocked(store, task.ID, err.Error())
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

// runLoopParallel runs up to `parallel` tasks concurrently via a semaphore and
// a WaitGroup. The main goroutine claims tasks (ClaimNextTask's transactional
// claim prevents double-claims across workers) while fewer than `parallel`
// workers are active, launching one goroutine per claimed task; idle,
// sentinel, and max-passes checks all count claimed tasks, the same way the
// sequential loop counts processed ones. work is the per-task unit of work
// (processTask in production; tests inject a fake to control timing without
// running a real build). On stop it waits for in-flight workers before
// returning.
func runLoopParallel(ctx context.Context, root string, store *storage.SQLiteStore, maxPasses, idleLimit, parallel int, work func(context.Context, *storage.Task, io.Writer) error) error {
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup

	totalPasses := 0
	consecutiveIdle := 0
	stopReason := ""

	for stopReason == "" {
		if maxPasses > 0 && totalPasses >= maxPasses {
			stopReason = fmt.Sprintf("reached max-passes (%d)", maxPasses)
			break
		}

		task, err := pickNextTask(ctx, store)
		if err != nil {
			wg.Wait()
			return fmt.Errorf("pick task: %w", err)
		}
		if task == nil {
			// A running worker can finish and unlock dependent parts, so
			// waiting on it is not idle. Only count passes with nothing running.
			if len(sem) == 0 {
				consecutiveIdle++
				fmt.Fprintf(os.Stderr, "%sidle: no tasks ready (%d/%d)%s\n", ansiBlue, consecutiveIdle, idleLimit, ansiReset)
				if consecutiveIdle >= idleLimit {
					stopReason = fmt.Sprintf("idle limit reached (%d consecutive)", idleLimit)
					break
				}
			}
			if loopOnce {
				break
			}
			if err := sleepInterruptibly(ctx); err != nil {
				break
			}
			if sentinelExists(root) {
				stopReason = "stop sentinel detected"
				_ = removeSentinel(root)
				break
			}
			continue
		}

		consecutiveIdle = 0
		totalPasses++

		sem <- struct{}{}
		wg.Add(1)
		go func(t *storage.Task) {
			defer wg.Done()
			defer func() { <-sem }()

			prefixed := output.NewPrefixWriter(os.Stderr, workerLabel(t))
			defer func() { _ = prefixed.Close() }()

			if err := work(ctx, t, prefixed); err != nil {
				fmt.Fprintf(prefixed, "%sloop: task %q failed: %v%s\n", ansiRed, t.ID, err, ansiReset)
				_ = markTaskBlocked(store, t.ID, err.Error())
			}
		}(task)

		if loopOnce {
			break
		}
		if sentinelExists(root) {
			stopReason = "stop sentinel detected"
			_ = removeSentinel(root)
			break
		}
		if ctx.Err() != nil {
			stopReason = "interrupted"
			break
		}
	}

	if stopReason != "" {
		fmt.Fprintf(os.Stderr, "%sloop: %s, exiting%s\n", ansiBlue, stopReason, ansiReset)
	}
	wg.Wait()
	return nil
}

// workerLabel picks the prefix a parallel worker's output is tagged with: the
// part ID (its spec file's basename) for group tasks, or the task ID otherwise.
func workerLabel(t *storage.Task) string {
	if t.GroupID != "" && t.SpecPath != "" {
		base := filepath.Base(t.SpecPath)
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	return t.ID
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

// pickNextTask claims the next actionable task, atomically moving it to
// "running" so parallel workers and the --watch daemon never pick the same
// task twice. ctx is unused — ClaimNextTask is a single local DB transaction —
// but kept so call sites don't change.
func pickNextTask(_ context.Context, store *storage.SQLiteStore) (*storage.Task, error) {
	return store.ClaimNextTask()
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

// resolveGateLang returns the language a task's quality gates should run under.
// Tasks created before `task add --lang` became required carry no language;
// gating those with the flag default silently runs the wrong toolchain (Go
// commands against a TypeScript repo), so the caller has to name the language
// explicitly. A language supplied that way is persisted, so the task is only
// missing it once.
func resolveGateLang(store *storage.SQLiteStore, task *storage.Task, flagLang string, flagSet bool) (string, error) {
	if task.Lang != "" {
		return task.Lang, nil
	}
	if !flagSet || flagLang == "" {
		return "", fmt.Errorf("task %q has no recorded language — re-run with --lang <typescript|python|go|none> to gate it", task.ID)
	}
	if _, err := module.CommandsFor(flagLang); err != nil {
		return "", err
	}
	if err := store.UpdateTaskLang(task.ID, flagLang); err != nil {
		return "", fmt.Errorf("persist lang: %w", err)
	}
	task.Lang = flagLang
	return flagLang, nil
}

func processTask(ctx context.Context, cfg *config.Config, root string, store *storage.SQLiteStore, task *storage.Task, logw io.Writer) error {
	lang, err := resolveGateLang(store, task, loopLang, loopLangExplicit)
	if err != nil {
		return err
	}

	input, err := taskInput(task)
	if err != nil {
		return err
	}
	input = withPriorFeedback(input, task)

	projectName := module.ProjectName(root)
	workdir, branch, err := resolveWorktreeSafe(root, projectName, task)
	if err != nil {
		return fmt.Errorf("setup worktree: %w", err)
	}
	module.InstallDeps(lang, workdir, logw)
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
	if task.GroupID != "" {
		if err := excludeFromGit(workdir, "/"+partNotesFile); err != nil {
			return fmt.Errorf("exclude %s: %w", partNotesFile, err)
		}
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
		// Code that only failed a gate needs re-gating, not rebuilding: the
		// fixer still runs if the gate is red, and a green gate goes to review.
		SkipBuilder:   resumeAtGate(task, branch),
		PriorFeedback: reviewChecklist(task),
	}
	if opts.SkipBuilder {
		fmt.Fprintf(logw, "%sloop: task %q last failed a gate — resuming at the gate, not the builder%s\n", ansiBlue, task.ID, ansiReset)
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
	registerReviewRecorder(reg, store, task.ID)
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
		TaskID:      task.ID,
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
			fmt.Fprintf(logw, "%sloop: task %q stuck (identical failure repeated), blocked%s\n", ansiRed, task.ID, ansiReset)
			if nodes.IsGateFailure(feedback) {
				fmt.Fprintf(logw, "%sloop: the fixer could not change this gate failure — check the gate's command and environment (shown under \"command:\" in the feedback)%s\n", ansiYellow, ansiReset)
			}
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
			fmt.Fprintf(logw, "%sloop: task %q hit max-cycles, blocked (use --retry-blocked to resume)%s\n", ansiRed, task.ID, ansiReset)
			return nil
		}

		return runErr
	}

	_ = store.UpdateSession(sessID, "completed", time.Now())

	return finishOrLand(ctx, cfg, workdir, branch, baseBranch, lang, task, store, logw)
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

// integrateTask merges the task's target into workdir and re-gates. Non-group
// tasks fetch and merge origin/<baseBranch>, unchanged. Group tasks merge the
// local epic branch ref directly — worktrees share refs, so no fetch is
// needed — and integrateTask returns the epic branch's SHA as observed at
// merge time, which landOnEpic uses as the "old" value in its update-ref CAS.
func integrateTask(ctx context.Context, cfg *config.Config, workdir, branch, baseBranch, lang string, task *storage.Task, store *storage.SQLiteStore, out io.Writer) (string, error) {
	isGroup := task.GroupID != ""

	var epicSHA string
	if isGroup {
		fmt.Fprintf(out, "%sloop: integrating %s with local %s (group task)%s\n", ansiBlue, branch, baseBranch, ansiReset)

		shaOut, shaErr := exec.Command("git", "-C", workdir, "rev-parse", baseBranch).Output()
		if shaErr != nil {
			_ = store.UpdateTaskState(task.ID, "blocked", fmt.Sprintf("epic branch %s not found: %v", baseBranch, shaErr))
			return "", fmt.Errorf("resolve epic branch %s: %w", baseBranch, shaErr)
		}
		epicSHA = strings.TrimSpace(string(shaOut))

		if err := mergeAndResolve(ctx, cfg, workdir, baseBranch, baseBranch, lang, task, store, out); err != nil {
			return "", err
		}
	} else {
		fmt.Fprintf(out, "%sloop: integrating %s with origin/%s%s\n", ansiBlue, branch, baseBranch, ansiReset)

		fetch := exec.Command("git", "fetch", "origin", baseBranch)
		fetch.Dir = workdir
		fetch.Stdout = out
		fetch.Stderr = out
		if err := fetch.Run(); err != nil {
			_ = store.UpdateTaskState(task.ID, "blocked", fmt.Sprintf("git fetch origin/%s failed: %v", baseBranch, err))
			return "", fmt.Errorf("git fetch: %w", err)
		}

		if err := mergeAndResolve(ctx, cfg, workdir, "FETCH_HEAD", "origin/"+baseBranch, lang, task, store, out); err != nil {
			return "", err
		}
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
		fixPrompt, ferr := prompt.Fix("integration gate", capGateOutput(gateOutput), module.CheckList(lang))
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
		feedback := fmt.Sprintf("integration gate failed after merge with %s:\n%s", baseBranch, gateOutput)
		_ = store.UpdateTaskState(task.ID, "blocked", feedback)
		fmt.Fprintf(out, "%sloop: task %q blocked — integration gate failed after merge%s\n", ansiRed, task.ID, ansiReset)
		return "", fmt.Errorf("integration gate after merge: %w\n%s", gateErr, gateOutput)
	}

	fmt.Fprintf(out, "%sloop: integration gate passed%s\n", ansiBlue, ansiReset)
	return epicSHA, nil
}

// mergeAndResolve merges ref (shown to people as name) into workdir. The
// agent's work is uncommitted, and git refuses a merge that would overwrite
// it, so it is committed first; both landing paths squash afterwards, so this
// commit never reaches the final history. Conflicts go to the fixer agent;
// only conflicts it leaves unresolved block the task.
func mergeAndResolve(ctx context.Context, cfg *config.Config, workdir, ref, name, lang string, task *storage.Task, store *storage.SQLiteStore, out io.Writer) error {
	block := func(feedback string) error {
		_ = store.UpdateTaskState(task.ID, "blocked", feedback)
		fmt.Fprintf(out, "%sloop: task %q blocked — %s%s\n", ansiRed, task.ID, feedback, ansiReset)
		return errors.New(feedback)
	}

	if err := commitWork(workdir, "loop: "+missionLabel(task)+" (before merging "+name+")", out); err != nil {
		return block(fmt.Sprintf("commit work before merging %s: %v", name, err))
	}

	merge := exec.Command("git", "merge", "--no-edit", ref)
	merge.Dir = workdir
	merge.Stdout = out
	merge.Stderr = out
	if merge.Run() == nil {
		return nil
	}
	abort := func() {
		cmd := exec.Command("git", "merge", "--abort")
		cmd.Dir = workdir
		_ = cmd.Run()
	}
	conflictFiles := getConflictFiles(workdir)
	if len(conflictFiles) == 0 {
		abort()
		return block(fmt.Sprintf("git merge %s failed (see log)", name))
	}

	fmt.Fprintf(out, "%sloop: merge conflict with %s in %s — asking fixer agent to resolve%s\n", ansiYellow, name, strings.Join(conflictFiles, ", "), ansiReset)
	if cfg != nil {
		feedback := fmt.Sprintf("Merging %s into this branch left conflict markers (<<<<<<<, =======, >>>>>>>) in: %s.\n"+
			"Resolve every conflict so the result keeps both this branch's changes and the ones already on %s. "+
			"Remove all conflict markers.", name, strings.Join(conflictFiles, ", "), name)
		fixPrompt, err := prompt.Fix("merge with "+name, feedback, module.CheckList(lang))
		if err == nil {
			if aerr := llm.New(cfg.CommandRole("loop", graph.RoleFixer)).RunAgent(ctx, workdir, fixPrompt, out); aerr != nil {
				fmt.Fprintf(out, "%sloop: fixer agent error: %v%s\n", ansiYellow, aerr, ansiReset)
			}
		}
	}

	if unresolved := filesWithConflictMarkers(workdir, conflictFiles); len(unresolved) > 0 {
		abort()
		return block(fmt.Sprintf("merge conflict with %s. Conflicting files: %s", name, strings.Join(unresolved, ", ")))
	}
	if err := commitWork(workdir, "", out); err != nil {
		abort()
		return block(fmt.Sprintf("commit resolved merge with %s: %v", name, err))
	}
	return nil
}

// commitWork stages everything except colony's SPEC.md and commits it with
// message, or with git's prepared message when message is "" (a merge being
// concluded). A clean tree outside a merge commits nothing.
func commitWork(workdir, message string, out io.Writer) error {
	add := exec.Command("git", "add", "-A", "--", ".", ":(exclude)SPEC.md")
	add.Dir = workdir
	if err := add.Run(); err != nil {
		return fmt.Errorf("git add: %w", err)
	}
	args := []string{"commit", "--no-edit", "--quiet"}
	if message != "" {
		if diff := exec.Command("git", "diff", "--cached", "--quiet"); runInDir(diff, workdir) == nil {
			return nil
		}
		args = append(args, "-m", message)
	}
	commit := exec.Command("git", args...)
	commit.Dir = workdir
	commit.Stdout = out
	commit.Stderr = out
	if err := commit.Run(); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	return nil
}

// filesWithConflictMarkers returns the files in names that still hold a
// conflict marker line, or can no longer be read.
func filesWithConflictMarkers(workdir string, names []string) []string {
	var unresolved []string
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(workdir, name))
		if os.IsNotExist(err) {
			continue // resolved by deleting the file
		}
		if err != nil {
			unresolved = append(unresolved, name)
			continue
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			if strings.HasPrefix(line, "<<<<<<< ") || strings.HasPrefix(line, ">>>>>>> ") {
				unresolved = append(unresolved, name)
				break
			}
		}
	}
	return unresolved
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

func finishTask(workdir, branch, baseBranch, label, ghUser string, out io.Writer) (string, error) {
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

	return pushBranchAndOpenPR(workdir, branch, baseBranch, ghUser, out)
}

// pushBranchAndOpenPR pushes branch to origin and opens a PR against
// baseBranch, returning the PR URL. Shared by finishTask (one PR per
// standalone task) and group completion (one PR for the epic branch, opened
// once every task in the group is done). A non-empty ghUser runs push and
// gh as that account.
func pushBranchAndOpenPR(workdir, branch, baseBranch, ghUser string, out io.Writer) (string, error) {
	env, err := ghEnv(ghUser)
	if err != nil {
		return "", err
	}
	push := exec.Command("git", "push", "-u", "origin", branch)
	push.Dir = workdir
	push.Env = env
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
	gh.Env = env
	var prURL strings.Builder
	gh.Stdout = io.MultiWriter(out, &prURL)
	gh.Stderr = out
	if err := gh.Run(); err != nil {
		return "", fmt.Errorf("gh pr create: %w", err)
	}
	return strings.TrimSpace(prURL.String()), nil
}

// ghEnv returns the environment for push and gh commands. For a named user
// it injects that account's token as GH_TOKEN rather than running
// `gh auth switch`, which would flip the machine-wide active account under
// any other loop running against a different repo. Nil means inherit.
func ghEnv(user string) ([]string, error) {
	if user == "" {
		return nil, nil
	}
	token, err := exec.Command("gh", "auth", "token", "--user", user).Output()
	if err != nil {
		return nil, fmt.Errorf("gh token for user %q (run: gh auth login, then check gh auth status): %w", user, err)
	}
	return append(os.Environ(), "GH_TOKEN="+strings.TrimSpace(string(token))), nil
}

func runInDir(cmd *exec.Cmd, dir string) error {
	cmd.Dir = dir
	return cmd.Run()
}

// landOnEpic squashes a group task's part commits onto its epic branch via an
// atomic compare-and-swap update-ref, so parallel sibling parts landing at
// the same time can't overwrite each other. epicSHA is the epic branch's tip
// as observed by integrateTask at merge time. On a stale-SHA CAS failure it
// re-runs integration (merge + re-gate against the epic's new tip) and
// retries, up to 3 total attempts, then marks the task blocked. landOnEpic
// does not push — the epic branch stays local until the whole group is done.
func landOnEpic(ctx context.Context, cfg *config.Config, workdir, branch, lang string, task *storage.Task, epicSHA string, store *storage.SQLiteStore, out io.Writer) error {
	epicBranch := task.BaseBranch
	const maxAttempts = 3

	if err := collectPartNotes(workdir, task.SpecPath); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(workdir, "SPEC.md"))

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		mbOut, err := exec.Command("git", "-C", workdir, "merge-base", "HEAD", epicSHA).Output()
		if err != nil {
			return fmt.Errorf("merge-base against epic %s: %w", epicBranch, err)
		}
		reset := exec.Command("git", "reset", "--soft", strings.TrimSpace(string(mbOut)))
		reset.Dir = workdir
		if err := reset.Run(); err != nil {
			return fmt.Errorf("git reset --soft: %w", err)
		}

		add := exec.Command("git", "add", "-A")
		add.Dir = workdir
		if err := add.Run(); err != nil {
			return fmt.Errorf("git add: %w", err)
		}
		if diff := exec.Command("git", "diff", "--cached", "--quiet"); runInDir(diff, workdir) == nil {
			return fmt.Errorf("no changes to commit — agent produced an empty diff")
		}

		commit := exec.Command("git", "commit", "-m", "loop: "+missionLabel(task))
		commit.Dir = workdir
		commit.Stdout = out
		commit.Stderr = out
		if err := commit.Run(); err != nil {
			return fmt.Errorf("git commit: %w", err)
		}

		partHeadOut, err := exec.Command("git", "-C", workdir, "rev-parse", "HEAD").Output()
		if err != nil {
			return fmt.Errorf("rev-parse HEAD: %w", err)
		}
		partHead := strings.TrimSpace(string(partHeadOut))

		updateRef := exec.Command("git", "-C", workdir, "update-ref", "refs/heads/"+epicBranch, partHead, epicSHA)
		if err := updateRef.Run(); err == nil {
			_ = store.UpdateTaskState(task.ID, "done", epicBranch)
			return nil
		}

		if attempt == maxAttempts {
			break
		}
		fmt.Fprintf(out, "%sloop: epic branch %s moved during landing — re-integrating and retrying (attempt %d/%d)%s\n",
			ansiYellow, epicBranch, attempt+1, maxAttempts, ansiReset)
		newEpicSHA, err := integrateTask(ctx, cfg, workdir, branch, epicBranch, lang, task, store, out)
		if err != nil {
			return err // integrateTask already marked the task blocked
		}
		epicSHA = newEpicSHA
	}

	feedback := fmt.Sprintf("failed to land on epic branch %s after %d attempts (stale ref)", epicBranch, maxAttempts)
	_ = store.UpdateTaskState(task.ID, "blocked", feedback)
	return fmt.Errorf("land on epic %s: exceeded retry attempts", epicBranch)
}

// maybeCompleteGroup pushes the epic branch and opens one PR for the whole
// group once every task in it is done. workdir can be any task's worktree —
// worktrees of the same project share refs, so pushing the epic branch by
// name doesn't require it to be checked out anywhere. A push/PR failure marks
// the group blocked; this is the only case that does.
func maybeCompleteGroup(groupID, workdir, ghUser string, store *storage.SQLiteStore, out io.Writer) error {
	tasks, err := store.QueryTasks(storage.TaskFilter{GroupID: groupID})
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if t.State != "done" {
			return nil
		}
	}

	groups, err := store.QueryGroups(storage.GroupFilter{ID: groupID})
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		return fmt.Errorf("group %q not found", groupID)
	}
	g := groups[0]
	if g.State != "open" {
		return nil
	}

	prURL, err := pushBranchAndOpenPR(workdir, g.EpicBranch, g.BaseBranch, ghUser, out)
	if err != nil {
		_ = store.UpdateGroup(g.ID, "blocked", "")
		return fmt.Errorf("push/PR for group %s: %w", g.Name, err)
	}
	fmt.Fprintf(out, "%sloop: group %q done → %s%s\n", ansiGreen, g.Name, prURL, ansiReset)
	return store.UpdateGroup(g.ID, "done", prURL)
}

// finishOrLand runs integration and then, depending on whether task belongs
// to a group, either lands it on the group's epic branch (and completes the
// group if it was the last part) or pushes and opens a standalone PR exactly
// as before. Errors from integration/landing are already recorded on the
// task; the caller only needs to know the loop pass is over.
func finishOrLand(ctx context.Context, cfg *config.Config, workdir, branch, baseBranch, lang string, task *storage.Task, store *storage.SQLiteStore, out io.Writer) error {
	epicSHA, err := integrateTask(ctx, cfg, workdir, branch, baseBranch, lang, task, store, out)
	if err != nil {
		return nil
	}

	if task.GroupID != "" {
		if err := landOnEpic(ctx, cfg, workdir, branch, lang, task, epicSHA, store, out); err != nil {
			fmt.Fprintf(out, "%sloop: task %q failed to land on %s: %v%s\n", ansiRed, task.ID, baseBranch, err, ansiReset)
			return nil
		}
		fmt.Fprintf(out, "%sloop: task %q landed on %s%s\n", ansiGreen, task.ID, baseBranch, ansiReset)
		if cerr := maybeCompleteGroup(task.GroupID, workdir, cfg.GHUser, store, out); cerr != nil {
			fmt.Fprintf(out, "%sloop: group completion failed: %v%s\n", ansiYellow, cerr, ansiReset)
		}
		return nil
	}

	prURL, finishErr := finishTask(workdir, branch, baseBranch, missionLabel(task), cfg.GHUser, out)
	if finishErr != nil {
		fmt.Fprintf(out, "%sloop: task %q done (gate passed) — warning: %v%s\n", ansiYellow, task.ID, finishErr, ansiReset)
		_ = store.UpdateTaskState(task.ID, "done", "warning: "+finishErr.Error())
		return nil
	}
	fmt.Fprintf(out, "%sloop: task %q done → %s%s\n", ansiGreen, task.ID, prURL, ansiReset)
	_ = store.UpdateTaskState(task.ID, "done", prURL)
	return nil
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
		TaskID:      task.ID,
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
		return markTaskBlocked(store, task.ID, extractFeedback(nil, runErr))
	}

	_ = store.UpdateTaskState(task.ID, "done", "")
	return nil
}

// markTaskBlocked blocks a task and records why, so a later --retry-blocked
// run can hand the reason back to the agent.
func markTaskBlocked(store *storage.SQLiteStore, taskID, reason string) error {
	return store.UpdateTaskState(taskID, "blocked", reason)
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

// reapStaleSessions clears sessions left "running" by a loop that was killed
// before it could record how it ended. Failure only leaves stale rows, so it
// is reported and the loop carries on.
func reapStaleSessions(store *storage.SQLiteStore) {
	n, err := store.ReapStaleSessions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%sloop: reap stale sessions: %v%s\n", ansiRed, err, ansiReset)
		return
	}
	if n > 0 {
		fmt.Fprintf(os.Stderr, "%sloop: marked %d stale session(s) interrupted%s\n", ansiBlue, n, ansiReset)
	}
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

// resolveWorktreeMu serializes worktree creation. Concurrent `git worktree
// add` calls from parallel workers race on `.git`'s lock files, so parallel
// loop runs must not call resolveWorktree at the same time.
var resolveWorktreeMu sync.Mutex

// resolveWorktreeSafe is resolveWorktree guarded by a mutex, for use by
// processTask, which parallel workers may call concurrently.
func resolveWorktreeSafe(root, projectName string, task *storage.Task) (workdir, branch string, err error) {
	resolveWorktreeMu.Lock()
	defer resolveWorktreeMu.Unlock()
	return resolveWorktree(root, projectName, task)
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
	if strings.HasPrefix(spec, "---\n") {
		if part, err := parsePartContent(task.SpecPath, spec); err == nil && part.Source != "" {
			spec, err = partWithSource(task.SpecPath, part)
			if err != nil {
				return "", err
			}
		}
	}
	if task.Description == "" {
		return spec, nil
	}
	return task.Description + "\n\n" + spec, nil
}

// partWithSource gives a split part's agent its brief, the group's plan, the
// handoff notes of the parts it depends on, and the full source spec the
// brief points into (briefs reference the spec instead of restating it).
func partWithSource(partPath string, part groupPart) (string, error) {
	sourcePath := part.Source
	if !filepath.IsAbs(sourcePath) {
		sourcePath = filepath.Join(filepath.Dir(partPath), sourcePath)
	}
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return "", fmt.Errorf("read source spec for part %q: %w", part.ID, err)
	}
	featureDir := filepath.Dir(filepath.Dir(partPath))

	var b strings.Builder
	fmt.Fprintf(&b, "# Your part: %s\n\n%s\n", part.ID, part.Body)
	if plan, err := os.ReadFile(filepath.Join(featureDir, "PLAN.md")); err == nil {
		fmt.Fprintf(&b, "\n# Plan for all parts\n\n%s\n", plan)
	}
	for _, dep := range part.DependsOn {
		if notes, err := os.ReadFile(partNotesPath(partPath, dep)); err == nil {
			fmt.Fprintf(&b, "\n# Notes from %s (already built, on your base branch)\n\n%s\n", dep, notes)
		}
	}
	fmt.Fprintf(&b, "\n# Handoff notes\n\nBefore you finish, write %s at the repository root for the parts that build on yours: "+
		"what you built, the exact names of the types, interfaces and functions they will use, and anything that differs from the plan. "+
		"It is not committed.\n", partNotesFile)
	fmt.Fprintf(&b, "\n# Source spec\n\nBuild only your part above. The full spec is here for reference.\n\n%s", source)
	return b.String(), nil
}

// partNotesFile is where a part's agent writes its handoff notes in its
// worktree. It is git-excluded and moved to the spec folder when the part lands.
const partNotesFile = nodes.PartNotesFile

// partNotesPath is where a landed part's notes live: <spec-dir>/notes/<id>.md,
// next to the parts/ directory holding partPath.
func partNotesPath(partPath, partID string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(partPath)), "notes", partID+".md")
}

// collectPartNotes moves the agent's handoff notes out of the worktree into
// the spec folder, so dependents can read them and they never reach a commit.
// A part that wrote no notes is not an error.
func collectPartNotes(workdir, specPath string) error {
	src := filepath.Join(workdir, partNotesFile)
	notes, err := os.ReadFile(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", partNotesFile, err)
	}
	part, err := parsePartFrontmatter(specPath)
	if err != nil {
		return fmt.Errorf("read part for notes: %w", err)
	}
	dst := partNotesPath(specPath, part.ID)
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("create notes dir: %w", err)
	}
	if err := os.WriteFile(dst, notes, 0644); err != nil {
		return fmt.Errorf("write notes: %w", err)
	}
	return os.Remove(src)
}

// excludeFromGit adds pattern to the repository's .git/info/exclude (shared
// by every worktree and never committed) unless it is already listed.
func excludeFromGit(workdir, pattern string) error {
	out, err := exec.Command("git", "-C", workdir, "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return fmt.Errorf("find git dir: %w", err)
	}
	gitDir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(workdir, gitDir)
	}
	path := filepath.Join(gitDir, "info", "exclude")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for line := range strings.SplitSeq(string(existing), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		pattern = "\n" + pattern
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(pattern + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// withPriorFeedback appends the reason a needs-fix task was last rejected, so
// the retry knows what to address instead of repeating the same attempt.
func withPriorFeedback(input string, task *storage.Task) string {
	if task.State != "needs-fix" || task.LastFeedback == "" {
		return input
	}
	input += "\n\n## Previous attempt was rejected\n\nAddress this feedback from the last attempt:\n\n" + handoff.Bound(task.LastFeedback, handoff.FeedbackLimit)
	// A gate failure after a review rejection must not hide the review's
	// findings: they are still open and the next review re-checks them.
	if task.ReviewFeedback != "" && task.ReviewFeedback != task.LastFeedback {
		input += "\n\nThese review findings are also still open:\n\n" + handoff.Bound(task.ReviewFeedback, handoff.FeedbackLimit)
	}
	return input
}

// reviewChecklist is the open review rejection the next reviewer must
// re-verify. Tasks blocked before review_feedback existed kept it only in
// LastFeedback, so fall back to that unless it is a gate failure, which says
// nothing about the review.
func reviewChecklist(task *storage.Task) string {
	if task.ReviewFeedback != "" {
		return task.ReviewFeedback
	}
	if nodes.IsGateFailure(task.LastFeedback) {
		return ""
	}
	return task.LastFeedback
}

// resumeAtGate reports whether a retried task can skip the builder: it already
// has its worktree and was last stopped by a gate, not by review or an error.
func resumeAtGate(task *storage.Task, branch string) bool {
	return task.State == "needs-fix" && task.Branch != "" && task.Branch == branch &&
		nodes.IsGateFailure(task.LastFeedback)
}

// recordReview keeps the latest review outcome apart from LastFeedback: a
// rejection's findings stay open until a review approves, whatever fails later.
func recordReview(store *storage.SQLiteStore, taskID string, out graph.Output) {
	switch out.Envelope.Decision {
	case graph.REJECTED:
		_ = store.UpdateTaskReviewFeedback(taskID, out.Envelope.Feedback)
	case graph.APPROVED:
		_ = store.UpdateTaskReviewFeedback(taskID, "")
	}
}

// registerReviewRecorder wraps the review role so every review inside a
// mission records its outcome as it happens. The mission's final output only
// carries the last node's result, which is often a later gate failure.
func registerReviewRecorder(reg *graph.Registry, store *storage.SQLiteStore, taskID string) {
	reg.Register(graph.RoleReview, func(agentID string, cfg config.LLMConfig) (graph.Node, error) {
		node, err := nodes.ReviewNodeFactory(agentID, cfg)
		if err != nil {
			return nil, err
		}
		return reviewRecorder{node: node, store: store, taskID: taskID}, nil
	})
}

type reviewRecorder struct {
	node   graph.Node
	store  *storage.SQLiteStore
	taskID string
}

func (r reviewRecorder) Run(ctx context.Context, in graph.Input) (graph.Output, error) {
	out, err := r.node.Run(ctx, in)
	if err == nil {
		recordReview(r.store, r.taskID, out)
	}
	return out, err
}

func extractFeedback(out *graph.Output, runErr error) string {
	var last *graph.Output
	switch e := runErr.(type) {
	case *graph.ErrMaxCycles:
		last = e.LastOutput
	case *graph.ErrStuck:
		last = e.LastOutput
	}
	if last == nil {
		last = out
	}
	if last != nil && last.Envelope.Feedback != "" {
		return handoff.Bound(last.Envelope.Feedback, handoff.FeedbackLimit)
	}
	// Execution failure takes precedence over a partial response.
	if runErr != nil {
		return handoff.Bound(runErr.Error(), handoff.FeedbackLimit)
	}
	if last != nil {
		return handoff.Bound(last.Envelope.OutputText(), handoff.FeedbackLimit)
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
	reapStaleSessions(store)

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
			if err := processTask(ctx, cfg, root, store, task, os.Stderr); err != nil {
				slog.Error("task failed", "id", task.ID, "error", err)
				_ = markTaskBlocked(store, task.ID, err.Error())
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
