package storage

import (
	"fmt"
	"sync"
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

func TestTaskQueryByID(t *testing.T) {
	db := openTestDB(t)

	now := time.Now().Truncate(time.Second).UTC()
	task := Task{
		ID: "test-task-1", Description: "fix login", SpecPath: "SPEC.md",
		Branch: "feature/login", PRURL: "https://github.com/owner/repo/pull/1",
		State: "open", CreatedAt: now,
	}
	if err := db.InsertTask(task); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	tasks, err := db.QueryTasks(TaskFilter{ID: "test-task-1"})
	if err != nil {
		t.Fatalf("QueryTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	got := tasks[0]
	if got.Description != "fix login" {
		t.Errorf("description: got %q, want %q", got.Description, "fix login")
	}
	if got.State != "open" {
		t.Errorf("state: got %q, want %q", got.State, "open")
	}
	if got.Branch != "feature/login" {
		t.Errorf("branch: got %q, want %q", got.Branch, "feature/login")
	}
	if got.PRURL != "https://github.com/owner/repo/pull/1" {
		t.Errorf("pr_url: got %q, want %q", got.PRURL, "https://github.com/owner/repo/pull/1")
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

func TestDeleteTask(t *testing.T) {
	db := openTestDB(t)

	now := time.Now().Truncate(time.Second).UTC()
	if err := db.InsertTask(Task{ID: "t-del", Description: "delete me", State: "open", CreatedAt: now}); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	if err := db.DeleteTask("t-del"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	tasks, err := db.QueryTasks(TaskFilter{})
	if err != nil {
		t.Fatalf("QueryTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks after delete, got %d", len(tasks))
	}

	// Deleting a missing id is a no-op, not an error.
	if err := db.DeleteTask("t-missing"); err != nil {
		t.Errorf("DeleteTask(missing) returned error: %v", err)
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

func TestPickNextOpenTaskPrioritizesNeedsFix(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Truncate(time.Second).UTC()
	// Insert needs-fix task older than open task.
	if err := db.InsertTask(Task{
		ID: "needs-fix-old", Description: "needs fix", State: "needs-fix",
		CreatedAt: now.Add(-1 * time.Hour),
	}); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}
	if err := db.InsertTask(Task{
		ID: "open-recent", Description: "open recent", State: "open",
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	picked, err := db.PickNextOpenTask()
	if err != nil {
		t.Fatalf("PickNextOpenTask: %v", err)
	}
	if picked == nil {
		t.Fatal("expected a task, got nil")
	}
	if picked.ID != "needs-fix-old" {
		t.Errorf("expected needs-fix-old (prioritized), got %s", picked.ID)
	}
}

func TestGroupCRUD(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Truncate(time.Second).UTC()

	g := TaskGroup{
		ID: "g1", Name: "payments", EpicBranch: "feature/payments",
		BaseBranch: "main", Lang: "go", State: "open", CreatedAt: now,
	}
	if err := db.InsertGroup(g); err != nil {
		t.Fatalf("InsertGroup: %v", err)
	}

	groups, err := db.QueryGroups(GroupFilter{ID: "g1"})
	if err != nil {
		t.Fatalf("QueryGroups: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	got := groups[0]
	if got.Name != "payments" || got.EpicBranch != "feature/payments" ||
		got.BaseBranch != "main" || got.Lang != "go" || got.State != "open" {
		t.Errorf("group fields mismatch: %+v", got)
	}

	if err := db.UpdateGroup("g1", "done", "https://github.com/o/r/pull/1"); err != nil {
		t.Fatalf("UpdateGroup: %v", err)
	}
	groups, err = db.QueryGroups(GroupFilter{ID: "g1"})
	if err != nil {
		t.Fatalf("QueryGroups: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].State != "done" || groups[0].PRURL != "https://github.com/o/r/pull/1" {
		t.Errorf("update not applied: %+v", groups[0])
	}
	if groups[0].UpdatedAt == nil {
		t.Error("expected UpdatedAt to be set")
	}

	// Filter tasks by GroupID.
	if err := db.InsertTask(Task{ID: "gt1", Description: "d", State: "open", GroupID: "g1", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertTask(Task{ID: "gt2", Description: "d2", State: "open", GroupID: "other", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	tasks, err := db.QueryTasks(TaskFilter{GroupID: "g1"})
	if err != nil {
		t.Fatalf("QueryTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "gt1" {
		t.Errorf("expected only gt1 for group g1, got %v", tasks)
	}
}

func TestClaimNextTask(t *testing.T) {
	now := time.Now().Truncate(time.Second).UTC()

	t.Run("returns oldest open", func(t *testing.T) {
		db := openTestDB(t)
		if err := db.InsertTask(Task{ID: "a", Description: "a", State: "open", CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := db.InsertTask(Task{ID: "b", Description: "b", State: "open", CreatedAt: now.Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
		got, err := db.ClaimNextTask()
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || got.ID != "a" {
			t.Fatalf("expected task a, got %+v", got)
		}
	})

	t.Run("includes needs-fix", func(t *testing.T) {
		db := openTestDB(t)
		if err := db.InsertTask(Task{ID: "nf", Description: "nf", State: "needs-fix", CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		got, err := db.ClaimNextTask()
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || got.ID != "nf" {
			t.Fatalf("expected task nf, got %+v", got)
		}
	})

	t.Run("skips task with unmet deps", func(t *testing.T) {
		db := openTestDB(t)
		if err := db.InsertTask(Task{ID: "dep", Description: "dep", State: "open", CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := db.InsertTask(Task{ID: "child", Description: "child", State: "open", DependsOn: "dep", CreatedAt: now.Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
		got, err := db.ClaimNextTask()
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || got.ID != "dep" {
			t.Fatalf("expected dep claimed first, got %+v", got)
		}
	})

	t.Run("claims dependent after dep done", func(t *testing.T) {
		db := openTestDB(t)
		if err := db.InsertTask(Task{ID: "dep2", Description: "dep2", State: "done", CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := db.InsertTask(Task{ID: "child2", Description: "child2", State: "open", DependsOn: "dep2", CreatedAt: now.Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
		got, err := db.ClaimNextTask()
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || got.ID != "child2" {
			t.Fatalf("expected child2, got %+v", got)
		}
	})

	t.Run("blocked dep keeps dependent unclaimable", func(t *testing.T) {
		db := openTestDB(t)
		if err := db.InsertTask(Task{ID: "dep3", Description: "dep3", State: "blocked", CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := db.InsertTask(Task{ID: "child3", Description: "child3", State: "open", DependsOn: "dep3", CreatedAt: now.Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
		got, err := db.ClaimNextTask()
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Fatalf("expected nil (child3 unclaimable, dep3 not open/needs-fix), got %+v", got)
		}
	})

	t.Run("returns nil when none claimable", func(t *testing.T) {
		db := openTestDB(t)
		if err := db.InsertTask(Task{ID: "done1", Description: "d", State: "done", CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		got, err := db.ClaimNextTask()
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
	})

	t.Run("sets state running", func(t *testing.T) {
		db := openTestDB(t)
		if err := db.InsertTask(Task{ID: "run1", Description: "d", State: "needs-fix", LastFeedback: "fb", CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		got, err := db.ClaimNextTask()
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || got.State != "needs-fix" {
			t.Fatalf("expected returned task to keep its claimed-from state needs-fix, got %+v", got)
		}
		tasks, err := db.QueryTasks(TaskFilter{ID: "run1"})
		if err != nil || len(tasks) != 1 {
			t.Fatalf("query: %v tasks=%d", err, len(tasks))
		}
		if tasks[0].State != "running" {
			t.Errorf("expected persisted state 'running', got %q", tasks[0].State)
		}
	})
}

func TestClaimNextTask_Concurrent(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Truncate(time.Second).UTC()
	for i := range 5 {
		if err := db.InsertTask(Task{
			ID: fmt.Sprintf("c%d", i), Description: "d", State: "open",
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	results := make(chan *Task, 10)
	errs := make(chan error, 10)
	for range 10 {
		wg.Go(func() {
			task, err := db.ClaimNextTask()
			if err != nil {
				errs <- err
				return
			}
			results <- task
		})
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("ClaimNextTask error: %v", err)
	}

	seen := map[string]bool{}
	claimed, nilCount := 0, 0
	for task := range results {
		if task == nil {
			nilCount++
			continue
		}
		claimed++
		if seen[task.ID] {
			t.Fatalf("task %s claimed twice", task.ID)
		}
		seen[task.ID] = true
	}
	if claimed != 5 {
		t.Errorf("expected 5 claimed tasks, got %d", claimed)
	}
	if nilCount != 5 {
		t.Errorf("expected 5 nil results, got %d", nilCount)
	}
}

func TestPickNextOpenTaskEmpty(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Truncate(time.Second).UTC()
	// Only done tasks.
	if err := db.InsertTask(Task{
		ID: "done-1", Description: "done", State: "done",
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	picked, err := db.PickNextOpenTask()
	if err != nil {
		t.Fatalf("PickNextOpenTask: %v", err)
	}
	if picked != nil {
		t.Errorf("expected nil for no open tasks, got %+v", picked)
	}
}
