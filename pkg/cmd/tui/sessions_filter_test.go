package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/futureboard-dev/colony/pkg/storage"
)

func filterFixture() []storage.Session {
	now := time.Now()
	return []storage.Session{
		{ID: "loop-fix-login-0000", TaskID: "t-1", Status: "failed", StartedAt: now.Add(-9 * time.Minute), FinishedAt: ptrTime(now.Add(-8 * time.Minute))},
		{ID: "retry-gate-fix-login-0001", TaskID: "t-1", Status: "completed", StartedAt: now.Add(-6 * time.Minute), FinishedAt: ptrTime(now.Add(-1 * time.Minute))},
		{ID: "escalation-t-2-0002", TaskID: "t-2", Status: "running", StartedAt: now.Add(-3 * time.Minute)},
		{ID: "nightly-audit-0003", Status: "completed", StartedAt: now.Add(-1 * time.Minute), FinishedAt: ptrTime(now)},
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestSessionTypeClassification(t *testing.T) {
	cases := map[string]string{
		"loop-fix-login-0000":       "loop",
		"retry-gate-fix-login-0001": "retry-gate",
		"escalation-t-2-0002":       "escalation",
		"nightly-audit-0003":        "mission",
	}
	for id, want := range cases {
		t.Run(want, func(t *testing.T) {
			if got := sessionType(storage.Session{ID: id}); got != want {
				t.Errorf("sessionType(%q) = %q, want %q", id, got, want)
			}
		})
	}
}

func TestApplySessionsFilters(t *testing.T) {
	sessions := filterFixture()

	t.Run("empty filter returns all", func(t *testing.T) {
		if got := ApplySessions(sessions, SessionsFilter{}); len(got) != 4 {
			t.Errorf("expected 4, got %d", len(got))
		}
	})

	t.Run("by type", func(t *testing.T) {
		got := ApplySessions(sessions, SessionsFilter{Type: "retry-gate"})
		if len(got) != 1 || got[0].ID != "retry-gate-fix-login-0001" {
			t.Errorf("unexpected result: %+v", got)
		}
	})

	t.Run("by status", func(t *testing.T) {
		got := ApplySessions(sessions, SessionsFilter{Status: "completed"})
		if len(got) != 2 {
			t.Errorf("expected 2 completed, got %d", len(got))
		}
	})

	t.Run("by task", func(t *testing.T) {
		got := ApplySessions(sessions, SessionsFilter{TaskID: "t-1"})
		if len(got) != 2 {
			t.Fatalf("expected 2 sessions for t-1, got %d", len(got))
		}
		for _, s := range got {
			if s.TaskID != "t-1" {
				t.Errorf("leaked session %s", s.ID)
			}
		}
	})

	t.Run("filters compose", func(t *testing.T) {
		got := ApplySessions(sessions, SessionsFilter{TaskID: "t-1", Status: "failed"})
		if len(got) != 1 || got[0].ID != "loop-fix-login-0000" {
			t.Errorf("unexpected result: %+v", got)
		}
	})

	t.Run("unlinked session excluded by task filter", func(t *testing.T) {
		got := ApplySessions(sessions, SessionsFilter{TaskID: "t-2"})
		if len(got) != 1 || got[0].ID != "escalation-t-2-0002" {
			t.Errorf("unexpected result: %+v", got)
		}
	})
}

func TestApplySessionsSort(t *testing.T) {
	sessions := filterFixture()

	t.Run("started is newest first", func(t *testing.T) {
		got := ApplySessions(sessions, SessionsFilter{Sort: "started"})
		if got[0].ID != "nightly-audit-0003" {
			t.Errorf("expected newest first, got %s", got[0].ID)
		}
	})

	t.Run("duration is longest first", func(t *testing.T) {
		got := ApplySessions(sessions, SessionsFilter{Sort: "duration"})
		if got[0].ID != "retry-gate-fix-login-0001" {
			t.Errorf("expected longest first, got %s", got[0].ID)
		}
	})

	t.Run("status groups alphabetically", func(t *testing.T) {
		got := ApplySessions(sessions, SessionsFilter{Sort: "status"})
		if got[0].Status != "completed" {
			t.Errorf("expected completed first, got %s", got[0].Status)
		}
	})
}

func TestCycleWraps(t *testing.T) {
	if got := cycle(sessionTypes, ""); got != "loop" {
		t.Errorf("first cycle = %q, want loop", got)
	}
	if got := cycle(sessionTypes, "mission"); got != "" {
		t.Errorf("cycle past last = %q, want empty (all)", got)
	}
	if got := cycle(sessionTypes, "bogus"); got != "" {
		t.Errorf("cycle from unknown = %q, want empty (all)", got)
	}
}

func TestSessionFilterKeysCycle(t *testing.T) {
	m := newTestModel(t, ViewSessions, sampleStore())

	m = sendKey(m, "t")
	if m.sessFilt.Type != "loop" {
		t.Errorf("after 't', Type = %q, want loop", m.sessFilt.Type)
	}
	m = sendKey(m, "S")
	if m.sessFilt.Status != "running" {
		t.Errorf("after 'S', Status = %q, want running", m.sessFilt.Status)
	}
	m = sendKey(m, "f")
	if m.sessFilt.Sort != "duration" {
		t.Errorf("after 'f', Sort = %q, want duration", m.sessFilt.Sort)
	}
}

func TestSessionFilterBarShowsActiveValues(t *testing.T) {
	m := newTestModel(t, ViewSessions, sampleStore())
	m = sendKey(m, "t")

	out := render(m)
	if !strings.Contains(out, "loop") {
		t.Error("filter bar should show the active type")
	}
}

func TestSessionTaskScopeToggle(t *testing.T) {
	m := newTestModel(t, ViewSessions, sampleStore())

	m = sendKey(m, "T")
	if m.sessFilt.TaskID == "" {
		t.Fatal("expected scope to be set to the selected session's task")
	}
	scoped := m.sessFilt.TaskID
	if got := m.filteredSessions(); len(got) != 1 || got[0].TaskID != scoped {
		t.Errorf("expected only %s sessions, got %+v", scoped, got)
	}

	m = sendKey(m, "T")
	if m.sessFilt.TaskID != "" {
		t.Errorf("second 'T' should clear the scope, got %q", m.sessFilt.TaskID)
	}
}

func TestSessionTaskScopeRejectsUnlinkedSession(t *testing.T) {
	st := sampleStore()
	st.sessions = []storage.Session{{ID: "nightly-audit-0003", Status: "completed", StartedAt: time.Now()}}
	m := newTestModel(t, ViewSessions, st)

	m = sendKey(m, "T")
	if m.sessFilt.TaskID != "" {
		t.Errorf("unlinked session should not set a scope, got %q", m.sessFilt.TaskID)
	}
}

func TestSessionFilterClampsCursor(t *testing.T) {
	m := newTestModel(t, ViewSessions, sampleStore())
	m.cursor = 1

	// Filtering to a single task must pull the cursor back into range.
	m = sendKey(m, "T")
	if m.cursor >= len(m.filteredSessions()) {
		t.Errorf("cursor %d out of range for %d sessions", m.cursor, len(m.filteredSessions()))
	}
}

func TestSessionsForTaskUsesTaskID(t *testing.T) {
	m := newTestModel(t, ViewSessions, sampleStore())

	if got := m.sessionsForTask("t-1"); len(got) != 1 || got[0].ID != "loop-t-1-0000" {
		t.Errorf("expected the t-1 session, got %+v", got)
	}

	// A legacy session with no TaskID must not match on ID substring.
	m.sessions = []storage.Session{{ID: "loop-t-1-legacy", Status: "completed", StartedAt: time.Now()}}
	if got := m.sessionsForTask("t-1"); len(got) != 0 {
		t.Errorf("unlinked session should not match, got %+v", got)
	}
}
