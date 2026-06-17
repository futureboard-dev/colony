package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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

	// Wait for the loop to process the first task (fails fast) and enter idle
	// sleep. During idle sleep, insert a new task and write the sentinel so
	// that when the loop wakes and picks the task, the sentinel is present at
	// the pass boundary.
	time.Sleep(100 * time.Millisecond)

	store, err = storage.Open(filepath.Join(dir, ".colony", "missions.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertTask(storage.Task{
		ID:          "sentinel-test-2",
		Description: "second task",
		State:       "open",
		Lang:        "go",
		CreatedAt:   time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	store.Close()

	if err := os.WriteFile(sentinel, nil, 0644); err != nil {
		t.Fatal(err)
	}

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
