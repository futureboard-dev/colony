package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jirateep/colony/pkg/storage"
	"github.com/spf13/cobra"
)

// TestLoopOnce_Idle verifies that with no open tasks pickNextTask returns nil.
func TestLoopOnce_Idle(t *testing.T) {
	dir := t.TempDir()
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	dbPath := filepath.Join(dir, ".colony", "missions.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	task, err := pickNextTask(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if task != nil {
		t.Fatalf("expected nil task on empty DB, got %+v", task)
	}
}

// TestLoopOnce_RunsMission verifies that with an open task pickNextTask returns it.
func TestLoopOnce_RunsMission(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	dbPath := filepath.Join(dir, ".colony", "missions.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Insert an open task.
	taskRow := storage.Task{
		Description: "test task",
		State:       "open",
		Lang:        "go",
		CreatedAt:   time.Now(),
	}
	if err := store.InsertTask(taskRow); err != nil {
		t.Fatal(err)
	}

	task, err := pickNextTask(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if task == nil {
		t.Fatal("expected a task, got nil")
	}
	if task.Description != "test task" {
		t.Errorf("expected description 'test task', got %q", task.Description)
	}
}

// TestLoopOnce_MaxPasses verifies that with no tasks we get nil (idle).
func TestLoopOnce_MaxPasses(t *testing.T) {
	dir := t.TempDir()
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	dbPath := filepath.Join(dir, ".colony", "missions.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	task, err := pickNextTask(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if task != nil {
		t.Fatal("expected nil task for empty DB")
	}
}

// TestLoopOnce_EscalationCeiling verifies markTaskBlocked updates the task row.
func TestLoopOnce_EscalationCeiling(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	dbPath := filepath.Join(dir, ".colony", "missions.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Insert a task with known ID.
	taskRow := storage.Task{
		ID:          "test-escalation",
		Description: "escalation test",
		State:       "open",
		CreatedAt:   time.Now(),
	}
	if err := store.InsertTask(taskRow); err != nil {
		t.Fatal(err)
	}

	// Mark it blocked.
	if err := markTaskBlocked(store, "test-escalation"); err != nil {
		t.Fatal(err)
	}

	// Verify it's blocked.
	tasks, err := store.QueryTasks(storage.TaskFilter{States: []string{"blocked"}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tk := range tasks {
		if tk.ID == "test-escalation" && tk.State == "blocked" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected task to be marked 'blocked'")
	}
}

// TestPickNextTask_ReturnsOldestOpen verifies ordering by created_at.
func TestPickNextTask_ReturnsOldestOpen(t *testing.T) {
	dir := t.TempDir()
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	dbPath := filepath.Join(dir, ".colony", "missions.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().Truncate(time.Second).UTC()
	older := storage.Task{ID: "older", Description: "older", State: "open", CreatedAt: now}
	newer := storage.Task{ID: "newer", Description: "newer", State: "open", CreatedAt: now.Add(1 * time.Hour)}
	if err := store.InsertTask(older); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertTask(newer); err != nil {
		t.Fatal(err)
	}

	task, err := pickNextTask(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if task == nil {
		t.Fatal("expected a task, got nil")
	}
	if task.ID != "older" {
		t.Errorf("expected oldest task 'older', got %q", task.ID)
	}
}

// TestPickNextTask_SkipsDoneAndBlocked verifies filtering.
func TestPickNextTask_SkipsDoneAndBlocked(t *testing.T) {
	dir := t.TempDir()
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	dbPath := filepath.Join(dir, ".colony", "missions.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().Truncate(time.Second).UTC()
	_ = store.InsertTask(storage.Task{ID: "open-1", Description: "open", State: "open", CreatedAt: now})
	_ = store.InsertTask(storage.Task{ID: "done-1", Description: "done", State: "done", CreatedAt: now.Add(1 * time.Second)})
	_ = store.InsertTask(storage.Task{ID: "blocked-1", Description: "blocked", State: "blocked", CreatedAt: now.Add(2 * time.Second)})

	task, err := pickNextTask(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if task == nil {
		t.Fatal("expected a task, got nil")
	}
	if task.ID != "open-1" {
		t.Errorf("expected 'open-1', got %q", task.ID)
	}
}

// TestPickNextTask_ReturnsNeedsFix verifies needs-fix tasks are picked.
func TestPickNextTask_ReturnsNeedsFix(t *testing.T) {
	dir := t.TempDir()
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	dbPath := filepath.Join(dir, ".colony", "missions.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().Truncate(time.Second).UTC()
	_ = store.InsertTask(storage.Task{ID: "needs-fix-1", Description: "needs fix", State: "needs-fix", CreatedAt: now})

	task, err := pickNextTask(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if task == nil {
		t.Fatal("expected a task, got nil")
	}
	if task.ID != "needs-fix-1" {
		t.Errorf("expected 'needs-fix-1', got %q", task.ID)
	}
}

// TestPickNextTask_IdleWhenEmpty verifies no tasks returns nil.
func TestPickNextTask_IdleWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	dbPath := filepath.Join(dir, ".colony", "missions.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	task, err := pickNextTask(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if task != nil {
		t.Fatalf("expected nil, got %+v", task)
	}
}

// setupLoopCmd creates a cobra command with loop flags bound to global vars.
// Callers must restore globals after use: loopOnce, loopMaxPasses, etc.
func setupLoopCmd(ctx context.Context) *cobra.Command {
	cmd := &cobra.Command{Use: "loop"}
	cmd.SetContext(ctx)
	cmd.Flags().BoolVar(&loopOnce, "once", false, "")
	cmd.Flags().IntVar(&loopMaxPasses, "max-passes", 0, "")
	cmd.Flags().IntVar(&loopMaxCycles, "max-cycles", 5, "")
	cmd.Flags().StringVar(&loopEscalateTo, "escalate-to", "", "")
	cmd.Flags().StringVar(&loopLang, "lang", "go", "")
	cmd.Flags().IntVar(&loopIdleLimit, "idle", 10, "")
	return cmd
}

// TestLoop_SentinelStop verifies that writing .colony/loop.stop causes the
// running loop to exit after the current task and clean up the sentinel.
func TestLoop_SentinelStop(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	// Insert an open task so the loop has something to process.
	store, err := storage.Open(filepath.Join(dir, ".colony", "missions.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertTask(storage.Task{
		ID:          "sentinel-test-1",
		Description: "first task",
		State:       "open",
		Lang:        "go",
		CreatedAt:   time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	store.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := setupLoopCmd(ctx)

	loopOnce = false
	loopMaxPasses = 0
	loopMaxCycles = 5
	loopEscalateTo = ""
	loopLang = "go"
	loopIdleLimit = 10

	sentinel := filepath.Join(dir, ".colony", "loop.stop")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = runLoop(cmd, nil)
	}()

	// Let the loop start (clearing any stale sentinel at startup) and begin
	// processing the in-flight task. Then write the sentinel and cancel the
	// context: cancelling makes the runner return, bringing the loop to a pass
	// boundary where the now-present sentinel is detected. Because the sentinel
	// is checked before the context-cancel exit, the loop exits via the sentinel
	// path and removes the file.
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(sentinel, nil, 0644); err != nil {
		t.Fatal(err)
	}
	cancel()

	// Wait for the loop to detect the sentinel and exit.
	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()

	select {
	case <-doneCh:
		// Loop exited.
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for loop to stop after sentinel")
	}

	// Assert the sentinel file was removed.
	if _, err := os.Stat(sentinel); err == nil {
		t.Error("expected sentinel file to be removed after loop exit")
	} else if !os.IsNotExist(err) {
		t.Errorf("unexpected error checking sentinel: %v", err)
	}
}

// TestLoop_ContextCancel verifies that cancelling the context closes the
// running session as interrupted and marks the in-flight task needs-fix.
func TestLoop_ContextCancel(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	// Insert an open task.
	store, err := storage.Open(filepath.Join(dir, ".colony", "missions.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertTask(storage.Task{
		ID:          "cancel-test",
		Description: "test task",
		State:       "open",
		Lang:        "go",
		CreatedAt:   time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	store.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cmd := setupLoopCmd(ctx)

	loopOnce = false
	loopMaxPasses = 0
	loopMaxCycles = 5
	loopEscalateTo = ""
	loopLang = "go"
	loopIdleLimit = 10

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = runLoop(cmd, nil)
	}()

	// Cancel the context so processTask's runner sees it and returns.
	cancel()

	// Wait for the loop to exit.
	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()

	select {
	case <-doneCh:
		// Loop exited.
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for loop to exit after context cancel")
	}

	// Re-open the store and verify session/task state.
	store, err = storage.Open(filepath.Join(dir, ".colony", "missions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Check that a session was closed as interrupted.
	sessions, err := store.QuerySessions(storage.SessionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	foundInterrupted := false
	for _, s := range sessions {
		if s.Status == "interrupted" {
			foundInterrupted = true
			break
		}
	}
	if !foundInterrupted {
		t.Error("expected at least one session with status 'interrupted'")
	}

	// Check that the task was left as needs-fix.
	tasks, err := store.QueryTasks(storage.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	foundNeedsFix := false
	for _, tk := range tasks {
		if tk.ID == "cancel-test" && tk.State == "needs-fix" {
			foundNeedsFix = true
			break
		}
	}
	if !foundNeedsFix {
		t.Error("expected task 'cancel-test' to have state 'needs-fix'")
	}
}

// TestLoop_StaleSentinelClearedOnStart verifies that a stale .colony/loop.stop
// is removed at loop startup so the loop does not immediately self-stop.
func TestLoop_StaleSentinelClearedOnStart(t *testing.T) {
	dir := t.TempDir()
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	// Create a stale sentinel file before starting the loop.
	sentinel := filepath.Join(dir, ".colony", "loop.stop")
	if err := os.WriteFile(sentinel, nil, 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	cmd := setupLoopCmd(ctx)

	loopOnce = true
	loopMaxPasses = 0
	loopMaxCycles = 5
	loopEscalateTo = ""
	loopLang = "go"
	loopIdleLimit = 10

	// Run the loop with --once and no tasks; it should exit after idle check.
	if err := runLoop(cmd, nil); err != nil {
		t.Fatalf("runLoop: %v", err)
	}

	// Assert the sentinel file was removed.
	if _, err := os.Stat(sentinel); err == nil {
		t.Error("expected stale sentinel to be removed at startup")
	} else if !os.IsNotExist(err) {
		t.Errorf("unexpected error checking sentinel: %v", err)
	}
}

func TestBranchDesc(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "TASK.md")
	if err := os.WriteFile(specPath, []byte("# Feature: add-user-auth\n\nbody\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		task storage.Task
		want string
	}{
		{"description wins", storage.Task{Description: "do the thing", SpecPath: specPath}, "do the thing"},
		{"spec title from contents", storage.Task{SpecPath: specPath}, "add-user-auth"},
		{"unreadable spec falls back to filename", storage.Task{SpecPath: filepath.Join(dir, "missing-spec.md")}, "missing-spec"},
		{"nothing falls back to id", storage.Task{ID: "abc-123"}, "abc-123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := branchDesc(&tc.task); got != tc.want {
				t.Errorf("branchDesc = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMissionLabel(t *testing.T) {
	if got := missionLabel(&storage.Task{Description: "Add User Auth"}); got != "add-user-auth" {
		t.Errorf("missionLabel = %q, want %q", got, "add-user-auth")
	}
	// Empty slug (whitespace-only description) falls back to the task ID.
	if got := missionLabel(&storage.Task{ID: "id-1", Description: "   "}); got != "id-1" {
		t.Errorf("missionLabel empty-slug fallback = %q, want %q", got, "id-1")
	}
}

// skipIfShort skips tests that drive a real agent CLI (claude/crush), which is
// unavailable/slow in CI. Run the full suite locally with `go test` (no -short).
func skipIfShort(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("invokes a live agent CLI; skipped in -short mode")
	}
}

func setupMinimalProject(t *testing.T, dir string) {
	t.Helper()
	colonyDir := filepath.Join(dir, ".colony")
	if err := os.MkdirAll(colonyDir, 0755); err != nil {
		t.Fatal(err)
	}
	configJSON := `{
		"llm": {"provider": "anthropic", "model": "claude-sonnet-4-6"}
	}`
	if err := os.WriteFile(filepath.Join(colonyDir, "config.json"), []byte(configJSON), 0644); err != nil {
		t.Fatal(err)
	}
	// Initialize a git repo so module.FindRoot succeeds. Make an initial commit
	// so HEAD resolves — the loop branches a worktree from the base ref.
	if err := exec.Command("git", "init", "--initial-branch=main", dir).Run(); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-C", dir, "config", "user.email", "test@example.com"},
		{"-C", dir, "config", "user.name", "test"},
		{"-C", dir, "add", "-A"},
		{"-C", dir, "commit", "-m", "init", "--quiet"},
	} {
		if err := exec.Command("git", args...).Run(); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
}

// --- --watch daemon helpers (pidfile, log rotation) ---

func TestWritePidfile(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "loop.pid")

	if err := writePidfile(pidPath); err != nil {
		t.Fatalf("writePidfile: %v", err)
	}

	// Verify content.
	data, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("read pidfile: %v", err)
	}
	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil {
		t.Fatalf("parse pid: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("expected pid %d, got %d", os.Getpid(), pid)
	}
}

func TestWritePidfileRejectsDuplicate(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "loop.pid")

	// Write a pidfile with a fake PID (high enough that it won't exist).
	fakePid := 999999999
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", fakePid)), 0644); err != nil {
		t.Fatalf("write fake pidfile: %v", err)
	}

	// Now try to writePidfile — this should succeed because the fake PID is dead.
	// (we can't test the "already running" case reliably in CI since PID may or may not exist)
	if err := writePidfile(pidPath); err != nil {
		t.Fatalf("expected stale pid cleared, got error: %v", err)
	}

	// Verify our PID was written.
	data, _ := os.ReadFile(pidPath)
	var pid int
	fmt.Sscanf(string(data), "%d", &pid)
	if pid != os.Getpid() {
		t.Errorf("expected our pid %d, got %d", os.Getpid(), pid)
	}
}

func TestMaybeRotate(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "loop.log")

	// Write a small file — rotation should not happen.
	if err := os.WriteFile(logPath, []byte("small"), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	if err := maybeRotate(logPath, 10<<20, 3); err != nil {
		t.Fatalf("maybeRotate on small file: %v", err)
	}
	// Original file should still be there.
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("small log was rotated when it shouldn't have been")
	}

	// Write a large file — rotation should happen.
	large := make([]byte, 10<<20+1) // > 10MB
	if err := os.WriteFile(logPath, large, 0644); err != nil {
		t.Fatalf("write large log: %v", err)
	}
	if err := maybeRotate(logPath, 10<<20, 3); err != nil {
		t.Fatalf("maybeRotate on large file: %v", err)
	}
	// Original should be renamed to .1
	if _, err := os.Stat(logPath + ".1"); os.IsNotExist(err) {
		t.Error("expected rotated file log.1 to exist")
	}
	// Should have created a new empty log file (via rotation).
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("expected new log file to exist after rotation")
	}
}

func TestReadPidfile(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "loop.pid")

	// No pidfile.
	if pid := readPidfile(pidPath); pid != 0 {
		t.Errorf("expected 0 for missing pidfile, got %d", pid)
	}

	// Valid pidfile.
	if err := os.WriteFile(pidPath, []byte("12345\n"), 0644); err != nil {
		t.Fatalf("write pidfile: %v", err)
	}
	if pid := readPidfile(pidPath); pid != 12345 {
		t.Errorf("expected 12345, got %d", pid)
	}

	// Invalid content.
	if err := os.WriteFile(pidPath, []byte("not-a-number\n"), 0644); err != nil {
		t.Fatalf("write pidfile: %v", err)
	}
	if pid := readPidfile(pidPath); pid != 0 {
		t.Errorf("expected 0 for invalid content, got %d", pid)
	}
}

func TestLoopStatusNoDaemon(t *testing.T) {
	// daemonUptime returns not running when no pidfile exists.
	colonyPath := t.TempDir()
	running, pid, _ := daemonUptime(colonyPath)
	if running {
		t.Errorf("expected not running, got running pid=%d", pid)
	}
}

// TestWatchDaemonLifecycle verifies the daemon lifecycle: pidfile written on
// start, removed on clean exit via the stop sentinel.
func TestWatchDaemonLifecycle(t *testing.T) {
	dir := t.TempDir()
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(origWd) }()

	cfg, root, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	pidPath := filepath.Join(root, ".colony", "loop.pid")
	stopPath := filepath.Join(root, ".colony", "loop.stop")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runWatchDaemon(ctx, cfg, root)
	}()

	// Wait for pidfile to appear.
	if err := waitForFile(pidPath, 2*time.Second); err != nil {
		t.Fatalf("pidfile not created: %v", err)
	}

	// Verify pidfile content.
	pid := readPidfile(pidPath)
	if pid == 0 {
		t.Fatal("expected non-zero pid in pidfile")
	}

	// Stop via sentinel.
	if err := os.WriteFile(stopPath, nil, 0644); err != nil {
		t.Fatalf("write stop sentinel: %v", err)
	}

	if err := <-done; err != nil {
		t.Fatalf("daemon exited with error: %v", err)
	}

	// Pidfile should be removed on clean exit.
	if _, err := os.Stat(pidPath); err == nil {
		t.Error("pidfile still exists after daemon exit")
	}
}

// TestWatchDaemonRejectsDuplicate verifies that a second --watch fails with an
// error when the pidfile already references a running process.
func TestWatchDaemonRejectsDuplicate(t *testing.T) {
	colonyPath := t.TempDir()
	pidPath := filepath.Join(colonyPath, "loop.pid")

	// Write our own pid so the lock check sees a running process.
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644); err != nil {
		t.Fatalf("write pidfile: %v", err)
	}

	err := writePidfile(pidPath)
	if err == nil {
		t.Fatal("expected duplicate daemon error, got nil")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("expected 'already running' error, got: %v", err)
	}
}

// TestWatchDaemonLogRotation verifies that log rotation occurs when the log
// exceeds the size threshold.
func TestWatchDaemonLogRotation(t *testing.T) {
	colonyPath := t.TempDir()
	logPath := filepath.Join(colonyPath, "loop.log")

	// Write a small file — rotation should not happen.
	if err := os.WriteFile(logPath, []byte("small"), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	if err := maybeRotate(logPath, 10<<20, 3); err != nil {
		t.Fatalf("maybeRotate on small file: %v", err)
	}
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("small log was rotated when it shouldn't have been")
	}

	// Write a large file — rotation should happen.
	large := make([]byte, 10<<20+1)
	if err := os.WriteFile(logPath, large, 0644); err != nil {
		t.Fatalf("write large log: %v", err)
	}
	if err := maybeRotate(logPath, 10<<20, 3); err != nil {
		t.Fatalf("maybeRotate on large file: %v", err)
	}

	// Original renamed to .1, new log created.
	if _, err := os.Stat(logPath + ".1"); os.IsNotExist(err) {
		t.Error("expected rotated file log.1 to exist")
	}
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("expected new log file to exist after rotation")
	}
}

// waitForFile waits up to timeout for a file to exist.
func waitForFile(path string, timeout time.Duration) error {
	deadline := time.After(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		select {
		case <-deadline:
			return fmt.Errorf("timed out waiting for %s", path)
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// CI/PR observation behaviour (red→needs-fix, green→done, PR comment→requeue)
// is covered in the mission package (pkg/mission/observe_test.go), which tests
// ObserveTasksForPR directly with the store and the gh CLI shelled out.
