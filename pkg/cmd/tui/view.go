package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Frame chrome heights.
const (
	tabBarHeight = 1
	statusHeight = 2
	minWidth     = 80
	minHeight    = 24
)

// View renders one full frame: a tab bar, the active view's panes sized to the
// remaining space, and a pinned status bar — with modals and toasts composited
// on top of that frame rather than replacing it.
func (m *Model) View() string {
	w, h := m.frameSize()

	if m.tooSmall {
		return m.renderTooSmall(w, h)
	}

	bodyH := h - tabBarHeight - statusHeight
	banner := ""
	if m.lockBanner {
		banner = m.theme.Banner.Render(fitLine("  "+m.theme.icon("✗")+" Database locked — retrying…", w))
		bodyH--
	}
	if bodyH < 1 {
		bodyH = 1
	}

	frame := joinV(
		m.renderTabBar(w),
		banner,
		m.renderBody(w, bodyH),
		m.renderStatusBar(w),
	)

	if box := m.renderModal(w, h); box != "" {
		frame = overlayCenter(frame, box, w, h)
	}
	return m.overlayToasts(frame, w)
}

// frameSize returns the drawable size, falling back to the 80x24 minimum before
// the first WindowSizeMsg arrives.
func (m *Model) frameSize() (int, int) {
	w, h := m.width, m.height
	if w <= 0 {
		w = minWidth
	}
	if h <= 0 {
		h = minHeight
	}
	return w, h
}

// renderTooSmall centers the minimum-size notice.
func (m *Model) renderTooSmall(w, h int) string {
	msg := []string{
		m.theme.Error.Render("Terminal too small (" + itoa(m.width) + "x" + itoa(m.height) + ")."),
		m.theme.Dim.Render("Minimum: 80x24."),
	}
	blank := strings.Repeat("\n", maxInt(0, h/2-1))
	var out strings.Builder
	out.WriteString(blank)
	for _, line := range msg {
		pad := maxInt(0, (w-ansi.StringWidth(line))/2)
		out.WriteString(strings.Repeat(" ", pad) + line + "\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

// renderBody dispatches to the active view's renderer.
func (m *Model) renderBody(w, h int) string {
	switch m.view {
	case ViewDashboard:
		return m.renderDashboard(w, h)
	case ViewQueue:
		return m.renderQueue(w, h)
	case ViewTaskDetail:
		return m.renderTaskDetail(w, h)
	case ViewSessions:
		return m.renderSessions(w, h)
	case ViewLiveOutput:
		return m.renderLiveOutput(w, h)
	}
	return strings.Repeat("\n", h-1)
}

// tabs lists the views in number-key order.
var tabs = []View{ViewDashboard, ViewQueue, ViewTaskDetail, ViewSessions, ViewLiveOutput}

// renderTabBar draws the view switcher. The active view is bracketed so it
// reads as selected even without color, and the loop status sits on the right.
func (m *Model) renderTabBar(w int) string {
	var left strings.Builder
	for i, v := range tabs {
		label := itoa(i+1) + " " + v.String()
		if v == m.view {
			left.WriteString(m.theme.TabActive.Render("[" + label + "]"))
		} else {
			left.WriteString(m.theme.TabInactive.Render(" " + label + " "))
		}
		left.WriteString(" ")
	}

	label, _ := m.loopState()
	right := m.theme.LoopStatusStyle(label).Render(
		m.theme.icon(LoopStatusIcon(label)) + " " + strings.ToUpper(label))

	gap := w - ansi.StringWidth(left.String()) - ansi.StringWidth(right) - 1
	if gap < 1 {
		return fitLine(left.String(), w)
	}
	return left.String() + strings.Repeat(" ", gap) + right + " "
}

// statusHints are the context-sensitive keybinds per view, as two lines.
var statusHints = map[View][2]string{
	ViewDashboard: {
		"[q]uit  [Enter] queue  [s]essions  [a]dd task  [l]oop ctrl  [o]bserve",
		"[r]eview  [v] live output  [?]help",
	},
	ViewQueue: {
		"[q]uit  [d]ashboard  [Enter] detail  [r]etry  [x]delete  [c]lose  [b]lock",
		"[m]ark done  [e]dit spec  [s]essions  [f]ilter  [R]eview  [?]help",
	},
	ViewTaskDetail: {
		"[q]uit  [Esc] back  [r]etry  [b]lock  [m]ark done  [x]delete  [e]dit spec",
		"[R]eview  [s]essions  [y] copy id  [v] live output  [?]help",
	},
	ViewSessions: {
		"[q]uit  [d]ashboard  [Enter] task  [v] log tail  [t]ask filter  [R]eview",
		"[c]opy session id  [?]help",
	},
	ViewLiveOutput: {
		"[s]top after current  [k]ill (SIGTERM)  [r]estart  [f]reeze scroll",
		"[d]ashboard  [q]uit  [C]lear output  [w]rap lines  [?]help",
	},
}

// renderStatusBar draws the always-visible bottom bar, highlighting the key
// glyphs inside brackets.
func (m *Model) renderStatusBar(w int) string {
	hints := statusHints[m.view]
	return fitLine(m.styleHints(hints[0]), w) + "\n" + fitLine(m.styleHints(hints[1]), w)
}

// styleHints emphasises the "[k]" portions of a keybind hint line.
func (m *Model) styleHints(s string) string {
	var out strings.Builder
	out.WriteString(" ")
	for {
		open := strings.Index(s, "[")
		if open < 0 {
			break
		}
		closeIdx := strings.Index(s[open:], "]")
		if closeIdx < 0 {
			break
		}
		closeIdx += open
		out.WriteString(m.theme.StatusText.Render(s[:open]))
		out.WriteString(m.theme.StatusKey.Render(s[open : closeIdx+1]))
		s = s[closeIdx+1:]
	}
	out.WriteString(m.theme.StatusText.Render(s))
	return out.String()
}

// renderModal returns the active modal's box, sized relative to the frame.
func (m *Model) renderModal(w, h int) string {
	switch m.modal {
	case ModalAddTask:
		return m.renderAddTaskModal(w, h)
	case ModalLoopControl:
		return m.renderLoopControlModal(w, h)
	case ModalSchedule:
		return m.renderScheduleModal(w, h)
	case ModalConfirm:
		return m.renderConfirmModal(w, h)
	case ModalReview:
		return m.renderReviewModal(w, h)
	case ModalObserve:
		return m.renderObserveModal(w, h)
	case ModalHelp:
		return m.renderHelp(w, h)
	}
	return ""
}

// overlayToasts floats the toast stack against the top-right corner. Each box
// is placed on its own so the padding around a narrow toast doesn't blank out
// the content behind it.
func (m *Model) overlayToasts(frame string, frameW int) string {
	all := m.notifier.All()
	if len(all) == 0 {
		return frame
	}
	maxW := minInt(48, frameW-6)
	y := tabBarHeight
	for _, t := range all {
		style := m.theme.ToastOK
		icon := m.theme.icon(iconApproved)
		if t.Kind == ToastErr {
			style, icon = m.theme.ToastErr, m.theme.icon(iconCrashed)
		}
		text := icon + " " + t.Message
		boxW := minInt(maxW, ansi.StringWidth(text)+4)
		box := m.toastBox(style, text, boxW)

		frame = overlay(frame, box, maxInt(0, frameW-boxW-2), y)
		y += 3
	}
	return frame
}

// toastBox draws a single-line bordered toast.
func (m *Model) toastBox(style lipgloss.Style, text string, w int) string {
	b := m.theme.borders
	inner := w - 2
	top := style.Render(b.TL + strings.Repeat(b.H, inner) + b.TR)
	mid := style.Render(b.V) + style.Render(fitLine(" "+text, inner)) + style.Render(b.V)
	bot := style.Render(b.BL + strings.Repeat(b.H, inner) + b.BR)
	return top + "\n" + mid + "\n" + bot
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

// loopState resolves the loop label and PID, tolerating an unset colony dir.
func (m *Model) loopState() (string, int) {
	if m.opts.ColonyDir == "" {
		return "idle", 0
	}
	return LoopState(m.opts.ColonyDir)
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
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
