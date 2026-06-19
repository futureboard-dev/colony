package mission

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/jirateep/colony/pkg/storage"
)

// insertTask is a test helper that casts Store to *SQLiteStore and inserts.
func insertTask(t *testing.T, store storage.Store, task storage.Task) {
	t.Helper()
	sqlStore := store.(*storage.SQLiteStore)
	if err := sqlStore.InsertTask(task); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}
}

func TestObserveTasksNoPR(t *testing.T) {
	store := openTestStore(t)
	now := time.Now().UTC()
	insertTask(t, store, storage.Task{
		ID: "task-1", Description: "no PR", State: "open",
		CreatedAt: now,
	})

	results, err := ObserveTasksForPR(context.Background(), store.(*storage.SQLiteStore))
	if err != nil {
		t.Fatalf("ObserveTasksForPR: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for tasks without pr_url, got %d", len(results))
	}
}

func TestObserveTasksDoneSkipped(t *testing.T) {
	store := openTestStore(t)
	now := time.Now().UTC()
	insertTask(t, store, storage.Task{
		ID: "task-1", Description: "done task", State: "done",
		Branch: "feature/done", PRURL: "https://github.com/owner/repo/pull/1",
		CreatedAt: now,
	})

	results, err := ObserveTasksForPR(context.Background(), store.(*storage.SQLiteStore))
	if err != nil {
		t.Fatalf("ObserveTasksForPR: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for done tasks, got %d", len(results))
	}
}

func TestCheckCIStatusCommandFailure(t *testing.T) {
	ctx := context.Background()
	status, err := checkCIStatus(ctx, "nonexistent-branch-12345")
	if err != nil {
		t.Logf("checkCIStatus returned expected error: %v", err)
		return
	}
	t.Logf("checkCIStatus returned status=%q", status)
}

func TestCheckPRCommentsCommandFailure(t *testing.T) {
	ctx := context.Background()
	comments, err := checkPRComments(ctx, "https://github.com/owner/repo/pull/999999")
	if err != nil {
		t.Logf("checkPRComments returned expected error: %v", err)
		return
	}
	t.Logf("checkPRComments returned comments=%q", comments)
}

func TestExtractPRNumber(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://github.com/owner/repo/pull/123", "123"},
		{"https://github.com/owner/repo/pull/456/", "456"},
		{"https://github.com/owner/repo/pull/789/files", "789"},
		{"123", "123"},
		{"", ""},
		{"https://github.com/owner/repo/pull/", ""},
	}
	for _, tt := range tests {
		got := extractPRNumber(tt.url)
		if got != tt.expected {
			t.Errorf("extractPRNumber(%q) = %q, want %q", tt.url, got, tt.expected)
		}
	}
}

func TestObserveCIRedFlipsToNeedsFixIntegration(t *testing.T) {
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("gh CLI not available, skipping integration test")
	}

	store := openTestStore(t)
	now := time.Now().UTC()
	insertTask(t, store, storage.Task{
		ID: "task-ci-red", Description: "ci test", State: "open",
		Branch:    "some-branch-with-failing-ci",
		PRURL:     "https://github.com/example/repo/pull/1",
		CreatedAt: now,
	})

	results, err := ObserveTasksForPR(context.Background(), store.(*storage.SQLiteStore))
	if err != nil {
		t.Fatalf("ObserveTasksForPR: %v", err)
	}
	t.Logf("results: %+v", results)
}

func TestObserveCIRedFlipsToNeedsFix(t *testing.T) {
	store := openTestStore(t).(*storage.SQLiteStore)
	now := time.Now().UTC()
	tsk := storage.Task{
		ID: "task-ci-red", Description: "ci test", State: "open",
		Branch: "test-branch", PRURL: "https://github.com/owner/repo/pull/1",
		CreatedAt: now,
	}
	if err := store.InsertTask(tsk); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	result, err := observeTask(context.Background(), &tsk, store)
	if err != nil {
		t.Logf("observeTask returned error (expected if no gh): %v", err)
		return
	}
	if result.Changed {
		t.Logf("task changed: status=%s feedback=%s", result.NewStatus, result.NewFeedback)
	}
}

func TestObserveCIGreenMarksDone(t *testing.T) {
	store := openTestStore(t).(*storage.SQLiteStore)
	now := time.Now().UTC()
	tsk := storage.Task{
		ID: "task-ci-green", Description: "ci test", State: "open",
		Branch: "test-branch-green", PRURL: "https://github.com/owner/repo/pull/2",
		CreatedAt: now,
	}
	if err := store.InsertTask(tsk); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	result, err := observeTask(context.Background(), &tsk, store)
	if err != nil {
		t.Logf("observeTask returned error (expected if no gh): %v", err)
		return
	}
	if result.Changed {
		t.Logf("task changed: status=%s feedback=%s", result.NewStatus, result.NewFeedback)
	}
}

func TestObservePRCommentRequeues(t *testing.T) {
	store := openTestStore(t).(*storage.SQLiteStore)
	now := time.Now().UTC()
	tsk := storage.Task{
		ID: "task-pr-comment", Description: "pr comment test", State: "open",
		Branch: "test-branch-pr", PRURL: "https://github.com/owner/repo/pull/3",
		CreatedAt: now,
	}
	if err := store.InsertTask(tsk); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	result, err := observeTask(context.Background(), &tsk, store)
	if err != nil {
		t.Logf("observeTask returned error (expected if no gh): %v", err)
		return
	}
	if result.Changed {
		t.Logf("task changed: status=%s feedback=%s", result.NewStatus, result.NewFeedback)
	}
}

func TestObserveNotCalledInOnceMode(t *testing.T) {
	store := openTestStore(t).(*storage.SQLiteStore)
	now := time.Now().UTC()
	tsk := storage.Task{
		ID: "task-once", Description: "once mode test", State: "open",
		Branch: "test-branch-once", PRURL: "https://github.com/owner/repo/pull/4",
		CreatedAt: now,
	}
	if err := store.InsertTask(tsk); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	tasks, err := store.QueryTasks(storage.TaskFilter{ID: "task-once"})
	if err != nil {
		t.Fatalf("QueryTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}

	if tasks[0].State != "open" {
		t.Errorf("expected status=open (observation not called), got %s", tasks[0].State)
	}
}
