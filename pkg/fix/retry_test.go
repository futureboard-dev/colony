package fix

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/futureboard-dev/colony/pkg/storage"
)

func openTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// noCloseStore wraps a storage.Store so Retry's own Close() (appropriate
// against the fresh connection defaultOpenStore hands it in production)
// doesn't tear down a test's shared *storage.SQLiteStore.
type noCloseStore struct{ storage.Store }

func (noCloseStore) Close() error { return nil }

// testOpener returns an open func, for injecting into storeRetrier, that
// hands back db wrapped so Retry's internal Close() is a no-op.
func testOpener(db storage.Store) func() (storage.Store, error) {
	return func() (storage.Store, error) { return noCloseStore{db}, nil }
}

func TestStoreRetrier_KeepsReasonHintAndBranch(t *testing.T) {
	db := openTestStore(t)
	now := time.Now().Truncate(time.Second).UTC()
	task := storage.Task{
		ID: "t1", Description: "fix thing", State: "blocked",
		LastFeedback: "cannot find config/schema.yaml",
		Branch:       "agent/t1-worktree",
		CreatedAt:    now,
	}
	if err := db.InsertTask(task); err != nil {
		t.Fatal(err)
	}

	r := storeRetrier{open: testOpener(db)}
	req := Request{TaskID: "t1", Hint: "it's at config/app-schema.yaml"}
	if err := r.Retry(context.Background(), req); err != nil {
		t.Fatalf("Retry: %v", err)
	}

	tasks, err := db.QueryTasks(storage.TaskFilter{ID: "t1"})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("QueryTasks: %v tasks=%d", err, len(tasks))
	}
	got := tasks[0]
	if got.State != "needs-fix" {
		t.Errorf("state = %q, want needs-fix", got.State)
	}
	if got.Branch != "agent/t1-worktree" {
		t.Errorf("branch = %q, want it retained", got.Branch)
	}
	if !strings.Contains(got.LastFeedback, "cannot find config/schema.yaml") {
		t.Errorf("feedback %q missing original reason", got.LastFeedback)
	}
	if !strings.Contains(got.LastFeedback, "it's at config/app-schema.yaml") {
		t.Errorf("feedback %q missing hint", got.LastFeedback)
	}
	if got.BlockHint != "it's at config/app-schema.yaml" {
		t.Errorf("BlockHint = %q, want hint persisted", got.BlockHint)
	}
}

func TestStoreRetrier_ExplicitReasonOverridesStoredFeedback(t *testing.T) {
	db := openTestStore(t)
	now := time.Now().Truncate(time.Second).UTC()
	if err := db.InsertTask(storage.Task{
		ID: "t2", Description: "d", State: "blocked",
		LastFeedback: "stale reason", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	r := storeRetrier{open: testOpener(db)}
	req := Request{TaskID: "t2", Reason: "fresh reason from explainer"}
	if err := r.Retry(context.Background(), req); err != nil {
		t.Fatalf("Retry: %v", err)
	}

	tasks, _ := db.QueryTasks(storage.TaskFilter{ID: "t2"})
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].LastFeedback != "fresh reason from explainer" {
		t.Errorf("LastFeedback = %q, want explicit reason to win", tasks[0].LastFeedback)
	}
}

func TestStoreRetrier_NoHintLeavesFeedbackAsReasonOnly(t *testing.T) {
	db := openTestStore(t)
	now := time.Now().Truncate(time.Second).UTC()
	if err := db.InsertTask(storage.Task{
		ID: "t3", Description: "d", State: "blocked",
		LastFeedback: "the original reason", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	r := storeRetrier{open: testOpener(db)}
	if err := r.Retry(context.Background(), Request{TaskID: "t3"}); err != nil {
		t.Fatalf("Retry: %v", err)
	}

	tasks, _ := db.QueryTasks(storage.TaskFilter{ID: "t3"})
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].LastFeedback != "the original reason" {
		t.Errorf("LastFeedback = %q, want unchanged reason with no hint appended", tasks[0].LastFeedback)
	}
	if tasks[0].BlockHint != "" {
		t.Errorf("BlockHint = %q, want empty", tasks[0].BlockHint)
	}
}

func TestStoreRetrier_UnknownTaskErrors(t *testing.T) {
	db := openTestStore(t)
	r := storeRetrier{open: testOpener(db)}
	err := r.Retry(context.Background(), Request{TaskID: "missing"})
	if err == nil {
		t.Fatal("expected error for unknown task")
	}
}

// TestRetry_DelegatesToRegisteredRetrier exercises the package-level helper
// (used by every caller outside pkg/fix) against the same storeRetrier.
func TestRetry_DelegatesToRegisteredRetrier(t *testing.T) {
	db := openTestStore(t)
	now := time.Now().Truncate(time.Second).UTC()
	if err := db.InsertTask(storage.Task{
		ID: "t4", Description: "d", State: "blocked",
		LastFeedback: "reason", Branch: "agent/t4", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	SetRetrier(storeRetrier{open: testOpener(db)})
	t.Cleanup(func() { SetRetrier(storeRetrier{open: defaultOpenStore}) })

	if err := Retry(context.Background(), Request{TaskID: "t4", Hint: "try X"}); err != nil {
		t.Fatalf("Retry: %v", err)
	}

	tasks, _ := db.QueryTasks(storage.TaskFilter{ID: "t4"})
	if len(tasks) != 1 || tasks[0].State != "needs-fix" || tasks[0].Branch != "agent/t4" {
		t.Errorf("unexpected task after delegated retry: %+v", tasks)
	}
}
