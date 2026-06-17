package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jirateep/colony/pkg/config"
	"github.com/jirateep/colony/pkg/mission"
	"github.com/jirateep/colony/pkg/storage"
	"github.com/spf13/cobra"
)

var loopCmd = &cobra.Command{
	Use:   "loop",
	Short: "Autonomous steward: build→gate→fix loop",
	Long: `Runs the autonomous build-gate-fix loop on open tasks from the queue.
Picks the next actionable task, runs BuildGateFix on it, and records the outcome.

Flags:
  --once          Run a single pass (pick one task, process it, exit)
  --max-passes    Stop after N total passes (0 = unlimited)
  --max-cycles    Cap the inner fix loop per task (default 5)
  --escalate-to   Model to use for escalation role (default: off)
  --lang          Language for gates (default: go)
  --idle          Consecutive idle passes before stopping (default 10)`,
	RunE: runLoop,
}

var (
	loopOnce       bool
	loopMaxPasses  int
	loopMaxCycles  int
	loopEscalateTo string
	loopLang       string
	loopIdleLimit  int
)

func init() {
	loopCmd.Flags().BoolVar(&loopOnce, "once", false, "run a single pass and exit")
	loopCmd.Flags().IntVar(&loopMaxPasses, "max-passes", 0, "stop after N total passes (0 = unlimited)")
	loopCmd.Flags().IntVar(&loopMaxCycles, "max-cycles", 5, "cap the inner fix loop per task")
	loopCmd.Flags().StringVar(&loopEscalateTo, "escalate-to", "", "model for escalation role (default: off)")
	loopCmd.Flags().StringVar(&loopLang, "lang", "go", "language for gates")
	loopCmd.Flags().IntVar(&loopIdleLimit, "idle", 10, "consecutive idle passes before stopping")
}

func runLoop(cmd *cobra.Command, args []string) error {
	cfg, root, err := loadConfig()
	if err != nil {
		return err
	}

	// Clear stale sentinel on startup if present.
	_ = removeSentinel(root)

	store, err := openLoopStore(root)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	maxPasses := loopMaxPasses
	if maxPasses <= 0 {
		maxPasses = 0 // unlimited
	}
	idleLimit := loopIdleLimit
	if idleLimit <= 0 {
		idleLimit = 10
	}

	// Wrap the command context with signal handling.
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	totalPasses := 0
	consecutiveIdle := 0

	for {
		// Check global pass cap.
		if maxPasses > 0 && totalPasses >= maxPasses {
			fmt.Fprintf(os.Stderr, "loop: reached max-passes (%d), exiting\n", maxPasses)
			break
		}

		task, err := pickNextTask(ctx, store)
		if err != nil {
			return fmt.Errorf("pick task: %w", err)
		}
		if task == nil {
			consecutiveIdle++
			fmt.Fprintf(os.Stderr, "idle\n")
			if consecutiveIdle >= idleLimit {
				fmt.Fprintf(os.Stderr, "loop: idle limit reached (%d consecutive), exiting\n", idleLimit)
				break
			}
			if loopOnce {
				break
			}
			if err := sleepInterruptibly(ctx); err != nil {
				return nil
			}
			continue
		}

		consecutiveIdle = 0
		totalPasses++

		if err := processTask(ctx, cfg, root, store, task); err != nil {
			fmt.Fprintf(os.Stderr, "loop: task %q failed: %v\n", task.ID, err)
			_ = markTaskBlocked(store, task.ID)
		}

		if loopOnce {
			break
		}

		// Poll sentinel at pass boundary.
		if sentinelExists(root) {
			fmt.Fprintf(os.Stderr, "loop: stop sentinel detected, exiting\n")
			_ = removeSentinel(root)
			break
		}

		// Check context (signal) at pass boundary.
		if ctx.Err() != nil {
			fmt.Fprintf(os.Stderr, "loop: interrupted, exiting\n")
			break
		}
	}

	return nil
}

// sleepInterruptibly sleeps for 5s or until the context is cancelled.
func sleepInterruptibly(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return nil
	}
}

// sentinelPath returns the path to the loop stop sentinel file.
func sentinelPath(root string) string {
	return filepath.Join(root, ".colony", "loop.stop")
}

// sentinelExists returns true if the stop sentinel file exists.
func sentinelExists(root string) bool {
	_, err := os.Stat(sentinelPath(root))
	return err == nil
}

// removeSentinel deletes the stop sentinel file if it exists.
func removeSentinel(root string) error {
	err := os.Remove(sentinelPath(root))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// pickNextTask queries the tasks table for the next open or needs-fix task.
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

// processTask runs the BuildGateFix mission on a single task.
func processTask(ctx context.Context, cfg *config.Config, root string, store *storage.SQLiteStore, task *storage.Task) error {
	lang := task.Lang
	if lang == "" {
		lang = loopLang
	}

	// Increment cycle count before processing.
	if err := store.IncrementCycle(task.ID); err != nil {
		return fmt.Errorf("increment cycle: %w", err)
	}

	// Build the mission.
	opts := mission.BuildGateFixOpts{
		Name:      "loop-" + task.ID,
		Input:     task.Description,
		Lang:      lang,
		MaxCycles: loopMaxCycles,
	}
	if loopEscalateTo != "" {
		opts.EscalationRole = mission.RoleEscalation
	}

	m := mission.BuildGateFix(opts)

	// Build the graph with CommandRole resolution.
	g, err := mission.BuildGraph(m, mission.DefaultRegistry, func(role string) config.LLMConfig {
		return cfg.CommandRole("loop", role)
	})
	if err != nil {
		return fmt.Errorf("build graph: %w", err)
	}

	// Create a session and run the mission.
	sessID := fmt.Sprintf("loop-%s-%s", task.ID, time.Now().Format("20060102-150405"))
	if err := store.InsertSession(storage.Session{
		ID:          sessID,
		MissionName: m.Name,
		StartedAt:   time.Now(),
		Status:      "running",
	}); err != nil {
		return fmt.Errorf("insert session: %w", err)
	}

	runner := mission.NewRunner()
	out, runErr := runner.Run(ctx, m, g, sessID, store)

	// Record outcome.
	if runErr != nil {
		// Check if the context was cancelled (signal received).
		if ctx.Err() != nil {
			_ = store.UpdateSession(sessID, "interrupted", time.Now())
			_ = store.UpdateTaskState(task.ID, "needs-fix", "")
			return nil
		}

		_ = store.UpdateSession(sessID, "failed", time.Now())

		// Check if it's a max-cycles error (task hit the cap).
		if _, ok := runErr.(*mission.ErrMaxCycles); ok && loopEscalateTo != "" {
			// Escalate: re-dispatch on escalation role once.
			if err := escalateTask(ctx, cfg, root, store, task, lang, out); err != nil {
				return fmt.Errorf("escalation failed: %w", err)
			}
			return nil
		}

		// Max-cycles hit but no escalation configured → needs-fix with feedback.
		if _, ok := runErr.(*mission.ErrMaxCycles); ok {
			feedback := extractFeedback(out, runErr)
			_ = store.UpdateTaskState(task.ID, "needs-fix", feedback)
			return nil
		}

		return runErr
	}

	_ = store.UpdateSession(sessID, "completed", time.Now())
	// Gate green.
	_ = store.UpdateTaskState(task.ID, "done", "")
	return nil
}

// escalateTask re-dispatches a max-cycled task on the escalation role.
func escalateTask(ctx context.Context, cfg *config.Config, root string, store *storage.SQLiteStore, task *storage.Task, lang string, prevOut *mission.Output) error {
	opts := mission.BuildGateFixOpts{
		Name:      "escalation-" + task.ID,
		Input:     task.Description,
		Lang:      lang,
		MaxCycles: 1,
	}
	// Escalation uses a single-shot mission: builder → gate → output.
	// The escalation role handles the fix.
	opts.EscalationRole = mission.RoleEscalation

	m := mission.BuildGateFix(opts)
	g, err := mission.BuildGraph(m, mission.DefaultRegistry, func(role string) config.LLMConfig {
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

	runner := mission.NewRunner()
	_, runErr := runner.Run(ctx, m, g, sessID, store)
	_ = store.UpdateSession(sessID, "completed", time.Now())

	if runErr != nil {
		fmt.Fprintf(os.Stderr, "escalation failed for task %q, marking blocked\n", task.ID)
		_ = store.UpdateTaskState(task.ID, "blocked", extractFeedback(nil, runErr))
		return markTaskBlocked(store, task.ID)
	}

	// Escalation succeeded.
	_ = store.UpdateTaskState(task.ID, "done", "")
	return nil
}

// markTaskBlocked transitions a task to blocked state.
func markTaskBlocked(store *storage.SQLiteStore, taskID string) error {
	return store.UpdateTaskState(taskID, "blocked", "")
}

// openLoopStore opens the project's missions.db.
func openLoopStore(root string) (*storage.SQLiteStore, error) {
	dbPath := storage.DefaultDBPath()
	if root != "" {
		dbPath = filepath.Join(root, ".colony", "missions.db")
	}
	return storage.Open(dbPath)
}

// extractFeedback returns a string representation of mission output or error
// for use in last_feedback.
func extractFeedback(out *mission.Output, runErr error) string {
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
