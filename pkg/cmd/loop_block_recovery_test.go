package cmd

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/futureboard-dev/colony/pkg/config"
	"github.com/futureboard-dev/colony/pkg/fix"
	"github.com/futureboard-dev/colony/pkg/mission/graph"
	"github.com/futureboard-dev/colony/pkg/storage"
)

func openBlockTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "missions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// fakeTriager implements fix.Triager with a canned response, and records how
// many times Diagnose was called so tests can assert "one attempt only".
type fakeTriager struct {
	diag  fix.Diagnosis
	err   error
	delay time.Duration
	calls int
}

func (f *fakeTriager) Diagnose(ctx context.Context, req fix.Request) (fix.Diagnosis, error) {
	f.calls++
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	return f.diag, f.err
}

func TestBlockTask_RecordsStuckTool(t *testing.T) {
	store := openBlockTestStore(t)
	task := storage.Task{ID: "t1", Description: "d", State: "running", CreatedAt: time.Now()}
	if err := store.InsertTask(task); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{LLM: config.LLMConfig{Provider: "anthropic"}}
	if err := blockTask(context.Background(), cfg, store, &task, graph.RoleFixer, "cannot find config/schema.yaml", "/tmp/worktree", io.Discard); err != nil {
		t.Fatal(err)
	}

	got := taskState(t, store, "t1")
	if got.State != "blocked" {
		t.Errorf("state = %q, want blocked", got.State)
	}
	if got.BlockTool != string(fix.ToolClaude) {
		t.Errorf("BlockTool = %q, want %q (anthropic provider resolves to claude)", got.BlockTool, fix.ToolClaude)
	}
	if got.LastFeedback != "cannot find config/schema.yaml" {
		t.Errorf("LastFeedback = %q, want the block reason preserved", got.LastFeedback)
	}
}

func TestBlockTask_NilConfigLeavesToolUnset(t *testing.T) {
	store := openBlockTestStore(t)
	task := storage.Task{ID: "t2", Description: "d", State: "running", CreatedAt: time.Now()}
	if err := store.InsertTask(task); err != nil {
		t.Fatal(err)
	}

	if err := blockTask(context.Background(), nil, store, &task, graph.RoleFixer, "merge conflict", "", io.Discard); err != nil {
		t.Fatal(err)
	}

	got := taskState(t, store, "t2")
	if got.State != "blocked" {
		t.Errorf("state = %q, want blocked", got.State)
	}
	if got.BlockTool != "" {
		t.Errorf("BlockTool = %q, want empty when no config is available", got.BlockTool)
	}
}

// A registered triager that fixes the block in its one attempt should let the
// task continue (needs-fix), never reaching "blocked".
func TestBlockTask_TriageFixLetsTaskContinue(t *testing.T) {
	triager := &fakeTriager{diag: fix.Diagnosis{Fixed: true, Change: "created the missing config/schema.yaml"}}
	fix.SetTriager(triager)
	t.Cleanup(func() { fix.SetTriager(nil) })

	store := openBlockTestStore(t)
	task := storage.Task{ID: "t3", Description: "d", State: "running", CreatedAt: time.Now()}
	if err := store.InsertTask(task); err != nil {
		t.Fatal(err)
	}

	if err := blockTask(context.Background(), &config.Config{}, store, &task, graph.RoleFixer, "cannot find config/schema.yaml", "/tmp/worktree", io.Discard); err != nil {
		t.Fatal(err)
	}

	got := taskState(t, store, "t3")
	if got.State != "needs-fix" {
		t.Errorf("state = %q, want needs-fix after a fixing triage", got.State)
	}
	if got.BlockAttempts != 1 {
		t.Errorf("BlockAttempts = %d, want 1 (one attempt consumed)", got.BlockAttempts)
	}
	if triager.calls != 1 {
		t.Errorf("Diagnose called %d times, want exactly 1", triager.calls)
	}
}

// A registered triager that cannot fix the block still only gets one attempt,
// and its explanation is folded into the reason the user sees.
func TestBlockTask_TriageNoFixBlocksWithExplanation(t *testing.T) {
	triager := &fakeTriager{diag: fix.Diagnosis{
		Fixed:       false,
		Explanation: fix.Explanation{Cause: "wrong path in the spec", NextStep: "point the spec at config/schema.yaml"},
	}}
	fix.SetTriager(triager)
	t.Cleanup(func() { fix.SetTriager(nil) })

	store := openBlockTestStore(t)
	task := storage.Task{ID: "t4", Description: "d", State: "running", CreatedAt: time.Now()}
	if err := store.InsertTask(task); err != nil {
		t.Fatal(err)
	}

	if err := blockTask(context.Background(), &config.Config{}, store, &task, graph.RoleFixer, "cannot find config/schema.yaml", "/tmp/worktree", io.Discard); err != nil {
		t.Fatal(err)
	}

	got := taskState(t, store, "t4")
	if got.State != "blocked" {
		t.Errorf("state = %q, want blocked", got.State)
	}
	if got.BlockAttempts != 1 {
		t.Errorf("BlockAttempts = %d, want 1", got.BlockAttempts)
	}
	if !contains(got.LastFeedback, "wrong path in the spec") || !contains(got.LastFeedback, "point the spec at config/schema.yaml") {
		t.Errorf("LastFeedback = %q, want it to carry the triage's cause and next step", got.LastFeedback)
	}
	if triager.calls != 1 {
		t.Errorf("Diagnose called %d times, want exactly 1", triager.calls)
	}
}

// A triager error other than ErrNotConfigured must not stop the task from
// being blocked — it degrades to the plain block path.
func TestBlockTask_TriageErrorStillBlocks(t *testing.T) {
	triager := &fakeTriager{err: errors.New("agent exec failed")}
	fix.SetTriager(triager)
	t.Cleanup(func() { fix.SetTriager(nil) })

	store := openBlockTestStore(t)
	task := storage.Task{ID: "t5", Description: "d", State: "running", CreatedAt: time.Now()}
	if err := store.InsertTask(task); err != nil {
		t.Fatal(err)
	}

	if err := blockTask(context.Background(), &config.Config{}, store, &task, graph.RoleFixer, "gate failed", "/tmp/worktree", io.Discard); err != nil {
		t.Fatal(err)
	}
	got := taskState(t, store, "t5")
	if got.State != "blocked" {
		t.Errorf("state = %q, want blocked", got.State)
	}
}

// --retry-blocked must re-queue with the stored reason plus any hint, in the
// same worktree (branch untouched).
func TestRequeueBlocked_KeepsReasonAndHintSameWorktree(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "missions.db")
	t.Setenv("COLONY_DB_PATH", dbPath) // requeueBlocked's fix.Retry opens the default DB path
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.InsertTask(storage.Task{
		ID: "t6", Description: "d", State: "blocked", Branch: "agent/t6",
		LastFeedback: "cannot find config/schema.yaml", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTaskBlockHint("t6", "it's under config/, not configs/"); err != nil {
		t.Fatal(err)
	}

	n, err := requeueBlocked(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("requeueBlocked returned %d, want 1", n)
	}

	got := taskState(t, store, "t6")
	if got.State != "needs-fix" {
		t.Errorf("state = %q, want needs-fix", got.State)
	}
	if got.Branch != "agent/t6" {
		t.Errorf("branch = %q, want unchanged agent/t6 (same worktree)", got.Branch)
	}
	if !contains(got.LastFeedback, "cannot find config/schema.yaml") || !contains(got.LastFeedback, "it's under config/, not configs/") {
		t.Errorf("LastFeedback = %q, want the reason and the hint both present", got.LastFeedback)
	}
	if got.BlockHint != "it's under config/, not configs/" {
		t.Errorf("BlockHint = %q, want hint preserved on the task row", got.BlockHint)
	}
}

// A non-terminal loop run must never read stdin, with or without a
// registered triager.
func TestBlockTask_NeverReadsStdin(t *testing.T) {
	triager := &fakeTriager{diag: fix.Diagnosis{Fixed: false}}
	fix.SetTriager(triager)
	t.Cleanup(func() { fix.SetTriager(nil) })

	// Stdin's read end has nothing writing to it: if blockTask ever tried to
	// read from it, the read (and the test, via the timeout below) would hang.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close(); _ = r.Close() })
	origStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = origStdin })

	store := openBlockTestStore(t)
	task := storage.Task{ID: "t7", Description: "d", State: "running", CreatedAt: time.Now()}
	if err := store.InsertTask(task); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- blockTask(context.Background(), &config.Config{}, store, &task, graph.RoleFixer, "stuck", "/tmp/worktree", io.Discard)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blockTask hung — likely blocked on a read that should never happen in a non-terminal run")
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// A slow triage attempt on one blocked task must not stall unrelated tasks
// running concurrently in the same parallel loop pass.
func TestBlockTask_UnrelatedTasksKeepRunningDuringTriage(t *testing.T) {
	triager := &fakeTriager{diag: fix.Diagnosis{Fixed: false}, delay: 150 * time.Millisecond}
	fix.SetTriager(triager)
	t.Cleanup(func() { fix.SetTriager(nil) })

	store := openBlockTestStore(t)
	now := time.Now()
	if err := store.InsertTask(storage.Task{ID: "slow-block", Description: "d", State: "open", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertTask(storage.Task{ID: "unrelated", Description: "d", State: "open", CreatedAt: now.Add(time.Millisecond)}); err != nil {
		t.Fatal(err)
	}

	unrelatedDone := make(chan struct{})
	work := func(ctx context.Context, tsk *storage.Task, w io.Writer) error {
		if tsk.ID == "slow-block" {
			return blockTask(ctx, &config.Config{}, store, tsk, graph.RoleFixer, "stuck", "/tmp/worktree", w)
		}
		close(unrelatedDone)
		return store.UpdateTaskState(tsk.ID, "done", "")
	}

	done := make(chan error, 1)
	go func() {
		done <- runLoopParallel(context.Background(), t.TempDir(), store, 2, 100, 2, work)
	}()

	select {
	case <-unrelatedDone:
		// The unrelated task finished without waiting on the slow triage.
	case <-time.After(2 * time.Second):
		t.Fatal("unrelated task never ran — a blocked task's triage must not stop other tasks")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runLoopParallel did not finish")
	}

	got := taskState(t, store, "unrelated")
	if got.State != "done" {
		t.Errorf("unrelated task state = %q, want done", got.State)
	}
	gotSlow := taskState(t, store, "slow-block")
	if gotSlow.State != "blocked" {
		t.Errorf("slow-block task state = %q, want blocked", gotSlow.State)
	}
}
