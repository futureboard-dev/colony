package tui

import (
	"fmt"
	"strings"

	"github.com/futureboard-dev/colony/pkg/storage"
)

// renderTaskDetail lays out the full-screen Task Detail: header, metadata,
// description, gate feedback per cycle, and the task's sessions.
func (m *Model) renderTaskDetail(w, h int) string {
	sel, ok := m.selectedTask()
	if !ok {
		return m.theme.pane("Task Detail",
			"\n  "+m.theme.Dim.Render("(no task selected)")+
				"\n  "+m.theme.Dim.Render("Press [2] to pick a task from the queue."),
			w, h, true)
	}

	// Fixed panes first; gate feedback absorbs whatever is left, borrowing from
	// description then sessions if the terminal is short. The five heights must
	// sum to exactly h.
	headerH, metaH := 3, 5
	sessH := clamp(h/5, 4, 7)
	descH := clamp(h/6, 3, 6)
	gateH := h - headerH - metaH - sessH - descH
	for gateH < 4 && descH > 3 {
		descH--
		gateH++
	}
	for gateH < 4 && sessH > 4 {
		sessH--
		gateH++
	}

	return joinV(
		m.theme.pane("", m.detailHeader(sel, w-2), w, headerH, true),
		m.theme.pane("Metadata", m.detailMetadata(sel, w-2), w, metaH, false),
		m.theme.pane("Description", m.detailDescription(sel, w-2, descH-2), w, descH, false),
		m.theme.pane("Gate Feedback", m.detailGateFeedback(w-2, gateH-2), w, gateH, false),
		m.theme.pane("Sessions", m.detailSessions(sel, w-2, sessH-2), w, sessH, false),
	)
}

// detailHeader renders the task title line with its state badge.
func (m *Model) detailHeader(t storageTaskRef, w int) string {
	st := m.theme.StateStyle(t.State)
	badge := st.Render(m.theme.icon(StateIcon(t.State)) + " " + t.State)
	title := fmt.Sprintf(" %s %s %s %s",
		m.theme.Dim.Render("Task:"),
		m.theme.Accent.Render(t.ID), m.theme.icon("—"),
		truncate(t.Description, maxInt(10, w-len(t.State)-22)))
	pad := maxInt(1, w-lenVisible(title)-lenVisible(badge)-1)
	return title + strings.Repeat(" ", pad) + badge
}

// detailMetadata renders the two-column metadata block.
func (m *Model) detailMetadata(t storageTaskRef, w int) string {
	col := maxInt(20, w/2)
	rows := [][2]string{
		{"State: " + t.State, "Base branch: " + or(t.BaseBranch, "(default)")},
		{fmt.Sprintf("Cycles: %d", t.CycleCount), "Lang: " + langCell(t.Lang)},
		{"Spec: " + or(t.SpecPath, "(none)"), ""},
	}
	var b strings.Builder
	for _, r := range rows {
		left := fitLine(" "+truncate(r[0], col-2), col)
		b.WriteString(m.theme.Dim.Render(left) + m.theme.Dim.Render(truncate(r[1], w-col)) + "\n")
	}
	return b.String()
}

// detailDescription renders the task description, wrapped to the pane.
func (m *Model) detailDescription(t storageTaskRef, w, rows int) string {
	lines := wrapText(t.Description, w-2)
	var b strings.Builder
	for i, line := range lines {
		if i >= rows {
			b.WriteString(scrollHint(m.theme, len(lines)-rows))
			break
		}
		b.WriteString(" " + line + "\n")
	}
	return b.String()
}

// detailGateFeedback renders rejected gate output newest-cycle-first, degrading
// gracefully for rows recorded before per-step feedback existed.
func (m *Model) detailGateFeedback(w, rows int) string {
	rejected := make([]storage.Step, 0)
	for _, s := range m.steps {
		if s.Role == "gate" && s.Decision == "REJECTED" {
			rejected = append(rejected, s)
		}
	}
	if len(rejected) == 0 {
		return m.theme.Dim.Render("  (no gate output — cycle passed or was aborted)")
	}

	var b strings.Builder
	used := 0
	for i := len(rejected) - 1; i >= 0 && used < rows; i-- {
		s := rejected[i]
		label := fmt.Sprintf(" ── Step %d", s.StepNum)
		if i == len(rejected)-1 {
			label += " (latest)"
		}
		b.WriteString(m.theme.Subtitle.Render(label+" ──") + "\n")
		used++

		for _, line := range strings.Split(strings.TrimRight(RenderGateOutput(s.Output), "\n"), "\n") {
			if used >= rows {
				break
			}
			b.WriteString("  " + m.theme.StateBlocked.Render(truncate(line, w-3)) + "\n")
			used++
		}
	}
	return b.String()
}

// detailSessions lists the sessions belonging to this task.
func (m *Model) detailSessions(t storageTaskRef, w, rows int) string {
	sessions := m.sessionsForTask(t.ID)
	if len(sessions) == 0 {
		return m.theme.Dim.Render("  (no sessions yet)")
	}
	var b strings.Builder
	for i, s := range sessions {
		if i >= rows {
			b.WriteString(scrollHint(m.theme, len(sessions)-rows))
			break
		}
		icon, style := m.sessionBadge(s.Status)
		fmt.Fprintf(&b, "  %d. %-34s %s %-10s %s\n",
			i+1, truncate(s.ID, 34), style.Render(icon), style.Render(s.Status),
			m.theme.Dim.Render(humanDuration(sessionDuration(s.StartedAt, s.FinishedAt))))
	}
	return b.String()
}

// lenVisible measures a styled string's printable width.
func lenVisible(s string) int {
	w, _ := blockSize(s)
	return w
}
