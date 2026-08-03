package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfirmationRequiredForDestructiveAction(t *testing.T) {
	t.Run("cancel leaves model unchanged", func(t *testing.T) {
		st := sampleStore()
		m := newTestModel(t, ViewQueue, st)
		var actionRan bool
		m.BeginConfirm("Delete task t-1?", "Permanently removes the task.", func(m *Model) {
			actionRan = true
		})
		out, _ := m.Update(key("esc"))
		m2 := out.(*Model)
		if actionRan {
			t.Error("action ran on cancel")
		}
		if len(st.tasks) != 3 {
			t.Errorf("cancel should not mutate tasks, got %d tasks", len(st.tasks))
		}
		if m2.modal != ModalNone {
			t.Errorf("expected modal to close on cancel, got %d", m2.modal)
		}
	})

	t.Run("confirm runs the action", func(t *testing.T) {
		st := sampleStore()
		m := newTestModel(t, ViewQueue, st)
		var actionRan bool
		m.BeginConfirm("Delete task t-1?", "Permanently removes the task.", func(m *Model) {
			actionRan = true
			if err := m.store.DeleteTask("t-1"); err != nil {
				t.Errorf("delete failed: %v", err)
			}
		})
		out, _ := m.Update(key("enter"))
		m2 := out.(*Model)
		if !actionRan {
			t.Error("expected action to run on confirm")
		}
		if len(st.tasks) != 2 {
			t.Errorf("expected task deleted after confirm, got %d tasks", len(st.tasks))
		}
		if m2.modal != ModalNone {
			t.Errorf("expected modal to close after confirm, got %d", m2.modal)
		}
	})
}

func TestAddTaskValidation(t *testing.T) {
	st := sampleStore()
	m := New(Options{Refresh: time.Second, StartView: ViewDashboard}, st)
	m.width, m.height = 120, 40

	t.Run("empty description rejected inline", func(t *testing.T) {
		m.modal = ModalAddTask
		m.addDesc = ""
		m.addSpec = ""
		err := m.addTaskNow()
		if err == nil {
			t.Error("expected empty description to be rejected")
		}
	})

	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.md")
	t.Run("non-existent spec path rejected", func(t *testing.T) {
		m.modal = ModalAddTask
		m.addDesc = "valid task"
		m.addSpec = missing
		err := m.addTaskNow()
		if err == nil {
			t.Error("expected missing spec path to be rejected")
		}
		if len(st.inserted) != 0 {
			t.Error("no task should be inserted on invalid input")
		}
	})

	t.Run("valid add inserts task", func(t *testing.T) {
		path := filepath.Join(dir, "exists.md")
		if err := os.WriteFile(path, []byte("# spec"), 0644); err != nil {
			t.Fatal(err)
		}
		m.addDesc = "a valid task"
		m.addSpec = path
		if err := m.addTaskNow(); err != nil {
			t.Fatalf("expected valid add to succeed: %v", err)
		}
		if len(st.inserted) != 1 {
			t.Errorf("expected 1 inserted task, got %d", len(st.inserted))
		}
		if st.inserted[0].SpecPath != path {
			t.Errorf("spec path not persisted: %q", st.inserted[0].SpecPath)
		}
	})
}

func TestGateFeedbackRendering(t *testing.T) {
	t.Run("pre-migration empty output renders graceful note", func(t *testing.T) {
		got := RenderGateOutput("")
		if got != gateNotAvailable {
			t.Errorf("expected graceful-degradation message, got %q", got)
		}
	})
	t.Run("captured output passes through", func(t *testing.T) {
		got := RenderGateOutput("FAIL: TestX")
		if got != "FAIL: TestX" {
			t.Errorf("expected verbatim output, got %q", got)
		}
	})
}
