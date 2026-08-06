package tui

import (
	"os"
	"path/filepath"
	"strings"
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

	t.Run("empty description and spec rejected inline", func(t *testing.T) {
		m.modal = ModalAddTask
		m.addDesc = ""
		m.addSpec = ""
		m.addLang = "go"
		err := m.addTaskNow()
		if err == nil {
			t.Error("expected empty description to be rejected")
		}
	})

	t.Run("missing lang rejected", func(t *testing.T) {
		m.modal = ModalAddTask
		m.addDesc = "valid task"
		m.addSpec = ""
		m.addLang = ""
		if err := m.addTaskNow(); err == nil {
			t.Error("expected missing lang to be rejected")
		}
		if len(st.inserted) != 0 {
			t.Error("no task should be inserted on invalid input")
		}
	})

	t.Run("unknown lang rejected", func(t *testing.T) {
		m.modal = ModalAddTask
		m.addDesc = "valid task"
		m.addLang = "cobol"
		if err := m.addTaskNow(); err == nil {
			t.Error("expected unknown lang to be rejected")
		}
		if len(st.inserted) != 0 {
			t.Error("no task should be inserted on invalid input")
		}
	})

	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.md")
	t.Run("non-existent spec path rejected", func(t *testing.T) {
		m.modal = ModalAddTask
		m.addDesc = "valid task"
		m.addSpec = missing
		m.addLang = "go"
		err := m.addTaskNow()
		if err == nil {
			t.Error("expected missing spec path to be rejected")
		}
		if len(st.inserted) != 0 {
			t.Error("no task should be inserted on invalid input")
		}
	})

	t.Run("valid add persists every field", func(t *testing.T) {
		path := filepath.Join(dir, "exists.md")
		if err := os.WriteFile(path, []byte("# spec"), 0644); err != nil {
			t.Fatal(err)
		}
		m.addDesc = "a valid task"
		m.addSpec = path
		m.addBase = "hotfix/worker-initial-notes-sanitize"
		m.addLang = "typescript"
		m.addNoFormat = true
		if err := m.addTaskNow(); err != nil {
			t.Fatalf("expected valid add to succeed: %v", err)
		}
		if len(st.inserted) != 1 {
			t.Fatalf("expected 1 inserted task, got %d", len(st.inserted))
		}
		got := st.inserted[0]
		if got.SpecPath != path {
			t.Errorf("spec path not persisted: %q", got.SpecPath)
		}
		if got.BaseBranch != "hotfix/worker-initial-notes-sanitize" {
			t.Errorf("base branch not persisted: %q", got.BaseBranch)
		}
		if got.Lang != "typescript" {
			t.Errorf("lang not persisted: %q", got.Lang)
		}
		if got.GateOverrides != "format" {
			t.Errorf("no-format not persisted as gate override: %q", got.GateOverrides)
		}
	})

	t.Run("spec-only task is accepted", func(t *testing.T) {
		st2 := sampleStore()
		m2 := New(Options{Refresh: time.Second, StartView: ViewDashboard}, st2)
		path := filepath.Join(dir, "exists.md")
		m2.addDesc = ""
		m2.addSpec = path
		m2.addLang = "go"
		if err := m2.addTaskNow(); err != nil {
			t.Fatalf("expected spec-only add to succeed: %v", err)
		}
		if len(st2.inserted) != 1 {
			t.Errorf("expected 1 inserted task, got %d", len(st2.inserted))
		}
	})
}

func TestAddTaskNoFormatToggle(t *testing.T) {
	m := newTestModel(t, ViewDashboard, sampleStore())
	m.Update(key("a"))

	for range addNoFormatField {
		m.Update(key("tab"))
	}
	if m.addFocus != addNoFormatField {
		t.Fatalf("expected focus on no-format field, got %d", m.addFocus)
	}

	m.Update(key(" "))
	if !m.addNoFormat {
		t.Error("expected space to enable no-format")
	}
	m.Update(key(" "))
	if m.addNoFormat {
		t.Error("expected space to toggle no-format back off")
	}

	// Text keys on the toggle field must not panic or leak into an input.
	m.Update(key("x"))
	if m.addDesc != "" {
		t.Errorf("toggle field consumed text into description: %q", m.addDesc)
	}
}

func TestAddTaskModalFitsMinimumTerminal(t *testing.T) {
	m := newTestModel(t, ViewDashboard, sampleStore())
	m.width, m.height = minWidth, minHeight
	m.Update(key("a"))
	m.addErr = "language is required (typescript, python, go)"

	got := strings.Count(m.renderAddTaskModal(minWidth, minHeight), "\n") + 1
	if got > minHeight {
		t.Errorf("Add Task modal is %d rows, exceeds the %d-row minimum terminal", got, minHeight)
	}
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
