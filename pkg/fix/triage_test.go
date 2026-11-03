package fix

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/futureboard-dev/colony/pkg/config"
	"github.com/futureboard-dev/colony/pkg/llm"
	"github.com/futureboard-dev/colony/pkg/storage"
)

// fakeTriageAgent replaces the headless run with fn, and fails the test if
// triage ever opens an interactive session: that would take over a terminal a
// TUI or parallel workers are still using.
func fakeTriageAgent(t *testing.T, fn func(workdir, prompt string) error) {
	t.Helper()
	origRun, origLaunch, origOnPath, origLoad := runTriageAgent, launchInteractive, onPath, loadConfig
	t.Cleanup(func() {
		runTriageAgent, launchInteractive, onPath, loadConfig = origRun, origLaunch, origOnPath, origLoad
	})
	runTriageAgent = func(_ context.Context, _ *llm.Executor, workdir, prompt string, _ io.Writer) error {
		return fn(workdir, prompt)
	}
	launchInteractive = func(*llm.Executor, string, string) error {
		t.Error("triage must not open an interactive session")
		return nil
	}
	onPath = func(cli string) bool { return cli == string(ToolClaude) }
	loadConfig = func() (*config.Config, error) { return nil, errString("no config") }
}

// initTriageWorktree creates a real git repo with one committed file, so
// `git status --porcelain` reflects real changes a fake fix agent makes.
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

	// Fake agent: writes a new file into the worktree to simulate a fix.
	fakeTriageAgent(t, func(workdir, prompt string) error {
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

	calls := 0
	fakeTriageAgent(t, func(workdir, prompt string) error {
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

	calls := 0
	fakeTriageAgent(t, func(workdir, prompt string) error {
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

// A blocked task's worktree is usually already dirty, so a fix to an
// already-modified file must count even though its status line doesn't change.
func TestTriage_FixToAlreadyModifiedFileCounts(t *testing.T) {
	db := openTestStore(t)
	insertBlockedTask(t, db, "t6")
	worktree := initTriageWorktree(t)
	readme := filepath.Join(worktree, "README.md")
	if err := os.WriteFile(readme, []byte("builder's work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fakeTriageAgent(t, func(workdir, prompt string) error {
		return os.WriteFile(readme, []byte("builder's work, fixed\n"), 0o644)
	})

	triager := storeTriager{open: testOpener(db)}
	diag, err := triager.Diagnose(context.Background(), Request{TaskID: "t6", Worktree: worktree})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if !diag.Fixed {
		t.Errorf("Fixed = false, want true for an edit to an already-modified file")
	}
}

// With no worktree, an unattended agent would otherwise be editing the project
// root, so it must not run at all and the attempt stays unspent.
func TestTriage_NoWorktreeSkipsAttempt(t *testing.T) {
	for name, worktree := range map[string]string{"empty": "", "missing": filepath.Join(t.TempDir(), "gone")} {
		t.Run(name, func(t *testing.T) {
			db := openTestStore(t)
			insertBlockedTask(t, db, "t7")

			fakeTriageAgent(t, func(workdir, prompt string) error {
				t.Errorf("agent ran in %q with no task worktree", workdir)
				return nil
			})

			triager := storeTriager{open: testOpener(db)}
			diag, err := triager.Diagnose(context.Background(), Request{TaskID: "t7", Reason: "stuck", Worktree: worktree})
			if err != nil {
				t.Fatalf("Diagnose: %v", err)
			}
			if diag.Fixed {
				t.Error("Fixed = true, want false when nothing ran")
			}
			tasks, _ := db.QueryTasks(storage.TaskFilter{ID: "t7"})
			if len(tasks) != 1 || tasks[0].BlockAttempts != 0 {
				t.Fatalf("BlockAttempts = %+v, want 0", tasks)
			}
		})
	}
}

func TestTriage_PromptCarriesBlockReason(t *testing.T) {
	db := openTestStore(t)
	insertBlockedTask(t, db, "t8")
	worktree := initTriageWorktree(t)

	var got string
	fakeTriageAgent(t, func(workdir, prompt string) error {
		got = prompt
		return nil
	})

	triager := storeTriager{open: testOpener(db)}
	if _, err := triager.Diagnose(context.Background(), Request{TaskID: "t8", Reason: "lint failed: x.go:1", Worktree: worktree}); err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if !strings.Contains(got, "lint failed: x.go:1") || !strings.Contains(got, "unattended") {
		t.Errorf("prompt = %q, want the block reason and the unattended instructions", got)
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

	wantErr := strings.Repeat("boom", 1)
	fakeTriageAgent(t, func(workdir, prompt string) error {
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

	fakeTriageAgent(t, func(workdir, prompt string) error { return nil })

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
