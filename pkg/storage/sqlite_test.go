package storage

import (
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
