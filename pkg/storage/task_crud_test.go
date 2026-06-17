package storage

import (
	"testing"
	"time"
)

func TestTaskInsertAndQuery(t *testing.T) {
	db := openTestDB(t)

	now := time.Now().Truncate(time.Second).UTC()
	tasks := []Task{
		{ID: "t1", Description: "first task", State: "open", CreatedAt: now},
		{ID: "t2", Description: "second task", State: "done", CreatedAt: now.Add(1 * time.Second)},
		{ID: "t3", Description: "third task", State: "blocked", CreatedAt: now.Add(2 * time.Second)},
	}
	for _, tk := range tasks {
		if err := db.InsertTask(tk); err != nil {
			t.Fatalf("InsertTask(%q): %v", tk.ID, err)
		}
	}

	all, err := db.QueryTasks(TaskFilter{})
	if err != nil {
		t.Fatalf("QueryTasks: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(all))
	}

	// Verify t1 fields round-trip.
	got := all[0]
	if got.ID != "t1" || got.Description != "first task" || got.State != "open" {
		t.Errorf("t1 fields mismatch: %+v", got)
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("t1 CreatedAt: got %v, want %v", got.CreatedAt, now)
	}
}

func TestTaskQueryByStates(t *testing.T) {
	db := openTestDB(t)

	now := time.Now().Truncate(time.Second).UTC()
	states := []struct {
		id    string
		state string
		at    time.Time
	}{
		{"open-1", "open", now},
		{"needs-fix-1", "needs-fix", now.Add(1 * time.Second)},
		{"done-1", "done", now.Add(2 * time.Second)},
		{"blocked-1", "blocked", now.Add(3 * time.Second)},
	}
	for _, s := range states {
		if err := db.InsertTask(Task{ID: s.id, Description: s.id, State: s.state, CreatedAt: s.at}); err != nil {
			t.Fatalf("InsertTask(%q): %v", s.id, err)
		}
	}

	openAndFix, err := db.QueryTasks(TaskFilter{States: []string{"open", "needs-fix"}})
	if err != nil {
		t.Fatalf("QueryTasks: %v", err)
	}
	if len(openAndFix) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(openAndFix))
	}
	if openAndFix[0].ID != "open-1" {
		t.Errorf("expected first to be 'open-1', got %q", openAndFix[0].ID)
	}
	if openAndFix[1].ID != "needs-fix-1" {
		t.Errorf("expected second to be 'needs-fix-1', got %q", openAndFix[1].ID)
	}
}

func TestTaskUpdateState(t *testing.T) {
	db := openTestDB(t)

	now := time.Now().Truncate(time.Second).UTC()
	if err := db.InsertTask(Task{ID: "t-upd", Description: "update me", State: "open", CreatedAt: now}); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	if err := db.UpdateTaskState("t-upd", "done", ""); err != nil {
		t.Fatalf("UpdateTaskState: %v", err)
	}

	tasks, err := db.QueryTasks(TaskFilter{States: []string{"done"}})
	if err != nil {
		t.Fatalf("QueryTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 done task, got %d", len(tasks))
	}
	if tasks[0].State != "done" {
		t.Errorf("expected state 'done', got %q", tasks[0].State)
	}
	if tasks[0].UpdatedAt == nil {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestTaskIncrementCycle(t *testing.T) {
	db := openTestDB(t)

	now := time.Now().Truncate(time.Second).UTC()
	if err := db.InsertTask(Task{ID: "t-cyc", Description: "cycle me", State: "open", CreatedAt: now}); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	if err := db.IncrementCycle("t-cyc"); err != nil {
		t.Fatalf("IncrementCycle: %v", err)
	}

	tasks, err := db.QueryTasks(TaskFilter{})
	if err != nil {
		t.Fatalf("QueryTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].CycleCount != 1 {
		t.Errorf("expected cycle_count=1, got %d", tasks[0].CycleCount)
	}
}

func TestTaskUpdateStateWithFeedback(t *testing.T) {
	db := openTestDB(t)

	now := time.Now().Truncate(time.Second).UTC()
	if err := db.InsertTask(Task{ID: "t-fb", Description: "feedback test", State: "open", CreatedAt: now}); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	feedback := "lint errors: missing imports"
	if err := db.UpdateTaskState("t-fb", "needs-fix", feedback); err != nil {
		t.Fatalf("UpdateTaskState: %v", err)
	}

	tasks, err := db.QueryTasks(TaskFilter{States: []string{"needs-fix"}})
	if err != nil {
		t.Fatalf("QueryTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 needs-fix task, got %d", len(tasks))
	}
	if tasks[0].State != "needs-fix" {
		t.Errorf("expected state 'needs-fix', got %q", tasks[0].State)
	}
	if tasks[0].LastFeedback != feedback {
		t.Errorf("expected last_feedback %q, got %q", feedback, tasks[0].LastFeedback)
	}
}
