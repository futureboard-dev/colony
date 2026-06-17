package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jirateep/colony/pkg/config"
	"github.com/jirateep/colony/pkg/mission"
	"github.com/jirateep/colony/pkg/storage"
	"github.com/spf13/cobra"
)

// TaskRow represents a task from the sqlite queue that the loop operates on.
// In v1 this is a minimal model; storage extensions are in pkg/storage/task.go.
type TaskRow struct {
	ID          string
	Description string
	State       string // "open", "needs-fix", "done", "blocked"
	SpecPath    string
	BaseBranch  string
	CycleCount  int
	Lang        string
}

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

	totalPasses := 0
	consecutiveIdle := 0

	for {
		// Check global pass cap.
		if maxPasses > 0 && totalPasses >= maxPasses {
			fmt.Fprintf(os.Stderr, "loop: reached max-passes (%d), exiting\n", maxPasses)
			break
		}

		task, err := pickNextTask(cmd.Context(), store)
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
			time.Sleep(5 * time.Second)
			continue
		}

		consecutiveIdle = 0
		totalPasses++

		if err := processTask(cmd.Context(), cfg, root, store, task); err != nil {
			fmt.Fprintf(os.Stderr, "loop: task %q failed: %v\n", task.ID, err)
			_ = markTaskBlocked(store, task.ID)
		}

		if loopOnce {
			break
		}
	}

	return nil
}

// pickNextTask queries the store for the next open or needs-fix task.
func pickNextTask(ctx context.Context, store *storage.SQLiteStore) (*TaskRow, error) {
	// v1: query the runs table as a proxy for the task queue.
	// Look for any open/unprocessed tasks. For now, we check if there's
	// at least one run that isn't "complete" or "blocked".
	runs, err := store.QueryRuns(storage.RunFilter{})
	if err != nil {
		return nil, err
	}

	for _, r := range runs {
		if r.Status == "running" || r.Status == "" {
			return &TaskRow{
				ID:          r.ID,
				Description: r.Branch,
				State:       r.Status,
				Lang:        r.Language,
			}, nil
		}
	}
	return nil, nil
}

// processTask runs the BuildGateFix mission on a single task.
func processTask(ctx context.Context, cfg *config.Config, root string, store *storage.SQLiteStore, task *TaskRow) error {
	lang := task.Lang
	if lang == "" {
		lang = loopLang
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
		_ = store.UpdateSession(sessID, "failed", time.Now())

		// Check if it's a max-cycles error (task hit the cap).
		if _, ok := runErr.(*mission.ErrMaxCycles); ok && loopEscalateTo != "" {
			// Escalate: re-dispatch on escalation role once.
			if err := escalateTask(ctx, cfg, root, store, task, lang, out); err != nil {
				return fmt.Errorf("escalation failed: %w", err)
			}
			return nil
		}
		return runErr
	}

	_ = store.UpdateSession(sessID, "completed", time.Now())
	return nil
}

// escalateTask re-dispatches a max-cycled task on the escalation role.
func escalateTask(ctx context.Context, cfg *config.Config, root string, store *storage.SQLiteStore, task *TaskRow, lang string, prevOut *mission.Output) error {
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
		return markTaskBlocked(store, task.ID)
	}
	return nil
}

// markTaskBlocked transitions a task to blocked state.
func markTaskBlocked(store *storage.SQLiteStore, taskID string) error {
	now := time.Now()
	return store.UpdateRun(storage.Run{ID: taskID, Status: "blocked", FinishedAt: &now})
}

// openLoopStore opens the project's missions.db.
func openLoopStore(root string) (*storage.SQLiteStore, error) {
	dbPath := storage.DefaultDBPath()
	if root != "" {
		dbPath = filepath.Join(root, ".colony", "missions.db")
	}
	return storage.Open(dbPath)
}
