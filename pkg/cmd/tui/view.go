package tui

import (
	"fmt"
	"os"
	"strings"
)

// View renders the current frame: the active view body plus a status bar, with
// the toast stack and any active modal overlay on top.
func (m *Model) View() string {
	if m.tooSmall {
		return fmt.Sprintf("Terminal too small (%dx%d).\nMinimum: 80x24.", m.width, m.height)
	}

	var body string
	switch m.view {
	case ViewDashboard:
		body = m.renderDashboard()
	case ViewQueue:
		body = m.renderQueue()
	case ViewTaskDetail:
		body = m.renderTaskDetail()
	case ViewSessions:
		body = m.renderSessions()
	case ViewLiveOutput:
		body = m.renderLiveOutput()
	}

	status := m.renderStatusBar()
	frame := body + "\n" + status

	// Locked banner layers above content.
	if m.lockBanner {
		frame += "\n" + m.theme.Error.Render("Database locked — retrying…")
	}

	overlays := m.renderOverlays()
	if overlays != "" {
		frame = overlays
	}
	return frame
}

// renderOverlays layers modals (then toasts) above the base frame.
func (m *Model) renderOverlays() string {
	var modalText string
	switch m.modal {
	case ModalAddTask:
		modalText = m.renderAddTaskModal()
	case ModalLoopControl:
		modalText = m.renderLoopControlModal()
	case ModalSchedule:
		modalText = m.renderScheduleModal()
	case ModalConfirm:
		modalText = m.renderConfirmModal()
	case ModalReview:
		modalText = m.renderReviewModal()
	case ModalObserve:
		modalText = m.renderObserveModal()
	case ModalHelp:
		modalText = m.renderHelp()
	}
	out := modalText
	if toasts := m.renderToasts(); toasts != "" {
		if out != "" {
			out += "\n\n"
		}
		out += toasts
	}
	return out
}

// renderToasts stacks visible toasts, newest-first, max 3.
func (m *Model) renderToasts() string {
	all := m.notifier.All()
	if len(all) == 0 {
		return ""
	}
	var lines []string
	for _, t := range all {
		style := m.theme.ToastOK
		if t.Kind == ToastErr {
			style = m.theme.ToastErr
		}
		lines = append(lines, style.Render(t.Message))
	}
	return strings.Join(lines, "\n")
}

// renderStatusBar shows view + context-sensitive keybinds.
func (m *Model) renderStatusBar() string {
	hints := map[View]string{
		ViewDashboard:  "[q]uit  [2] queue  [4]sessions  [a]dd  [l]oop ctrl  [v] live  [?]help",
		ViewQueue:      "[q]uit  [1] dashboard  [Enter] detail  [a]dd  [x]delete  [m]ark done  [f]ilter  [?]help",
		ViewTaskDetail: "[q]uit  [Esc] back  [r]etry  [b]lock  [m]ark done  [x]delete  [v] live  [?]help",
		ViewSessions:   "[q]uit  [1] dashboard  [2] queue  Enter] task  [v] live  [?]help",
		ViewLiveOutput: "[q]uit  [d]ashboard  [s]top  [k]ill  [r]estart  [f]reeze  [?]help",
	}
	title := m.theme.Title.Render(m.view.String())
	hint := m.theme.Help.Render(hints[m.view])
	return title + "\n" + hint
}

// storageTaskRef is a lightweight view-bound copy of a task.
type storageTaskRef struct {
	ID, Description, State, SpecPath, BaseBranch, Lang, LastFeedback string
	CycleCount                                                       int
}

// selectedTask returns the task at the queue cursor, if any.
func (m *Model) selectedTask() (storageTaskRef, bool) {
	tasks := Apply(m.tasks, m.queue)
	if len(tasks) == 0 || m.cursor >= len(tasks) {
		return storageTaskRef{}, false
	}
	t := tasks[m.cursor]
	return storageTaskRef{ID: t.ID, Description: t.Description, State: t.State,
		SpecPath: t.SpecPath, BaseBranch: t.BaseBranch, CycleCount: t.CycleCount,
		Lang: t.Lang, LastFeedback: t.LastFeedback}, true
}

func indentEach(s, indent string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func modalErr(err string) string {
	if err == "" {
		return ""
	}
	return "\n\nError: " + err
}

// fileExists reports whether a path refers to an existing file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// isLockedError inspects a sqlite busy/locked error message.
func isLockedError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "locked") || strings.Contains(msg, "busy") ||
		strings.Contains(msg, "cannot start a transaction")
}
