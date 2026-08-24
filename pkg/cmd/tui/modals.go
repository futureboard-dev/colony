package tui

import (
	"fmt"
	"strings"

	"github.com/futureboard-dev/colony/pkg/storage"
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
	b.WriteString(" " + m.fieldBox(addDescField, inner) + "\n")
	b.WriteString(" " + m.theme.Bold.Render("Spec file (optional):") + "\n")
	b.WriteString(" " + m.fieldBox(addSpecField, inner) + "\n")
	// Base and lang share a row so the modal still fits an 80x24 terminal.
	leftW := inner - langFieldWidth - 1
	b.WriteString(" " + fitLine(m.theme.Bold.Render("Base branch (optional):"), leftW+1) +
		m.theme.Bold.Render("Language:") + "\n")
	b.WriteString(m.fieldPair(addBaseField, leftW, addLangField, langFieldWidth))
	b.WriteString(" " + m.checkboxLine("Skip format gate (--no-format)", m.addNoFormat,
		m.addFocus == addNoFormatField) + "\n")

	if m.addErr != "" {
		b.WriteString(" " + m.theme.Error.Render(m.theme.icon(iconCrashed)+" "+m.addErr) + "\n")
	}
	b.WriteString(m.styleHints("[Enter] add  [Tab] next  [Space] toggle  [Esc] cancel"))

	return m.theme.modalBox("Add Task", b.String(), w)
}

// checkboxLine renders a boolean toggle row, brightened when focused.
func (m *Model) checkboxLine(label string, checked, focused bool) string {
	box := "[ ]"
	if checked {
		box = "[x]"
	}
	style := m.theme.Dim
	if focused {
		style = m.theme.FieldFocus
	}
	return style.Render(box + " " + label)
}

// langFieldWidth is the fixed width of the Language input, which shares a row
// with the wider Base branch input.
const langFieldWidth = 18

// fieldBox renders one bordered text input, brightened when focused. Its
// continuation lines carry the modal's one-column left padding.
func (m *Model) fieldBox(idx, w int) string {
	return strings.Join(m.fieldBoxLines(idx, w), "\n ")
}

// fieldPair renders two field boxes side by side as a single padded block.
func (m *Model) fieldPair(leftIdx, leftW, rightIdx, rightW int) string {
	left, right := m.fieldBoxLines(leftIdx, leftW), m.fieldBoxLines(rightIdx, rightW)
	var b strings.Builder
	for i := range left {
		b.WriteString(" ")
		b.WriteString(left[i])
		b.WriteString(" ")
		b.WriteString(right[i])
		b.WriteString("\n")
	}
	return b.String()
}

// fieldBoxLines renders one bordered text input as its three unpadded lines.
func (m *Model) fieldBoxLines(idx, w int) []string {
	if idx >= len(m.addInputs) || w < 4 {
		return []string{"", "", ""}
	}
	in := m.addInputs[idx]
	in.Width = w - 4
	style := m.theme.Field
	if m.addFocus == idx {
		style = m.theme.FieldFocus
	}
	bd := m.theme.borders
	return []string{
		m.theme.PaneBorder.Render(bd.TL + strings.Repeat(bd.H, w-2) + bd.TR),
		m.theme.PaneBorder.Render(bd.V) + style.Render(fitLine(" "+in.View(), w-2)) +
			m.theme.PaneBorder.Render(bd.V),
		m.theme.PaneBorder.Render(bd.BL + strings.Repeat(bd.H, w-2) + bd.BR),
	}
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
	b.WriteString(m.styleHints("[r] Restart loop              (kill + start)") + "\n")

	b.WriteString("\n" + m.theme.Subtitle.Render(" ── When idle ──") + "\n")
	b.WriteString(m.styleHints("[Enter] Run once (--once)") + "\n")
	b.WriteString(m.styleHints("[b] Run continuously") + "\n")
	b.WriteString(m.styleHints("[i] Run once interactive      (hands over the terminal)") + "\n")
	b.WriteString(m.styleHints("[c] Run with flags…           (command palette)") + "\n")
	b.WriteString("\n" + m.styleHints("[Esc] close"))

	return m.theme.modalBox("Loop Control", b.String(), w)
}

// renderScheduleModal renders the Schedule Setup form.
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

// renderReviewModal renders the approved/rejected tallies recorded per run,
// newest first, with the cursor row expanded to show its log path.
func (m *Model) renderReviewModal(frameW, frameH int) string {
	w := modalWidth(frameW, 72)
	var b strings.Builder

	switch {
	case m.runsErr != nil:
		b.WriteString(" " + m.theme.Error.Render("Could not read runs: "+m.runsErr.Error()) + "\n\n")
	case len(m.runs) == 0:
		b.WriteString(" " + m.theme.Dim.Render("No runs recorded yet.") + "\n\n")
	default:
		for i, r := range m.runs {
			b.WriteString(m.reviewRow(i, r))
		}
		b.WriteString("\n")
		if sel, ok := m.selectedRun(); ok {
			path := sel.LogPath
			if path == "" {
				path = "(no log path recorded)"
			}
			b.WriteString(" " + m.theme.Dim.Render(truncate("log: "+path, w-4)) + "\n\n")
		}
	}

	b.WriteString(m.styleHints("[j/k] select   [y] copy log path   [Esc] back"))
	return m.theme.modalBox("Review Results", b.String(), w)
}

// reviewRow formats one run: cursor marker, id, kind, status, and the review
// tallies that make this modal worth opening.
func (m *Model) reviewRow(i int, r storage.Run) string {
	marker := "  "
	if i == m.runCursor {
		marker = m.theme.Accent.Render("> ")
	}
	// Components are sized before styling; truncating a styled string would cut
	// through its escape sequences.
	head := fmt.Sprintf("%-14s %-6s ", truncate(r.ID, 14), truncate(r.Kind, 6))
	tally := fmt.Sprintf(" %d approved / %d rejected  ", r.Approved, r.Rejected)
	return " " + marker + head +
		m.theme.StateStyle(r.Status).Render(fitLine(r.Status, 10)) +
		tally + m.theme.Dim.Render(timeAgo(r.StartedAt)) + "\n"
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
	b.WriteString(m.styleHints("[Esc] back"))
	return m.theme.modalBox("Observe", b.String(), w)
}
