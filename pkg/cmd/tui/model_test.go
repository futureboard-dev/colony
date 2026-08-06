package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/futureboard-dev/colony/pkg/storage"
)

// newTestModel builds a model with a stubbed store and a given start view,
// preloading any tasks/sessions the stub holds.
func newTestModel(t *testing.T, start View, st *stubStore) *Model {
	t.Helper()
	opts := Options{Refresh: time.Second, StartView: start}
	if st == nil {
		st = &stubStore{}
	}
	m := New(opts, st)
	m.width, m.height = 120, 40
	if tasks, err := st.QueryTasks(storage.TaskFilter{}); err == nil {
		m.tasks = tasks
	}
	if sessions, err := st.QuerySessions(storage.SessionFilter{}); err == nil {
		m.sessions = sessions
	}
	if steps, err := st.QuerySteps(storage.StepFilter{}); err == nil {
		m.steps = steps
	}
	return m
}

// sendKey resizes (once) then sends a key and returns the resulting model.
func sendKey(m *Model, s string) *Model {
	out, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	_ = out
	out2, _ := m.Update(key(s))
	return out2.(*Model)
}

// render returns the model's current View output as plain text.
func render(m *Model) string {
	return m.View()
}

func sampleStore() *stubStore {
	now := time.Now()
	return &stubStore{
		tasks: []storage.Task{
			{ID: "t-1", Description: "fix login", State: "needs-fix", CycleCount: 2, Lang: "go", Branch: "colony/t-1", CreatedAt: now.Add(-8 * time.Minute)},
			{ID: "t-2", Description: "add rate limiter", State: "blocked", CycleCount: 3, Lang: "go", CreatedAt: now.Add(-4 * time.Minute)},
			{ID: "t-3", Description: "update docs", State: "done", CycleCount: 0, Lang: "go", CreatedAt: now.Add(-16 * time.Minute)},
		},
		sessions: []storage.Session{
			{ID: "loop-t-1-0000", Status: "failed", StartedAt: now.Add(-2 * time.Minute)},
			{ID: "loop-t-2-0000", Status: "completed", StartedAt: now.Add(-1 * time.Minute)},
		},
		steps: []storage.Step{
			{ID: 1, SessionID: "loop-t-1-0000", StepNum: 1, Role: "gate", Decision: "REJECTED", Output: "FAIL: TestLogin\n"},
			{ID: 2, SessionID: "loop-t-1-0000", StepNum: 2, Role: "builder", Decision: "APPROVED"},
		},
		runs: []storage.Run{
			{ID: "run-1", Kind: "craft", Project: "colony", Status: "complete", Approved: 3, Rejected: 1,
				LogPath: "/tmp/run-1.log", StartedAt: now.Add(-30 * time.Minute)},
			{ID: "run-2", Kind: "swarm", Project: "colony", Status: "running", Approved: 0, Rejected: 2,
				StartedAt: now.Add(-5 * time.Minute)},
		},
	}
}

func TestModelRendersEachView(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		title string
	}{
		{"dashboard", "1", "Dashboard"},
		{"queue", "2", "Queue"},
		{"task detail", "3", "Task Detail"},
		{"sessions", "4", "Sessions"},
		{"live output", "5", "Live Output"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t, ViewDashboard, sampleStore())
			m = sendKey(m, tc.key)
			out := render(m)
			if !strings.Contains(out, tc.title) {
				t.Errorf("view %q did not render marker %q; got:\n%s", tc.name, tc.title, out)
			}
		})
	}
}

func TestModelRoutingNumberAndLetter(t *testing.T) {
	m := newTestModel(t, ViewDashboard, sampleStore())
	// letter 'd' routes to queue per Explicit Decisions; number keys too.
	m = sendKey(m, "d")
	if m.view != ViewQueue {
		t.Errorf("expected 'd' to route to Queue, got %s", m.view)
	}
	m2 := newTestModel(t, ViewDashboard, sampleStore())
	m2 = sendKey(m2, "3")
	if m2.view != ViewTaskDetail {
		t.Errorf("expected '3' to route to Task Detail, got %s", m2.view)
	}
}

func TestModelKeyDispatch(t *testing.T) {
	t.Run("q quits", func(t *testing.T) {
		m := newTestModel(t, ViewDashboard, sampleStore())
		_, cmd := m.Update(key("q"))
		if cmd == nil {
			t.Error("expected a quit command from 'q'")
		}
	})
	t.Run("help opens and is view-aware", func(t *testing.T) {
		m := newTestModel(t, ViewQueue, sampleStore())
		m = sendKey(m, "?")
		if m.modal != ModalHelp {
			t.Fatalf("expected help modal, got %d", m.modal)
		}
		out := render(m)
		if !strings.Contains(out, "Queue view") {
			t.Errorf("help was not view-aware: %s", out)
		}
	})
	t.Run("enter drills into task detail", func(t *testing.T) {
		m := newTestModel(t, ViewQueue, sampleStore())
		m = sendKey(m, "enter")
		if m.view != ViewTaskDetail {
			t.Errorf("expected Enter to drill into Task Detail, got %s", m.view)
		}
	})
	t.Run("esc closes modal", func(t *testing.T) {
		m := newTestModel(t, ViewQueue, sampleStore())
		m = sendKey(m, "?")
		m = sendKey(m, "esc")
		if m.modal != ModalNone {
			t.Errorf("expected modal closed, got %d", m.modal)
		}
	})
	t.Run("g and G move cursor", func(t *testing.T) {
		m := newTestModel(t, ViewQueue, sampleStore())
		m = sendKey(m, "G")
		if m.cursor != 2 {
			t.Errorf("expected G to move to last row (2), got %d", m.cursor)
		}
		m2 := sendKey(m, "g")
		if m2.cursor != 0 {
			t.Errorf("expected g to move to top, got %d", m2.cursor)
		}
	})
}

func TestWindowTooSmallOverlay(t *testing.T) {
	m := newTestModel(t, ViewDashboard, sampleStore())
	out, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 10})
	m2 := out.(*Model)
	if !m2.tooSmall {
		t.Fatal("expected tooSmall to be set below 80x24")
	}
	rendered := m2.View()
	if !strings.Contains(rendered, "Terminal too small") {
		t.Errorf("expected terminal-too-small overlay, got: %s", rendered)
	}

	// Resize back above minimum restores the current view.
	out3, _ := m2.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m3 := out3.(*Model)
	if m3.tooSmall {
		t.Fatal("expected tooSmall cleared after resize")
	}
	if !strings.Contains(m3.View(), "Dashboard") {
		t.Errorf("expected prior view restored after resize, got: %s", m3.View())
	}
}

func TestMonochromeDisablesColor(t *testing.T) {
	m := newTestModel(t, ViewDashboard, sampleStore())
	m.theme = DefaultTheme(true)
	if !m.theme.Monochrome {
		t.Fatal("expected monochrome theme")
	}
	out := render(m)
	if strings.Contains(out, "\x1b[") {
		t.Errorf("monochrome output should not contain ANSI color escapes: %s", out)
	}
}
