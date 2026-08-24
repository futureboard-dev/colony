package tui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTaskActionsMutateStore(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		want  string
		start string
	}{
		{name: "retry re-opens a needs-fix task", key: "r", want: "open", start: "needs-fix"},
		{name: "block parks a task", key: "b", want: "blocked", start: "open"},
		{name: "mark done closes a task", key: "m", want: "done", start: "open"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := sampleStore()
			m := newTestModel(t, ViewQueue, st)
			focusTask(t, m, "t-1", tc.start)

			m.Update(key(tc.key))

			for _, task := range st.tasks {
				if task.ID == "t-1" && task.State != tc.want {
					t.Errorf("state = %q, want %q", task.State, tc.want)
				}
			}
		})
	}
}

// focusTask puts the queue cursor on the given task ID, optionally forcing its
// starting state. The queue is sorted, so the cursor is not the store index.
func focusTask(t *testing.T, m *Model, id, state string) {
	t.Helper()
	for i := range m.tasks {
		if m.tasks[i].ID == id && state != "" {
			m.tasks[i].State = state
		}
	}
	if st, ok := m.store.(*stubStore); ok {
		for i := range st.tasks {
			if st.tasks[i].ID == id && state != "" {
				st.tasks[i].State = state
			}
		}
	}
	for i, task := range Apply(m.tasks, m.queue) {
		if task.ID == id {
			m.cursor = i
			return
		}
	}
	t.Fatalf("task %s not found in the queue view", id)
}

func TestRetryRejectsAlreadyOpenTask(t *testing.T) {
	m := newTestModel(t, ViewQueue, sampleStore())
	focusTask(t, m, "t-1", "open")

	m.Update(key("r"))

	if got := m.notifier.All(); len(got) == 0 || got[0].Kind != ToastErr {
		t.Error("expected an error toast when retrying an open task")
	}
}

func TestDeleteRequiresConfirmation(t *testing.T) {
	st := sampleStore()
	m := newTestModel(t, ViewQueue, st)
	focusTask(t, m, "t-1", "")
	before := len(st.tasks)

	m.Update(key("x"))
	if m.modal != ModalConfirm {
		t.Fatalf("expected confirm modal, got %d", m.modal)
	}
	if len(st.tasks) != before {
		t.Error("task deleted before confirmation")
	}

	m.Update(key("enter"))
	if len(st.tasks) != before-1 {
		t.Errorf("expected %d tasks after confirm, got %d", before-1, len(st.tasks))
	}
}

func TestTaskActionsReportStoreErrors(t *testing.T) {
	st := sampleStore()
	m := newTestModel(t, ViewQueue, st)
	focusTask(t, m, "t-1", "")
	st.err = errors.New("database is locked")

	m.Update(key("m"))

	toasts := m.notifier.All()
	if len(toasts) == 0 || toasts[0].Kind != ToastErr {
		t.Fatal("expected an error toast when the store fails")
	}
}

func TestActionsWithoutSelectionAreSafe(t *testing.T) {
	st := &stubStore{}
	m := newTestModel(t, ViewQueue, st)
	m.tasks = nil

	// None of these may panic or mutate when the queue is empty.
	for _, k := range []string{"r", "b", "m", "x", "y"} {
		m.Update(key(k))
	}
	if len(st.inserted) != 0 || len(st.tasks) != 0 {
		t.Error("empty-queue actions mutated the store")
	}
}

func TestEditSpecRequiresSpecAndEditor(t *testing.T) {
	st := sampleStore()
	dir := t.TempDir()
	spec := filepath.Join(dir, "TASK.md")
	if err := os.WriteFile(spec, []byte("# spec"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Run("no spec path reports an error", func(t *testing.T) {
		m := newTestModel(t, ViewQueue, st)
		focusTask(t, m, "t-1", "")
		m.tasks[0].SpecPath = ""
		if cmd := m.editSelectedSpec(); cmd != nil {
			t.Error("expected no editor command for a task without a spec")
		}
	})

	t.Run("missing $EDITOR reports an error", func(t *testing.T) {
		t.Setenv("EDITOR", "")
		t.Setenv("VISUAL", "")
		m := newTestModel(t, ViewQueue, st)
		focusTask(t, m, "t-1", "")
		for i := range m.tasks {
			m.tasks[i].SpecPath = spec
		}
		if cmd := m.editSelectedSpec(); cmd != nil {
			t.Error("expected no editor command when $EDITOR is unset")
		}
	})

	t.Run("spec plus editor produces a command", func(t *testing.T) {
		t.Setenv("EDITOR", "true")
		m := newTestModel(t, ViewQueue, st)
		focusTask(t, m, "t-1", "")
		for i := range m.tasks {
			m.tasks[i].SpecPath = spec
		}
		if cmd := m.editSelectedSpec(); cmd == nil {
			t.Error("expected an editor command")
		}
	})
}

func TestSessionCopyUsesSessionID(t *testing.T) {
	st := sampleStore()
	m := newTestModel(t, ViewSessions, st)
	m.cursor = 0
	if len(m.sessions) == 0 {
		t.Skip("sample store has no sessions")
	}

	m.Update(key("y"))

	// Either outcome is fine in CI (no tmux, no TTY); what matters is that the
	// key is consumed and reported rather than silently ignored.
	if len(m.notifier.All()) == 0 {
		t.Error("expected a toast reporting the copy outcome")
	}
}
