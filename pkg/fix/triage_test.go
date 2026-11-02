package fix

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/futureboard-dev/colony/pkg/llm"
	"github.com/futureboard-dev/colony/pkg/storage"
)

// fakeLaunch adapts a workdir/prompt-only fake into launchInteractive's
// real signature, so tests never shell out to a real agent CLI.
func fakeLaunch(fn func(workdir, prompt string) error) func(ex *llm.Executor, workdir, prompt string) error {
	return func(ex *llm.Executor, workdir, prompt string) error { return fn(workdir, prompt) }
}

// initTriageWorktree creates a real git repo with one committed file, so
// `git status --porcelain` reflects real changes a fake fix session makes.
func initTriageWorktree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
	run("init", "--initial-branch=main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "init")
	return dir
}

func insertBlockedTask(t *testing.T, db storage.Store, id string) {
	t.Helper()
	now := time.Now().Truncate(time.Second).UTC()
	if err := db.InsertTask(storage.Task{
		ID: id, Description: "fix thing", State: "blocked",
		LastFeedback: "cannot find config/schema.yaml", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTriage_FixableBlockRetriesOnceAndReportsChange(t *testing.T) {
	db := openTestStore(t)
	insertBlockedTask(t, db, "t1")
	worktree := initTriageWorktree(t)

	orig := launchInteractive
	defer func() { launchInteractive = orig }()
	SetSession(&agentSession{})

	// Fake session: writes a new file into the worktree to simulate a fix.
	launchInteractive = fakeLaunch(func(workdir, prompt string) error {
		return os.WriteFile(filepath.Join(workdir, "fixed.txt"), []byte("fix\n"), 0o644)
	})

	triager := storeTriager{open: testOpener(db)}
	req := Request{TaskID: "t1", Reason: "cannot find config/schema.yaml", Worktree: worktree}

	diag, err := triager.Diagnose(context.Background(), req)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if !diag.Fixed {
		t.Fatalf("Fixed = false, want true: %+v", diag)
	}
	if !strings.Contains(diag.Change, "fixed.txt") {
		t.Errorf("Change = %q, want mention of fixed.txt", diag.Change)
	}

	tasks, _ := db.QueryTasks(storage.TaskFilter{ID: "t1"})
	if len(tasks) != 1 || tasks[0].BlockAttempts != 1 {
		t.Fatalf("BlockAttempts = %+v, want 1", tasks)
	}
}

func TestTriage_DecisionBlockReturnsDiagnosisWithoutRetrying(t *testing.T) {
	db := openTestStore(t)
	insertBlockedTask(t, db, "t2")
	worktree := initTriageWorktree(t)

	orig := launchInteractive
	defer func() { launchInteractive = orig }()
	SetSession(&agentSession{})

	calls := 0
	launchInteractive = fakeLaunch(func(workdir, prompt string) error {
		calls++
		return nil // no changes made: a real decision, not a simple fix.
	})

	triager := storeTriager{open: testOpener(db)}
	req := Request{TaskID: "t2", Reason: "review rejected: needs redesign", Worktree: worktree}

	diag, err := triager.Diagnose(context.Background(), req)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if diag.Fixed {
		t.Errorf("Fixed = true, want false for a decision block")
	}
	if diag.Explanation.Cause == "" {
		t.Errorf("Explanation.Cause empty, want a diagnosis attached")
	}
	if calls != 1 {
		t.Errorf("session opened %d times, want exactly 1", calls)
	}

	tasks, _ := db.QueryTasks(storage.TaskFilter{ID: "t2"})
	if len(tasks) != 1 || tasks[0].BlockAttempts != 1 {
		t.Fatalf("BlockAttempts = %+v, want 1", tasks)
	}
}

func TestTriage_SecondBlockOnSameTaskDoesNotTriageAgain(t *testing.T) {
	db := openTestStore(t)
	insertBlockedTask(t, db, "t3")
	worktree := initTriageWorktree(t)

	orig := launchInteractive
	defer func() { launchInteractive = orig }()
	SetSession(&agentSession{})

	calls := 0
	launchInteractive = fakeLaunch(func(workdir, prompt string) error {
		calls++
		return os.WriteFile(filepath.Join(workdir, "fixed.txt"), []byte("fix\n"), 0o644)
	})

	triager := storeTriager{open: testOpener(db)}
	req := Request{TaskID: "t3", Reason: "cannot find config/schema.yaml", Worktree: worktree}

	if _, err := triager.Diagnose(context.Background(), req); err != nil {
		t.Fatalf("first Diagnose: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls after first Diagnose = %d, want 1", calls)
	}

	// Second block on the same task (attempts already recorded): must not
	// open another session.
	diag, err := triager.Diagnose(context.Background(), req)
	if err != nil {
		t.Fatalf("second Diagnose: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls after second Diagnose = %d, want still 1 (no retriage)", calls)
	}
	if diag.Fixed {
		t.Errorf("second Diagnose Fixed = true, want false (no new attempt made)")
	}
	if diag.Explanation.Cause == "" {
		t.Errorf("second Diagnose Explanation.Cause empty, want a diagnosis attached")
	}

	tasks, _ := db.QueryTasks(storage.TaskFilter{ID: "t3"})
	if len(tasks) != 1 || tasks[0].BlockAttempts != 1 {
		t.Fatalf("BlockAttempts = %+v, want still 1 after second Diagnose", tasks)
	}
}

func TestTriage_UnknownTaskErrors(t *testing.T) {
	db := openTestStore(t)
	triager := storeTriager{open: testOpener(db)}
	_, err := triager.Diagnose(context.Background(), Request{TaskID: "missing"})
	if err == nil {
		t.Fatal("expected error for unknown task")
	}
}

func TestTriage_SessionErrorPropagates(t *testing.T) {
	db := openTestStore(t)
	insertBlockedTask(t, db, "t4")
	worktree := initTriageWorktree(t)

	orig := launchInteractive
	defer func() { launchInteractive = orig }()
	SetSession(&agentSession{})

	wantErr := strings.Repeat("boom", 1)
	launchInteractive = fakeLaunch(func(workdir, prompt string) error {
		return errString(wantErr)
	})

	triager := storeTriager{open: testOpener(db)}
	req := Request{TaskID: "t4", Worktree: worktree}
	_, err := triager.Diagnose(context.Background(), req)
	if err == nil {
		t.Fatal("expected error to propagate from a failed session")
	}

	// The attempt still counts against the one-per-block cap even though
	// the session itself failed.
	tasks, _ := db.QueryTasks(storage.TaskFilter{ID: "t4"})
	if len(tasks) != 1 || tasks[0].BlockAttempts != 1 {
		t.Fatalf("BlockAttempts = %+v, want 1", tasks)
	}
}

// TestDiagnose_DelegatesToRegisteredTriager exercises the package-level
// helper against the same storeTriager.
func TestDiagnose_DelegatesToRegisteredTriager(t *testing.T) {
	db := openTestStore(t)
	insertBlockedTask(t, db, "t5")
	worktree := initTriageWorktree(t)

	orig := launchInteractive
	defer func() { launchInteractive = orig }()
	SetSession(&agentSession{})
	launchInteractive = fakeLaunch(func(workdir, prompt string) error { return nil })

	SetTriager(storeTriager{open: testOpener(db)})
	t.Cleanup(func() { SetTriager(storeTriager{open: defaultOpenStore}) })

	diag, err := Diagnose(context.Background(), Request{TaskID: "t5", Worktree: worktree})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if diag.Fixed {
		t.Errorf("Fixed = true, want false (no change made)")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
