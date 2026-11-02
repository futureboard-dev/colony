package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTaskBlockFieldsRoundTrip(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Truncate(time.Second).UTC()

	task := Task{
		ID: "b1", Description: "blocked task", State: "blocked",
		BlockTool: "claude", BlockHint: "check config/schema.yaml",
		BlockAttempts: 1, CreatedAt: now,
	}
	if err := db.InsertTask(task); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	tasks, err := db.QueryTasks(TaskFilter{ID: "b1"})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("QueryTasks: %v tasks=%d", err, len(tasks))
	}
	got := tasks[0]
	if got.BlockTool != "claude" {
		t.Errorf("BlockTool = %q, want %q", got.BlockTool, "claude")
	}
	if got.BlockHint != "check config/schema.yaml" {
		t.Errorf("BlockHint = %q, want %q", got.BlockHint, "check config/schema.yaml")
	}
	if got.BlockAttempts != 1 {
		t.Errorf("BlockAttempts = %d, want 1", got.BlockAttempts)
	}
}

func TestTaskBlockFieldsDefaultToZeroValue(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Truncate(time.Second).UTC()

	if err := db.InsertTask(Task{ID: "b2", Description: "d", State: "open", CreatedAt: now}); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	tasks, err := db.QueryTasks(TaskFilter{ID: "b2"})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("QueryTasks: %v tasks=%d", err, len(tasks))
	}
	got := tasks[0]
	if got.BlockTool != "" || got.BlockHint != "" || got.BlockAttempts != 0 {
		t.Errorf("expected zero-value block fields, got %+v", got)
	}
}

func TestUpdateTaskBlockTool(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Truncate(time.Second).UTC()
	if err := db.InsertTask(Task{ID: "b3", Description: "d", State: "blocked", CreatedAt: now}); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	if err := db.UpdateTaskBlockTool("b3", "crush"); err != nil {
		t.Fatalf("UpdateTaskBlockTool: %v", err)
	}

	tasks, err := db.QueryTasks(TaskFilter{ID: "b3"})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("QueryTasks: %v tasks=%d", err, len(tasks))
	}
	if tasks[0].BlockTool != "crush" {
		t.Errorf("BlockTool = %q, want %q", tasks[0].BlockTool, "crush")
	}
	if tasks[0].UpdatedAt == nil {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestUpdateTaskBlockHint(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Truncate(time.Second).UTC()
	if err := db.InsertTask(Task{ID: "b4", Description: "d", State: "blocked", CreatedAt: now}); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	if err := db.UpdateTaskBlockHint("b4", "it's the other path"); err != nil {
		t.Fatalf("UpdateTaskBlockHint: %v", err)
	}
	tasks, err := db.QueryTasks(TaskFilter{ID: "b4"})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("QueryTasks: %v tasks=%d", err, len(tasks))
	}
	if tasks[0].BlockHint != "it's the other path" {
		t.Errorf("BlockHint = %q, want %q", tasks[0].BlockHint, "it's the other path")
	}

	// Overwriting with empty clears the hint (reflects "no hint this round").
	if err := db.UpdateTaskBlockHint("b4", ""); err != nil {
		t.Fatalf("UpdateTaskBlockHint: %v", err)
	}
	tasks, _ = db.QueryTasks(TaskFilter{ID: "b4"})
	if tasks[0].BlockHint != "" {
		t.Errorf("BlockHint = %q, want cleared", tasks[0].BlockHint)
	}
}

func TestIncrementBlockAttempts(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Truncate(time.Second).UTC()
	if err := db.InsertTask(Task{ID: "b5", Description: "d", State: "blocked", CreatedAt: now}); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	n, err := db.IncrementBlockAttempts("b5")
	if err != nil {
		t.Fatalf("IncrementBlockAttempts: %v", err)
	}
	if n != 1 {
		t.Errorf("first increment = %d, want 1", n)
	}

	n, err = db.IncrementBlockAttempts("b5")
	if err != nil {
		t.Fatalf("IncrementBlockAttempts: %v", err)
	}
	if n != 2 {
		t.Errorf("second increment = %d, want 2", n)
	}

	tasks, err := db.QueryTasks(TaskFilter{ID: "b5"})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("QueryTasks: %v tasks=%d", err, len(tasks))
	}
	if tasks[0].BlockAttempts != 2 {
		t.Errorf("persisted BlockAttempts = %d, want 2", tasks[0].BlockAttempts)
	}
}

// TestReopenAfterBlockColumnsMigrated proves a second Open against the same
// path (already migrated) does not error on the idempotent ALTER TABLE adds
// for block_tool/block_hint/block_attempts.
func TestReopenAfterBlockColumnsMigrated(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "reopen.db")

	s1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	s1.Close()

	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	s2.Close()
}
