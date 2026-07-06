package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/futureboard-dev/colony/pkg/storage"
	"github.com/spf13/cobra"
)

func TestLoopStatus_Queue(t *testing.T) {
	dir, store, cleanup := setupStatusTest(t)
	defer cleanup()

	now := time.Now().Truncate(time.Second).UTC()
	seed := []storage.Task{
		{ID: "t1", Description: "open task", State: "open", CreatedAt: now},
		{ID: "t2", Description: "needs fix task", State: "needs-fix", LastFeedback: "lint failure", CreatedAt: now.Add(time.Minute)},
		{ID: "t3", Description: "blocked task", State: "blocked", LastFeedback: "compile error", CreatedAt: now.Add(2 * time.Minute)},
		{ID: "t4", Description: "done task", State: "done", CreatedAt: now.Add(3 * time.Minute)},
	}
	for _, tk := range seed {
		if err := store.InsertTask(tk); err != nil {
			t.Fatal(err)
		}
	}

	out := runStatusCmd(t, dir, []string{})

	// Queue section should list open, needs-fix, and blocked tasks (not done).
	if !strings.Contains(out, "t1") {
		t.Error("expected t1 in queue output")
	}
	if !strings.Contains(out, "t2") {
		t.Error("expected t2 in queue output")
	}
	if !strings.Contains(out, "t3") {
		t.Error("expected t3 in queue output")
	}
	if strings.Contains(out, "t4") {
		t.Error("did not expect t4 (done) in queue output")
	}

	// Feedback section should include blocked/needs-fix text.
	if !strings.Contains(out, "lint failure") {
		t.Error("expected 'lint failure' in feedback section")
	}
	if !strings.Contains(out, "compile error") {
		t.Error("expected 'compile error' in feedback section")
	}
}

func TestLoopStatus_StateFilter(t *testing.T) {
	dir, store, cleanup := setupStatusTest(t)
	defer cleanup()

	now := time.Now().Truncate(time.Second).UTC()
	for _, tk := range []storage.Task{
		{ID: "o1", Description: "open one", State: "open", CreatedAt: now},
		{ID: "o2", Description: "open two", State: "open", CreatedAt: now.Add(time.Minute)},
		{ID: "b1", Description: "blocked one", State: "blocked", CreatedAt: now.Add(2 * time.Minute)},
	} {
		if err := store.InsertTask(tk); err != nil {
			t.Fatal(err)
		}
	}

	// Filter to blocked only.
	out := runStatusCmd(t, dir, []string{"--state", "blocked"})

	if !strings.Contains(out, "b1") {
		t.Error("expected b1 in blocked-only output")
	}
	if strings.Contains(out, "o1") {
		t.Error("did not expect o1 in blocked-only output")
	}
	if strings.Contains(out, "o2") {
		t.Error("did not expect o2 in blocked-only output")
	}
}

func TestLoopStatus_JSON(t *testing.T) {
	dir, store, cleanup := setupStatusTest(t)
	defer cleanup()

	now := time.Now().Truncate(time.Second).UTC()
	store.InsertTask(storage.Task{ID: "j1", Description: "json task", State: "open", CreatedAt: now})
	store.InsertTask(storage.Task{ID: "j2", Description: "needs fix", State: "needs-fix", LastFeedback: "test failed", CycleCount: 2, CreatedAt: now.Add(time.Minute)})

	sessID := "loop-j1-20260101-120000"
	store.InsertSession(storage.Session{
		ID: sessID, MissionName: "loop-j1",
		StartedAt: now, Status: "completed",
		FinishedAt: timePtr(now.Add(30 * time.Second)),
	})

	out := runStatusCmd(t, dir, []string{"--json"})

	var parsed struct {
		Queue    []json.RawMessage `json:"queue"`
		Sessions []json.RawMessage `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("expected valid JSON: %v\nraw: %s", err, out)
	}

	if len(parsed.Queue) != 2 {
		t.Errorf("expected 2 queue items, got %d", len(parsed.Queue))
	}
	if len(parsed.Sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(parsed.Sessions))
	}
}

func TestLoopStatus_SessionsDisplay(t *testing.T) {
	dir, store, cleanup := setupStatusTest(t)
	defer cleanup()

	now := time.Now().Truncate(time.Second).UTC()

	// Session with loop- prefix.
	store.InsertSession(storage.Session{
		ID: "loop-t1-20260101-120000", MissionName: "loop-t1",
		StartedAt: now, Status: "completed",
		FinishedAt: timePtr(now.Add(1 * time.Minute)),
	})

	// Session with escalation- prefix.
	store.InsertSession(storage.Session{
		ID: "escalation-t1-20260101-123000", MissionName: "escalation-t1",
		StartedAt: now.Add(30 * time.Minute), Status: "completed",
		FinishedAt: timePtr(now.Add(31 * time.Minute)),
	})

	// Running session (no finished_at).
	store.InsertSession(storage.Session{
		ID: "loop-t2-20260101-130000", MissionName: "loop-t2",
		StartedAt: now.Add(1 * time.Hour), Status: "running",
	})

	// Unrelated session should not appear.
	store.InsertSession(storage.Session{
		ID: "craft-abc", MissionName: "craft-abc",
		StartedAt: now, Status: "completed",
		FinishedAt: timePtr(now.Add(5 * time.Second)),
	})

	out := runStatusCmd(t, dir, []string{})

	if !strings.Contains(out, "loop-t1-20260101-120000") {
		t.Error("expected loop session in output")
	}
	if !strings.Contains(out, "escalation-t1-20260101-123000") {
		t.Error("expected escalation session in output")
	}
	if !strings.Contains(out, "loop-t2-20260101-130000") {
		t.Error("expected running loop session in output")
	}
	if strings.Contains(out, "craft-abc") {
		t.Error("did not expect unrelated session in output")
	}

	// Running session should show '–' for duration.
	if !strings.Contains(out, "–") {
		t.Error("expected dash for running session duration")
	}
}

// ----- helpers -----

func setupStatusTest(t *testing.T) (dir string, store *storage.SQLiteStore, cleanup func()) {
	t.Helper()
	dir = t.TempDir()
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	os.Chdir(dir)

	dbPath := filepath.Join(dir, ".colony", "missions.db")
	s, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	cleanup = func() {
		s.Close()
		os.Chdir(origWd)
	}
	return dir, s, cleanup
}

func runStatusCmd(t *testing.T, dir string, extraArgs []string) string {
	t.Helper()
	var buf bytes.Buffer
	cmd := &cobra.Command{Use: "status", RunE: runLoopStatus}
	cmd.Flags().StringVar(&statusState, "state", "", "")
	cmd.Flags().BoolVar(&statusJSON, "json", false, "")
	cmd.SetOut(&buf)
	cmd.SetArgs(extraArgs)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status command failed: %v", err)
	}
	return buf.String()
}

func timePtr(t time.Time) *time.Time {
	return &t
}

func init() {
	// Ensure no leftover dir changes from other tests.
	_ = exec.Command("git", "init", "--initial-branch=main").Run()
}
