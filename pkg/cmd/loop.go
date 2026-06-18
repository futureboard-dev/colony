package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jirateep/colony/pkg/storage"
	"github.com/spf13/cobra"
)

var loopCmd = &cobra.Command{
	Use:   "loop",
	Short: "Task processing loop — one-shot, watch, status, stop",
}

var loopWatch bool

func init() {
	loopCmd.AddCommand(loopStatusCmd)
	loopCmd.AddCommand(loopStopCmd)

	loopCmd.Flags().BoolVarP(&loopWatch, "watch", "w", false, "Run as resident daemon (poll queue continuously)")

	rootCmd.AddCommand(loopCmd)
}

// runLoop is the main loop engine. When watch is true it runs as a resident
// daemon; otherwise it processes one pass and exits.
func runLoop(cmd *cobra.Command, args []string) error {
	if loopWatch {
		return runWatchDaemon(cmd)
	}
	return runOncePass(cmd)
}

// runOncePass picks the next task, processes it, and exits.
func runOncePass(cmd *cobra.Command) error {
	store, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	task, err := pickNextTask(store)
	if err != nil {
		return fmt.Errorf("pick next task: %w", err)
	}
	if task == nil {
		fmt.Println("No pending tasks.")
		return nil
	}

	return processTask(cmd.Context(), store, task)
}

// openStore opens and returns the default SQLite store.
func openStore() (*storage.SQLiteStore, error) {
	dbPath := storage.DefaultDBPath()
	store, err := storage.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	return store, nil
}

// pickNextTask returns the first open or needs-fix task, or nil if none exists.
func pickNextTask(store *storage.SQLiteStore) (*storage.Task, error) {
	tasks, err := store.QueryTasks()
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		t := &tasks[i]
		if t.Status == "open" || t.Status == "needs-fix" {
			return t, nil
		}
	}
	return nil, nil
}

// processTask marks the task in-progress, runs build→gate→fix, and records the outcome.
// It uses the mission engine via BuildGateFix (not yet constructed in this file;
// the concrete implementation shells out to the mission runner or runs inline).
func processTask(ctx context.Context, store *storage.SQLiteStore, task *storage.Task) error {
	// Mark in-progress.
	if err := store.UpdateTaskState(task.ID, "in-progress", ""); err != nil {
		return fmt.Errorf("mark in-progress: %w", err)
	}
	fmt.Printf("Processing task %s: %s\n", task.ID, task.Description)

	// Resolve input.
	spec := taskInput(task)

	// For now, run build→gate→fix via a simple inline flow:
	// In a full implementation this would construct a mission graph and run it.
	// The SPEC says we reuse processTask's existing logic — this is the skeleton.
	_ = spec

	// TODO: In the full implementation, BuildGateFix processes the spec,
	// runs the gate, and on reject runs the fix cycle.
	// For now we mark the task done (placeholder).
	if err := store.UpdateTaskState(task.ID, "done", ""); err != nil {
		return fmt.Errorf("mark done: %w", err)
	}
	return nil
}

// taskInput resolves the input for a task — reads spec_path if set, otherwise
// uses the description.
func taskInput(task *storage.Task) string {
	if task.SpecPath != "" {
		data, err := os.ReadFile(task.SpecPath)
		if err == nil {
			return string(data)
		}
		// Fall back to description on read error.
	}
	return task.Description
}

// sentinelExists checks for the stop sentinel file.
func sentinelExists() bool {
	_, err := os.Stat(filepath.Join(".colony", "loop.stop"))
	return err == nil
}

// removeSentinel removes the stop sentinel file if it exists.
func removeSentinel() {
	p := filepath.Join(".colony", "loop.stop")
	os.Remove(p)
}

// extractFeedback extracts the feedback text from the mission output envelope.
func extractFeedback(lastOutput string) string {
	// If the output contains gate feedback (from a reject/cycle), extract it.
	// Simple heuristic: look for "feedback:" or "Feedback:" lines.
	lines := strings.Split(lastOutput, "\n")
	var feedback []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(trimmed), "feedback:") {
			feedback = append(feedback, strings.TrimSpace(trimmed[len("feedback:"):]))
		}
	}
	if len(feedback) > 0 {
		return strings.Join(feedback, "\n")
	}
	return lastOutput
}

// triggerSentinel writes the stop sentinel file.
func triggerSentinel() {
	// Used by loop_stop to signal the daemon.
	sentinelPath := filepath.Join(".colony", "loop.stop")
	_ = os.WriteFile(sentinelPath, []byte("stop"), 0644)
}

// loopStopCmd is the `loop stop` command — writes the stop sentinel.
var loopStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Signal the daemon to stop gracefully",
	RunE: func(cmd *cobra.Command, args []string) error {
		triggerSentinel()
		fmt.Println("Stop signal sent to daemon.")
		return nil
	},
}
