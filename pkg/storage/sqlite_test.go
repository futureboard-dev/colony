package storage

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSchemaMigrationIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "idem.db")

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

func TestInsertUpdateSessionRoundTrip(t *testing.T) {
	db := openTestDB(t)

	start := time.Now().Truncate(time.Second).UTC()
	sess := Session{
		ID:          "test-mission-20260429-120000",
		MissionName: "test-mission",
		StartedAt:   start,
		Status:      "running",
	}
	if err := db.InsertSession(sess); err != nil {
		t.Fatalf("InsertSession: %v", err)
	}

	finish := start.Add(5 * time.Second)
	if err := db.UpdateSession(sess.ID, "completed", finish); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	sessions, err := db.QuerySessions(SessionFilter{SessionID: sess.ID})
	if err != nil {
		t.Fatalf("QuerySessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	got := sessions[0]
	if got.Status != "completed" {
		t.Errorf("expected status completed, got %s", got.Status)
	}
	if got.FinishedAt == nil {
		t.Error("expected FinishedAt to be set")
	}
	if got.MissionName != "test-mission" {
		t.Errorf("expected mission_name test-mission, got %s", got.MissionName)
	}
}

func TestAuditQueryByMissionName(t *testing.T) {
	db := openTestDB(t)

	for _, id := range []string{"alpha-20260101-000000", "beta-20260101-000000"} {
		name := "alpha"
		if id[0] == 'b' {
			name = "beta"
		}
		_ = db.InsertSession(Session{
			ID: id, MissionName: name,
			StartedAt: time.Now(), Status: "completed",
		})
	}

	sessions, err := db.QuerySessions(SessionFilter{MissionName: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].MissionName != "alpha" {
		t.Errorf("expected 1 alpha session, got %+v", sessions)
	}
}

func TestStepOutputColumnRoundTrip(t *testing.T) {
	db := openTestDB(t)

	sessID := "mission-output"
	_ = db.InsertSession(Session{ID: sessID, MissionName: "m", StartedAt: time.Now(), Status: "running"})

	now := time.Now()
	steps := []Step{
		// Gate REJECTED stores full captured output.
		{SessionID: sessID, StepNum: 1, AgentID: "g1", Role: "gate", Decision: "REJECTED",
			Output: "FAIL: TestRateLimit_Burst\n--- go test ./... ---\n", StartedAt: now, FinishedAt: now},
		// Gate APPROVED stores an empty output string.
		{SessionID: sessID, StepNum: 2, AgentID: "g2", Role: "gate", Decision: "APPROVED", StartedAt: now, FinishedAt: now},
		// LLM step stores nothing by default.
		{SessionID: sessID, StepNum: 3, AgentID: "b1", Role: "builder", Decision: "APPROVED", StartedAt: now, FinishedAt: now},
	}
	for _, s := range steps {
		if err := db.InsertStep(s); err != nil {
			t.Fatalf("InsertStep: %v", err)
		}
	}

	got, err := db.QuerySteps(StepFilter{SessionID: sessID})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(got))
	}
	// First gate step has verbatim output.
	if got[0].Output != steps[0].Output {
		t.Errorf("gate REJECTED output mismatch: got %q want %q", got[0].Output, steps[0].Output)
	}
	// APPROVED gate and builder store empty output.
	if got[1].Output != "" {
		t.Errorf("gate APPROVED should store empty output, got %q", got[1].Output)
	}
	if got[2].Output != "" {
		t.Errorf("builder step should store empty output, got %q", got[2].Output)
	}
}

func TestStepOutputColumnDefaultsToEmpty(t *testing.T) {
	// Verify a step inserted without the Output field defaults to ''.
	db := openTestDB(t)
	sessID := "mission-default"
	_ = db.InsertSession(Session{ID: sessID, MissionName: "m", StartedAt: time.Now(), Status: "running"})
	now := time.Now()
	if err := db.InsertStep(Step{SessionID: sessID, StepNum: 1, AgentID: "a", Role: "gate", Decision: "APPROVED", StartedAt: now, FinishedAt: now}); err != nil {
		t.Fatal(err)
	}
	steps, err := db.QuerySteps(StepFilter{SessionID: sessID})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Output != "" {
		t.Errorf("expected output to default to '', got %q", steps[0].Output)
	}
}

func TestAuditQueryByDecision(t *testing.T) {
	db := openTestDB(t)

	sessID := "mission-20260429-000001"
	_ = db.InsertSession(Session{ID: sessID, MissionName: "m", StartedAt: time.Now(), Status: "running"})

	now := time.Now()
	steps := []Step{
		{SessionID: sessID, StepNum: 1, AgentID: "a1", Role: "r", Decision: "APPROVED", StartedAt: now, FinishedAt: now},
		{SessionID: sessID, StepNum: 2, AgentID: "a2", Role: "r", Decision: "REJECTED", StartedAt: now, FinishedAt: now},
		{SessionID: sessID, StepNum: 3, AgentID: "a3", Role: "r", Decision: "REJECTED", StartedAt: now, FinishedAt: now},
	}
	for _, s := range steps {
		if err := db.InsertStep(s); err != nil {
			t.Fatalf("InsertStep: %v", err)
		}
	}

	rejected, err := db.QuerySteps(StepFilter{SessionID: sessID, Decision: "REJECTED"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 2 {
		t.Errorf("expected 2 REJECTED steps, got %d", len(rejected))
	}
	for _, s := range rejected {
		if s.Decision != "REJECTED" {
			t.Errorf("expected REJECTED, got %s", s.Decision)
		}
	}
}

func TestAuditQueryBySessionID(t *testing.T) {
	db := openTestDB(t)

	for _, sid := range []string{"s1", "s2"} {
		_ = db.InsertSession(Session{ID: sid, MissionName: "m", StartedAt: time.Now(), Status: "running"})
		now := time.Now()
		_ = db.InsertStep(Step{
			SessionID: sid, StepNum: 1, AgentID: "a", Role: "r",
			Decision: "APPROVED", StartedAt: now, FinishedAt: now,
		})
	}

	steps, err := db.QuerySteps(StepFilter{SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].SessionID != "s1" {
		t.Errorf("expected 1 step for s1, got %+v", steps)
	}
}

func TestInsertUpdateRunRoundTrip(t *testing.T) {
	db := openTestDB(t)

	start := time.Now().Truncate(time.Second).UTC()
	run := Run{
		ID: "craft-20260429-023036", Kind: "craft", Project: "colony",
		Language: "go", Model: "claude-opus-4-8", Status: "running",
		LogPath: ".colony/logs/craft-20260429-023036.log", StartedAt: start,
	}
	if err := db.InsertRun(run); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}

	finish := start.Add(90 * time.Second)
	if err := db.UpdateRun(Run{
		ID: run.ID, Status: "complete", Branch: "feat/widget", FinishedAt: &finish,
	}); err != nil {
		t.Fatalf("UpdateRun: %v", err)
	}

	runs, err := db.QueryRuns(RunFilter{Kind: "craft"})
	if err != nil {
		t.Fatalf("QueryRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	got := runs[0]
	if got.Status != "complete" {
		t.Errorf("expected status complete, got %s", got.Status)
	}
	if got.Branch != "feat/widget" {
		t.Errorf("expected branch feat/widget, got %s", got.Branch)
	}
	if got.Language != "go" || got.Model != "claude-opus-4-8" {
		t.Errorf("unexpected language/model: %s / %s", got.Language, got.Model)
	}
	if got.FinishedAt == nil {
		t.Error("expected FinishedAt to be set")
	}
}

func TestQueryRunsFilterByKindAndProject(t *testing.T) {
	db := openTestDB(t)

	now := time.Now()
	seed := []Run{
		{ID: "craft-1", Kind: "craft", Project: "alpha", Status: "complete", StartedAt: now},
		{ID: "swarm-1", Kind: "swarm", Project: "alpha", Mode: "full", Approved: 2, Rejected: 1, Status: "complete", StartedAt: now},
		{ID: "craft-2", Kind: "craft", Project: "beta", Status: "blocked", StartedAt: now},
	}
	for _, r := range seed {
		if err := db.InsertRun(r); err != nil {
			t.Fatalf("InsertRun %s: %v", r.ID, err)
		}
	}

	swarms, err := db.QueryRuns(RunFilter{Kind: "swarm"})
	if err != nil {
		t.Fatal(err)
	}
	if len(swarms) != 1 || swarms[0].Approved != 2 || swarms[0].Rejected != 1 {
		t.Errorf("expected 1 swarm with 2/1 tally, got %+v", swarms)
	}

	alpha, err := db.QueryRuns(RunFilter{Project: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if len(alpha) != 2 {
		t.Errorf("expected 2 alpha runs, got %d", len(alpha))
	}
}

func TestDefaultDBPathEnvOverride(t *testing.T) {
	t.Setenv("COLONY_DB_PATH", "/tmp/override.db")
	if got := DefaultDBPath(); got != "/tmp/override.db" {
		t.Errorf("expected /tmp/override.db, got %s", got)
	}
}

func TestDefaultDBPathDefault(t *testing.T) {
	os.Unsetenv("COLONY_DB_PATH")
	want := filepath.Join(".colony", "missions.db")
	if got := DefaultDBPath(); got != want {
		t.Errorf("expected %s, got %s", want, got)
	}
}

func TestSessionTaskIDRoundTripAndFilter(t *testing.T) {
	db := openTestDB(t)

	now := time.Now()
	for _, s := range []Session{
		{ID: "loop-fix-auth-20260101-000000", MissionName: "loop-fix-auth", TaskID: "t-1", StartedAt: now, Status: "completed"},
		{ID: "escalation-t-1-20260101-010000", MissionName: "escalation-fix-auth", TaskID: "t-1", StartedAt: now, Status: "failed"},
		{ID: "loop-other-20260101-020000", MissionName: "loop-other", TaskID: "t-2", StartedAt: now, Status: "completed"},
		{ID: "standalone-20260101-030000", MissionName: "some-mission", StartedAt: now, Status: "completed"},
	} {
		if err := db.InsertSession(s); err != nil {
			t.Fatalf("InsertSession(%s): %v", s.ID, err)
		}
	}

	t.Run("filters by task id", func(t *testing.T) {
		sessions, err := db.QuerySessions(SessionFilter{TaskID: "t-1"})
		if err != nil {
			t.Fatal(err)
		}
		if len(sessions) != 2 {
			t.Fatalf("expected 2 sessions for t-1, got %d", len(sessions))
		}
		for _, s := range sessions {
			if s.TaskID != "t-1" {
				t.Errorf("session %s has TaskID %q, want t-1", s.ID, s.TaskID)
			}
		}
	})

	t.Run("standalone mission has empty task id", func(t *testing.T) {
		sessions, err := db.QuerySessions(SessionFilter{SessionID: "standalone-20260101-030000"})
		if err != nil {
			t.Fatal(err)
		}
		if len(sessions) != 1 {
			t.Fatalf("expected 1 session, got %d", len(sessions))
		}
		if sessions[0].TaskID != "" {
			t.Errorf("expected empty TaskID, got %q", sessions[0].TaskID)
		}
	})

	t.Run("empty filter returns all", func(t *testing.T) {
		sessions, err := db.QuerySessions(SessionFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(sessions) != 4 {
			t.Errorf("expected 4 sessions, got %d", len(sessions))
		}
	})

	t.Run("deletes by task id", func(t *testing.T) {
		n, err := db.DeleteSessions(SessionFilter{TaskID: "t-2"})
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("expected 1 deleted, got %d", n)
		}
	})
}

func TestSessionTaskIDMigratesLegacyDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")

	// Build a database with the pre-task_id sessions table and a row in it.
	legacy, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE sessions (
		id           TEXT    PRIMARY KEY,
		mission_name TEXT    NOT NULL,
		started_at   DATETIME NOT NULL,
		finished_at  DATETIME,
		status       TEXT    NOT NULL DEFAULT 'running'
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(
		`INSERT INTO sessions (id, mission_name, started_at, status) VALUES (?,?,?,?)`,
		"loop-old-20250101-000000", "loop-old", time.Now().UTC().Format(time.RFC3339), "completed",
	); err != nil {
		t.Fatal(err)
	}
	legacy.Close()

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open on legacy db: %v", err)
	}
	defer db.Close()

	sessions, err := db.QuerySessions(SessionFilter{SessionID: "loop-old-20250101-000000"})
	if err != nil {
		t.Fatalf("QuerySessions after migration: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 legacy session, got %d", len(sessions))
	}
	if sessions[0].TaskID != "" {
		t.Errorf("legacy session should have empty TaskID, got %q", sessions[0].TaskID)
	}
	if sessions[0].MissionName != "loop-old" {
		t.Errorf("legacy mission_name lost: %q", sessions[0].MissionName)
	}
}
