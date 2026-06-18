package storage

import (
	"testing"
	"time"
)

func TestInsertTask(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Truncate(time.Second).UTC()

	task := Task{
		ID:          "task-001",
		Description: "Fix login bug",
		SpecPath:    "SPEC.md",
		Status:      "open",
		Branch:      "agent/fix-login",
		PRURL:       "https://github.com/owner/repo/pull/42",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := db.InsertTask(task); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	tasks, err := db.QueryTasks()
	if err != nil {
		t.Fatalf("QueryTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	got := tasks[0]
	if got.ID != "task-001" {
		t.Errorf("expected id task-001, got %s", got.ID)
	}
	if got.Description != "Fix login bug" {
		t.Errorf("expected description 'Fix login bug', got %s", got.Description)
	}
	if got.SpecPath != "SPEC.md" {
		t.Errorf("expected spec_path SPEC.md, got %s", got.SpecPath)
	}
	if got.Status != "open" {
		t.Errorf("expected status open, got %s", got.Status)
	}
	if got.Branch != "agent/fix-login" {
		t.Errorf("expected branch agent/fix-login, got %s", got.Branch)
	}
	if got.PRURL != "https://github.com/owner/repo/pull/42" {
		t.Errorf("expected pr_url https://github.com/owner/repo/pull/42, got %s", got.PRURL)
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("expected created_at %v, got %v", now, got.CreatedAt)
	}
	if !got.UpdatedAt.Equal(now) {
		t.Errorf("expected updated_at %v, got %v", now, got.UpdatedAt)
	}
}

func TestInsertTaskDefaults(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Truncate(time.Second).UTC()

	task := Task{
		ID:        "task-002",
		Status:    "open",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.InsertTask(task); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	tasks, err := db.QueryTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	got := tasks[0]
	if got.Branch != "" {
		t.Errorf("expected empty branch, got %s", got.Branch)
	}
	if got.PRURL != "" {
		t.Errorf("expected empty pr_url, got %s", got.PRURL)
	}
}

func TestQueryTasksOrderByCreatedAt(t *testing.T) {
	db := openTestDB(t)
	base := time.Now().Truncate(time.Second).UTC()

	// Insert two tasks with different creation times.
	first := Task{ID: "task-001", CreatedAt: base, UpdatedAt: base}
	second := Task{ID: "task-002", CreatedAt: base.Add(time.Minute), UpdatedAt: base.Add(time.Minute)}
	if err := db.InsertTask(first); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertTask(second); err != nil {
		t.Fatal(err)
	}

	tasks, err := db.QueryTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
	if tasks[0].ID != "task-001" || tasks[1].ID != "task-002" {
		t.Errorf("expected task-001 first, task-002 second; got %s, %s", tasks[0].ID, tasks[1].ID)
	}
}

func TestUpdateTaskState(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Truncate(time.Second).UTC()

	task := Task{ID: "task-001", Status: "open", CreatedAt: now, UpdatedAt: now}
	if err := db.InsertTask(task); err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateTaskState("task-001", "needs-fix", "gate rejected: test failure"); err != nil {
		t.Fatalf("UpdateTaskState: %v", err)
	}

	tasks, err := db.QueryTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	got := tasks[0]
	if got.Status != "needs-fix" {
		t.Errorf("expected status needs-fix, got %s", got.Status)
	}
	if got.LastFeedback != "gate rejected: test failure" {
		t.Errorf("expected feedback 'gate rejected: test failure', got %s", got.LastFeedback)
	}
	if got.UpdatedAt.IsZero() {
		t.Errorf("expected updated_at to be set, got zero")
	}
}

func TestUpdateTaskStateDone(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Truncate(time.Second).UTC()

	task := Task{ID: "task-001", Status: "open", CreatedAt: now, UpdatedAt: now}
	if err := db.InsertTask(task); err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateTaskState("task-001", "done", "CI passed"); err != nil {
		t.Fatalf("UpdateTaskState: %v", err)
	}

	tasks, err := db.QueryTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	got := tasks[0]
	if got.Status != "done" {
		t.Errorf("expected status done, got %s", got.Status)
	}
	if got.LastFeedback != "CI passed" {
		t.Errorf("expected feedback 'CI passed', got %s", got.LastFeedback)
	}
}
