package tui

import (
	"fmt"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/futureboard-dev/colony/pkg/module"
)

// editorFinishedMsg reports the result of a suspended $EDITOR session.
type editorFinishedMsg struct{ err error }

// retryTask re-queues the selected task as open and clears its stale feedback
// so the loop treats the next pass as a fresh attempt.
func (m *Model) retryTask() tea.Cmd {
	sel, ok := m.selectedTask()
	if !ok {
		m.notifier.Push("No task selected", ToastErr)
		return nil
	}
	if sel.State == "open" {
		m.notifier.Push("Task "+sel.ID+" is already open", ToastErr)
		return nil
	}
	return m.setTaskState(sel.ID, "open", "")
}

// blockTask parks the selected task so the loop skips it.
func (m *Model) blockTask() tea.Cmd {
	sel, ok := m.selectedTask()
	if !ok {
		m.notifier.Push("No task selected", ToastErr)
		return nil
	}
	return m.setTaskState(sel.ID, "blocked", sel.LastFeedback)
}

// markTaskDone closes the selected task without running the loop.
func (m *Model) markTaskDone() tea.Cmd {
	sel, ok := m.selectedTask()
	if !ok {
		m.notifier.Push("No task selected", ToastErr)
		return nil
	}
	return m.setTaskState(sel.ID, "done", sel.LastFeedback)
}

// setTaskState writes a state transition and refreshes the snapshot so the
// change is visible before the next poll tick.
func (m *Model) setTaskState(id, state, feedback string) tea.Cmd {
	if m.store == nil {
		m.notifier.Push("Storage unavailable", ToastErr)
		return nil
	}
	if err := m.store.UpdateTaskState(id, state, feedback); err != nil {
		m.notifier.Push("Update failed: "+err.Error(), ToastErr)
		return nil
	}
	m.notifier.Push(id+" → "+state, ToastOK)
	return m.loadOnce()
}

// confirmDeleteTask opens the confirmation modal for a destructive delete.
func (m *Model) confirmDeleteTask() {
	sel, ok := m.selectedTask()
	if !ok {
		m.notifier.Push("No task selected", ToastErr)
		return
	}
	m.BeginConfirm("Delete task "+sel.ID+"?",
		truncate(sel.Description, 120),
		func(m *Model) {
			if m.store == nil {
				m.notifier.Push("Storage unavailable", ToastErr)
				return
			}
			if sel.Branch != "" {
				root := or(m.opts.Root, ".")
				projectName := module.ProjectName(root)
				if err := module.RemoveWorktree(root, projectName, sel.Branch, true); err != nil {
					m.notifier.Push("Worktree cleanup warning: "+err.Error(), ToastErr)
				}
			}
			if err := m.store.DeleteTask(sel.ID); err != nil {
				m.notifier.Push("Delete failed: "+err.Error(), ToastErr)
				return
			}
			m.notifier.Push("Deleted "+sel.ID, ToastOK)
			m.clampCursor()
		})
}

// copySelectedTaskID puts the selected task's ID on the clipboard.
func (m *Model) copySelectedTaskID() {
	sel, ok := m.selectedTask()
	if !ok {
		m.notifier.Push("No task selected", ToastErr)
		return
	}
	m.copyValue(sel.ID, "Task ID")
}

// copySelectedSessionID puts the selected session's ID on the clipboard.
func (m *Model) copySelectedSessionID() {
	sessions := m.filteredSessions()
	if m.cursor >= len(sessions) {
		m.notifier.Push("No session selected", ToastErr)
		return
	}
	m.copyValue(sessions[m.cursor].ID, "Session ID")
}

// copySelectedRunLog puts the selected run's log path on the clipboard.
func (m *Model) copySelectedRunLog() {
	sel, ok := m.selectedRun()
	if !ok {
		m.notifier.Push("No run selected", ToastErr)
		return
	}
	if sel.LogPath == "" {
		m.notifier.Push("Run has no log path", ToastErr)
		return
	}
	m.copyValue(sel.LogPath, "Log path")
}

// copyValue routes a copy through the clipboard fallback chain and reports
// which mechanism carried it.
func (m *Model) copyValue(value, label string) {
	res, err := Clipboard(value)
	if err != nil {
		m.notifier.Push("Copy failed: "+err.Error(), ToastErr)
		return
	}
	m.notifier.Push(fmt.Sprintf("%s copied (%s)", label, res), ToastOK)
}

// editSelectedSpec suspends the TUI and opens the task's spec in $EDITOR.
func (m *Model) editSelectedSpec() tea.Cmd {
	sel, ok := m.selectedTask()
	if !ok {
		m.notifier.Push("No task selected", ToastErr)
		return nil
	}
	if sel.SpecPath == "" {
		m.notifier.Push("Task "+sel.ID+" has no spec file", ToastErr)
		return nil
	}
	if !fileExists(sel.SpecPath) {
		m.notifier.Push("Spec file missing: "+sel.SpecPath, ToastErr)
		return nil
	}
	editor := or(os.Getenv("VISUAL"), os.Getenv("EDITOR"))
	if editor == "" {
		m.notifier.Push("Set $EDITOR to edit specs", ToastErr)
		return nil
	}
	return tea.ExecProcess(exec.Command(editor, sel.SpecPath), func(err error) tea.Msg {
		return editorFinishedMsg{err: err}
	})
}

// clampCursor keeps the cursor inside the list after rows disappear.
func (m *Model) clampCursor() {
	if n := m.listLen(); m.cursor >= n {
		m.cursor = maxInt(0, n-1)
	}
}
