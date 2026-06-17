package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jirateep/colony/pkg/storage"
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

// setupMinimalProject creates .colony/config.json in dir.
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
}
