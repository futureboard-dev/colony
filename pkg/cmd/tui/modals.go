package tui

import (
	"fmt"
	"strings"
)

// modalWidth sizes a modal as a fraction of the frame, within sane bounds.
func modalWidth(frameW, preferred int) int {
	return clamp(preferred, 40, maxInt(40, frameW-8))
}

// renderAddTaskModal renders the Add Task form with focused text inputs and
// inline validation errors.
func (m *Model) renderAddTaskModal(frameW, frameH int) string {
	w := modalWidth(frameW, 64)
	inner := w - 4

	var b strings.Builder
	b.WriteString(" " + m.theme.Bold.Render("Description:") + "\n")
	b.WriteString(" " + m.fieldBox(0, inner) + "\n")
	b.WriteString(" " + m.theme.Bold.Render("Spec file (optional):") + "\n")
	b.WriteString(" " + m.fieldBox(1, inner) + "\n")
	b.WriteString(" " + m.theme.Dim.Render("Base branch: (default)") + "\n")

	if m.addErr != "" {
		b.WriteString(" " + m.theme.Error.Render(m.theme.icon(iconCrashed)+" "+m.addErr) + "\n")
	}
	b.WriteString(m.styleHints("[Enter] add   [Tab] next field   [Esc] cancel"))

	return m.theme.modalBox("Add Task", b.String(), w)
}

// fieldBox renders one bordered text input, brightened when focused.
func (m *Model) fieldBox(idx, w int) string {
	if idx >= len(m.addInputs) {
		return ""
	}
	in := m.addInputs[idx]
	in.Width = w - 4
	style := m.theme.Field
	if m.addFocus == idx {
		style = m.theme.FieldFocus
	}
	bd := m.theme.borders
	line := style.Render(fitLine(" "+in.View(), w-2))
	return m.theme.PaneBorder.Render(bd.TL+strings.Repeat(bd.H, w-2)+bd.TR) + "\n " +
		m.theme.PaneBorder.Render(bd.V) + line + m.theme.PaneBorder.Render(bd.V) + "\n " +
		m.theme.PaneBorder.Render(bd.BL+strings.Repeat(bd.H, w-2)+bd.BR)
}

// renderLoopControlModal renders loop status plus the stop/kill/restart menu.
func (m *Model) renderLoopControlModal(frameW, frameH int) string {
	w := modalWidth(frameW, 62)
	label, pid := m.loopState()
	st := m.theme.LoopStatusStyle(label)

	var b strings.Builder
	fmt.Fprintf(&b, " %s %s\n", m.theme.Dim.Render("Status:"),
		st.Render(m.theme.icon(LoopStatusIcon(label))+" "+strings.ToUpper(label)))

	if cur, ok := m.currentTask(); ok {
		fmt.Fprintf(&b, " %s %s (cycle %d)\n", m.theme.Dim.Render("Current task:"), cur.ID, cur.CycleCount)
	} else {
		b.WriteString(" " + m.theme.Dim.Render("Current task: —") + "\n")
	}
	if pid > 0 {
		fmt.Fprintf(&b, " %s %d\n", m.theme.Dim.Render("PID:"), pid)
	} else {
		b.WriteString(" " + m.theme.Dim.Render("PID: (no pid file)") + "\n")
	}

	b.WriteString("\n" + m.theme.Subtitle.Render(" ── Controls ──") + "\n")
	b.WriteString(m.styleHints("[s] Stop after current task   (sentinel file)") + "\n")
	b.WriteString(m.styleHints("[K] Kill now (SIGTERM)        (needs PID file)") + "\n")
	b.WriteString(m.styleHints("[r] Restart loop              (stop + start)") + "\n")

	b.WriteString("\n" + m.theme.Subtitle.Render(" ── When idle ──") + "\n")
	b.WriteString(m.styleHints("[Enter] Run once (--once)") + "\n")
	b.WriteString(m.styleHints("[b] Run continuously") + "\n")
	b.WriteString(m.styleHints("[i] Run once interactive") + "\n")
	b.WriteString("\n" + m.styleHints("[Esc] close"))

	return m.theme.modalBox("Loop Control", b.String(), w)
}

// renderScheduleModal renders the Schedule Setup form.
func (m *Model) renderScheduleModal(frameW, frameH int) string {
	w := modalWidth(frameW, 62)
	var b strings.Builder
	b.WriteString(" " + m.theme.Bold.Render("Run colony loop --once every:") + "\n")
	b.WriteString(" " + m.theme.FieldFocus.Render(" 15m ") + "  " +
		m.theme.Dim.Render("(15m / 30m / 1h / 2h / 4h / custom…)") + "\n\n")
	b.WriteString(" " + m.theme.Dim.Render("Backend: crontab (Linux) / launchd (macOS) — auto-detected") + "\n\n")
	b.WriteString(" " + m.theme.Subtitle.Render("Preview:") + "\n")
	b.WriteString(" " + m.theme.Dim.Render("*/15 * * * * cd <project> && colony loop --once") + "\n\n")
	b.WriteString(m.styleHints("[Enter] install   [Esc] cancel"))
	return m.theme.modalBox("Schedule Setup", b.String(), w)
}

// renderConfirmModal renders the destructive-action confirmation.
func (m *Model) renderConfirmModal(frameW, frameH int) string {
	w := modalWidth(frameW, 60)
	if m.confirm == nil {
		return m.theme.modalBox("Confirm", " (nothing to confirm)\n", w)
	}

	var b strings.Builder
	b.WriteString(" " + m.theme.Bold.Render(m.confirm.Prompt) + "\n")
	for _, line := range wrapText(m.confirm.Detail, w-4) {
		b.WriteString(" " + m.theme.Dim.Render(line) + "\n")
	}
	b.WriteString("\n " + m.theme.Error.Render("This cannot be undone.") + "\n\n")
	b.WriteString(m.styleHints("[Enter] confirm   [Esc] cancel"))
	return m.theme.modalBox("Confirm", b.String(), w)
}

// renderReviewModal renders review results. Review records have no storage
// surface yet, so this reports the missing source instead of inventing a
// verdict.
func (m *Model) renderReviewModal(frameW, frameH int) string {
	w := modalWidth(frameW, 66)
	var b strings.Builder
	b.WriteString(" " + m.theme.Dim.Render("No review records available.") + "\n")
	b.WriteString(" " + m.theme.Dim.Render("Reviews are not yet persisted to the Colony database.") + "\n\n")
	b.WriteString(m.styleHints("[Enter] back   [y] copy as markdown   [n]ext review"))
	return m.theme.modalBox("Review Results", b.String(), w)
}

// renderObserveModal renders PR/CI observation state. Like reviews, the
// observation source is not yet persisted.
func (m *Model) renderObserveModal(frameW, frameH int) string {
	w := modalWidth(frameW, 62)
	var b strings.Builder

	linked := make([]string, 0, 4)
	for _, t := range m.tasks {
		if t.PRURL != "" {
			linked = append(linked, fmt.Sprintf("%s (%s)", t.ID, t.State))
		}
	}
	if len(linked) == 0 {
		b.WriteString(" " + m.theme.Dim.Render("No tasks are linked to a pull request.") + "\n\n")
	} else {
		b.WriteString(" " + m.theme.Bold.Render("Linked tasks:") + "\n")
		for _, l := range linked[:minInt(6, len(linked))] {
			b.WriteString("  " + l + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(m.styleHints("[Enter] view linked tasks   [r]efresh now   [q] close"))
	return m.theme.modalBox("Observe", b.String(), w)
}
