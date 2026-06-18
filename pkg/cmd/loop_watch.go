package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jirateep/colony/pkg/storage"
	"github.com/spf13/cobra"
)

const (
	watchPollInterval = 30 * time.Second
	logRotationSize   = 10 * 1024 * 1024 // 10 MB
	logMaxRotated     = 3
)

// runWatchDaemon runs the resident daemon loop.
func runWatchDaemon(cmd *cobra.Command) error {
	// --- Pidfile lock ---
	pidPath := filepath.Join(".colony", "loop.pid")
	if err := acquirePidfile(pidPath); err != nil {
		return fmt.Errorf("pidfile: %w", err)
	}
	defer os.Remove(pidPath)

	// --- Log setup ---
	logPath := filepath.Join(".colony", "loop.log")
	logFile, err := openLog(logPath)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	defer logFile.Close()

	logger := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	logger.Info("daemon started", "pid", os.Getpid())

	// --- Open store ---
	store, err := openStore()
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = store.Close() }()

	// --- Context for graceful shutdown ---
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	// Watch for stop sentinel in a goroutine.
	sentinelPath := filepath.Join(".colony", "loop.stop")
	go watchSentinel(ctx, sentinelPath, cancel)

	// Also listen for SIGTERM/SIGINT via the command context.
	// Cobra sets up signal handling when we use cmd.Context().

	daemonLoop(ctx, store, logPath, logger)

	logger.Info("daemon stopped")
	return nil
}

// daemonLoop is the main poll loop.
func daemonLoop(ctx context.Context, store *storage.SQLiteStore, logPath string, logger *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Rotate log if needed.
		rotateLogIfNeeded(logPath, logger)

		// OBSERVE phase: poll CI and PR comments for tasks with pr_url.
		// This runs ONLY in --watch mode.
		observeTasks(ctx, store, logger)

		// Pick and process the next task.
		task, err := pickNextTask(store)
		if err != nil {
			logger.Error("pickNextTask", "err", err)
			sleepOrDone(ctx)
			continue
		}
		if task == nil {
			sleepOrDone(ctx)
			continue
		}

		logger.Info("processing task", "id", task.ID, "status", task.Status)
		if err := processWatchTask(ctx, store, task, logger); err != nil {
			logger.Error("processTask", "id", task.ID, "err", err)
		}

		// After processing, loop again immediately (don't sleep — pickNextTask
		// returns nil if no work, which will sleep).
	}
}

// processWatchTask wraps processTask with logging.
func processWatchTask(ctx context.Context, store *storage.SQLiteStore, task *storage.Task, logger *slog.Logger) error {
	if err := store.UpdateTaskState(task.ID, "in-progress", ""); err != nil {
		return fmt.Errorf("mark in-progress: %w", err)
	}

	spec := taskInput(task)
	logger.Info("running build->gate->fix", "id", task.ID)

	// BuildGateFix logic.
	outcome, feedback, err := buildGateFix(ctx, task.ID, spec, logger)
	if err != nil {
		if ue := store.UpdateTaskState(task.ID, "needs-fix", feedback); ue != nil {
			logger.Error("update task state", "err", ue)
		}
		return fmt.Errorf("buildGateFix: %w", err)
	}

	if outcome == "approved" {
		logger.Info("task approved", "id", task.ID)
		if ue := store.UpdateTaskState(task.ID, "done", feedback); ue != nil {
			logger.Error("update task state", "err", ue)
		}
	} else {
		logger.Info("task needs fix", "id", task.ID, "feedback", feedback)
		if ue := store.UpdateTaskState(task.ID, "needs-fix", feedback); ue != nil {
			logger.Error("update task state", "err", ue)
		}
	}
	return nil
}

// buildGateFix runs the build→gate→fix cycle. Returns outcome ("approved" or "rejected")
// and feedback text.
func buildGateFix(ctx context.Context, taskID, spec string, logger *slog.Logger) (outcome, feedback string, err error) {
	// Run build step.
	buildResult, err := runBuild(ctx, spec)
	if err != nil {
		return "", fmt.Sprintf("build failed: %v", err), err
	}

	// Run gate step.
	gateResult, gateFeedback := runGate(ctx, buildResult)
	if gateResult == "approved" {
		return "approved", "", nil
	}

	// Run fix step — only if gate rejected.
	fixResult, err := runFix(ctx, spec, buildResult, gateFeedback)
	if err != nil {
		return "", gateFeedback, err
	}
	_ = fixResult

	// Re-gate after fix.
	_, finalFeedback := runGate(ctx, fixResult)
	return "rejected", finalFeedback, nil
}

// runBuild executes the LLM build step.
func runBuild(ctx context.Context, spec string) (string, error) {
	// This would normally use the mission engine.
	// For now, a placeholder that logs and returns the spec.
	logger := slog.Default()
	logger.Info("build step", "spec_len", len(spec))
	return spec, nil
}

// runGate executes the LLM gate (review) step. Returns "approved" or "rejected"
// with feedback.
func runGate(ctx context.Context, buildOutput string) (string, string) {
	// Placeholder — in production calls the gate LLM.
	return "approved", ""
}

// runFix executes the LLM fix step.
func runFix(ctx context.Context, spec, buildOutput, gateFeedback string) (string, error) {
	return buildOutput, nil
}

// observeTasks runs the OBSERVE phase: polls CI status and PR comments for all
// tasks that have a pr_url set.
func observeTasks(ctx context.Context, store *storage.SQLiteStore, logger *slog.Logger) {
	tasks, err := store.QueryTasks()
	if err != nil {
		logger.Error("observe: query tasks", "err", err)
		return
	}
	for i := range tasks {
		t := &tasks[i]
		if t.PRURL == "" {
			continue
		}
		// Skip tasks already done — no need to re-observe.
		if t.Status == "done" {
			continue
		}
		// Skip tasks in-progress — avoid interference.
		if t.Status == "in-progress" {
			continue
		}

		changed := false
		newFeedback := ""

		// Poll CI status.
		ciStatus, ciOutput := pollCIStatus(t.Branch)
		switch ciStatus {
		case "failure":
			newFeedback = ciOutput
			logger.Info("observe: CI red", "task", t.ID, "branch", t.Branch)
			if ue := store.UpdateTaskState(t.ID, "needs-fix", newFeedback); ue != nil {
				logger.Error("update task state", "err", ue)
			}
			changed = true
		case "success":
			logger.Info("observe: CI green", "task", t.ID, "branch", t.Branch)
			if ue := store.UpdateTaskState(t.ID, "done", "CI passed"); ue != nil {
				logger.Error("update task state", "err", ue)
			}
			changed = true
		}

		// Poll PR comments — only if CI didn't already mark done.
		if !changed {
			comment, found := pollPRComments(t.PRURL, t.LastFeedback)
			if found {
				logger.Info("observe: new PR comment", "task", t.ID)
				if ue := store.UpdateTaskState(t.ID, "needs-fix", comment); ue != nil {
					logger.Error("update task state", "err", ue)
				}
				changed = true
			}
		}
	}
}

// pollCIStatus shells out to `gh run view` to check CI status for a branch.
// Returns "success", "failure", or "unknown" with output text.
func pollCIStatus(branch string) (status, output string) {
	if branch == "" {
		return "unknown", ""
	}
	out, err := runGH("run", "view", "--branch", branch, "--json", "conclusion,headBranch")
	if err != nil {
		return "unknown", fmt.Sprintf("gh run view: %v", err)
	}
	// Parse JSON output for conclusion field.
	if strings.Contains(out, `"conclusion":"failure"`) || strings.Contains(out, `"conclusion":"cancelled"`) || strings.Contains(out, `"conclusion":"timed_out"`) {
		return "failure", out
	}
	if strings.Contains(out, `"conclusion":"success"`) {
		return "success", out
	}
	return "unknown", out
}

// pollPRComments checks for new PR review comments since the last observed feedback.
// Returns (comment, found).
func pollPRComments(prURL, lastFeedback string) (string, bool) {
	if prURL == "" {
		return "", false
	}
	// Extract PR number from URL: https://github.com/owner/repo/pull/123
	parts := strings.Split(strings.TrimRight(prURL, "/"), "/")
	if len(parts) < 2 || parts[len(parts)-2] != "pull" {
		return "", false
	}
	prNum := parts[len(parts)-1]

	out, err := runGH("pr", "view", prNum, "--json", "comments,body")
	if err != nil {
		return "", false
	}
	// If no comments, nothing to do.
	if !strings.Contains(out, `"comments"`) {
		return "", false
	}
	// Extract the last comment body.
	// Simple heuristic: find the last occurrence of "body":"..." in the JSON.
	body := extractLastCommentBody(out)
	if body == "" {
		return "", false
	}
	// Avoid re-processing the same feedback.
	if body == lastFeedback {
		return "", false
	}
	return body, true
}

// extractLastCommentBody finds the last "body" field in a GitHub JSON response.
func extractLastCommentBody(jsonStr string) string {
	// Very simple extraction — looks for `"body":"..."` patterns.
	// In production, use proper JSON parsing.
	marker := `"body":"`
	start := strings.LastIndex(jsonStr, marker)
	if start < 0 {
		return ""
	}
	start += len(marker)
	// Find the closing quote of the body value.
	end := strings.Index(jsonStr[start:], `"`)
	if end < 0 {
		return ""
	}
	return jsonStr[start : start+end]
}

// acquirePidfile writes the pidfile or returns an error if the daemon is already running.
func acquirePidfile(pidPath string) error {
	// Check for stale pidfile.
	data, err := os.ReadFile(pidPath)
	if err == nil {
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil && pid > 0 {
			// Check if process is alive.
			proc, err := os.FindProcess(pid)
			if err == nil && proc != nil {
				if err := proc.Signal(os.Signal(nil)); err == nil {
					return fmt.Errorf("daemon already running (pid %d)", pid)
				}
			}
			// Stale pidfile — clean it.
			os.Remove(pidPath)
		}
	}
	if err := os.MkdirAll(filepath.Dir(pidPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644)
}

// openLog opens the log file for appending (create if not exist).
func openLog(logPath string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return nil, err
	}
	return os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
}

// rotateLogIfNeeded rotates the log file if it exceeds logRotationSize.
func rotateLogIfNeeded(logPath string, logger *slog.Logger) {
	fi, err := os.Stat(logPath)
	if err != nil {
		return
	}
	if fi.Size() < logRotationSize {
		return
	}
	logger.Info("rotating log", "path", logPath, "size", fi.Size())
	for i := logMaxRotated - 1; i >= 0; i-- {
		older := fmt.Sprintf("%s.%d", logPath, i)
		if _, err := os.Stat(older); err == nil {
			if i == logMaxRotated-1 {
				os.Remove(older)
			} else {
				_ = os.Rename(older, fmt.Sprintf("%s.%d", logPath, i+1))
			}
		}
	}
	_ = os.Rename(logPath, logPath+".1")
	// Reopen the log file (best-effort; old filehandle still works but we create a new one).
}

// watchSentinel polls the stop sentinel file and cancels the context when found.
func watchSentinel(ctx context.Context, sentinelPath string, cancel context.CancelFunc) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if _, err := os.Stat(sentinelPath); err == nil {
			os.Remove(sentinelPath)
			cancel()
			return
		}
		time.Sleep(1 * time.Second)
	}
}

// sleepOrDone sleeps for the poll interval, returning early if context is cancelled.
func sleepOrDone(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(watchPollInterval):
	}
}

// init registers the loop command's run handler (called once during cobra setup).
func init() {
	// Override the default RunE for loopCmd to route to runLoop.
	loopCmd.RunE = runLoop
}

// runGH runs the gh CLI with the given arguments and returns stdout.
func runGH(args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return string(ee.Stderr), err
		}
		return "", err
	}
	return string(out), nil
}
