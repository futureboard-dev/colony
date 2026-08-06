package tui

import (
	"fmt"
	"strings"

	"github.com/futureboard-dev/colony/pkg/storage"
)

// renderQueue lays out the Queue as a split view: filter bar, scrollable task
// table, and a detail pane for the selected task.
func (m *Model) renderQueue(w, h int) string {
	filterH := 3
	tableH := clamp((h-filterH)*11/20, 5, h-filterH-6)
	detailH := h - filterH - tableH

	tasks := Apply(m.tasks, m.queue)
	return joinV(
		m.theme.pane("Filter", m.queueFilterBar(w-2), w, filterH, m.searching),
		m.theme.pane(m.queueTableTitle(len(tasks)), m.queueTable(tasks, w-2, tableH-2), w, tableH, !m.searching),
		m.theme.pane("Detail", m.queueDetail(tasks, w-2, detailH-2), w, detailH, false),
	)
}

// queueTableTitle labels the table with the visible/total task counts.
func (m *Model) queueTableTitle(shown int) string {
	if shown == len(m.tasks) {
		return fmt.Sprintf("Tasks (%d)", shown)
	}
	return fmt.Sprintf("Tasks (%d of %d)", shown, len(m.tasks))
}

// queueFilterBar renders the state filter with counts, sort order, and search.
func (m *Model) queueFilterBar(w int) string {
	counts := CountByState(m.tasks)
	state := or(m.queue.State, "all")
	stateLabel := fmt.Sprintf("all (%d)", len(m.tasks))
	if state != "all" {
		stateLabel = fmt.Sprintf("%s (%d)", state, counts[state])
	}

	search := m.queue.Search
	searchStyle := m.theme.Field
	if m.searching {
		search += "▏"
		searchStyle = m.theme.FieldFocus
	}
	if search == "" {
		search = " "
	}

	return fmt.Sprintf(" %s %s   %s %s   %s %s",
		m.theme.Dim.Render("State:"), m.theme.Accent.Render(stateLabel),
		m.theme.Dim.Render("Sort:"), m.theme.Accent.Render(m.queue.Sort),
		m.theme.Dim.Render("Search:"), searchStyle.Render(fitLine(" "+search, minInt(30, maxInt(10, w/3)))),
	)
}

// queueTable renders the task table, windowed so the cursor stays visible.
func (m *Model) queueTable(tasks []storage.Task, w, rows int) string {
	header := m.theme.Dim.Render(fmt.Sprintf("   %-8s %-14s %4s %-6s %s",
		"ID", "STATE", "CYC", "LANG", "DESCRIPTION"))
	if len(tasks) == 0 {
		return header + "\n\n" + m.theme.Dim.Render("   (no tasks match this filter)")
	}

	descW := maxInt(10, w-42)
	start, end := window(len(tasks), m.cursor, rows-1)

	var b strings.Builder
	b.WriteString(header + "\n")
	for i := start; i < end; i++ {
		t := tasks[i]
		marker := " "
		if i == m.cursor {
			marker = m.theme.icon("▶")
		}
		row := fmt.Sprintf(" %s %-8s %s %-12s %4d %-6s %s",
			marker, t.ID, m.theme.icon(StateIcon(t.State)), t.State,
			t.CycleCount, or(t.Lang, "—"), truncate(t.Description, descW))

		if i == m.cursor {
			b.WriteString(m.theme.Selected.Render(fitLine(row, w)))
		} else {
			b.WriteString(m.theme.StateStyle(t.State).Render(fitLine(row, w)))
		}
		b.WriteString("\n")
	}
	if hidden := len(tasks) - end; hidden > 0 {
		b.WriteString(scrollHint(m.theme, hidden))
	}
	return b.String()
}

// queueDetail renders metadata, last feedback, and recent sessions for the
// selected task.
func (m *Model) queueDetail(tasks []storage.Task, w, rows int) string {
	if len(tasks) == 0 || m.cursor >= len(tasks) {
		return m.theme.Dim.Render("  (no task selected)")
	}
	t := tasks[m.cursor]
	st := m.theme.StateStyle(t.State)

	var b strings.Builder
	title := fmt.Sprintf(" %s %s %s",
		m.theme.Accent.Render(t.ID), m.theme.icon("—"),
		truncate(t.Description, maxInt(10, w-len(t.State)-16)))
	badge := st.Render("[" + t.State + "]")
	b.WriteString(title +
		strings.Repeat(" ", maxInt(1, w-lenVisible(title)-lenVisible(badge)-1)) +
		badge + "\n")

	fmt.Fprintf(&b, " %s\n", m.theme.Dim.Render(fmt.Sprintf(
		"Cycles: %d   Lang: %s   Created: %s   Updated: %s",
		t.CycleCount, or(t.Lang, "—"),
		t.CreatedAt.Format("2006-01-02 15:04"), timeAgo(taskTimestamp(t)))))

	if t.SpecPath != "" {
		fmt.Fprintf(&b, " %s %s\n", m.theme.Dim.Render("Spec:"), t.SpecPath)
	}
	if t.BaseBranch != "" {
		fmt.Fprintf(&b, " %s %s\n", m.theme.Dim.Render("Base:"), t.BaseBranch)
	}

	used := strings.Count(b.String(), "\n")
	remaining := rows - used - 1

	if t.LastFeedback != "" && remaining > 2 {
		b.WriteString("\n" + m.theme.Subtitle.Render(" ── Last Feedback ──") + "\n")
		remaining -= 2
		for _, line := range wrapText(t.LastFeedback, w-3) {
			if remaining <= 1 {
				break
			}
			b.WriteString("  " + m.theme.StateNeedsFix.Render(truncate(line, w-3)) + "\n")
			remaining--
		}
	}

	if sessions := m.sessionsForTask(t.ID); len(sessions) > 0 && remaining > 1 {
		b.WriteString(m.theme.Subtitle.Render(fmt.Sprintf(" ── Recent Sessions (%d) ──", len(sessions))) + "\n")
		parts := make([]string, 0, 3)
		for _, s := range sessions[:minInt(3, len(sessions))] {
			parts = append(parts, fmt.Sprintf("%s %s %s",
				s.ID, s.Status, humanDuration(sessionDuration(s.StartedAt, s.FinishedAt))))
		}
		b.WriteString("  " + m.theme.Dim.Render(truncate(strings.Join(parts, "  ·  "), w-3)))
	}
	return b.String()
}

// sessionsForTask returns sessions recorded against the given task. Sessions
// written before the task_id column existed have an empty TaskID and never
// match — their link is not recoverable.
func (m *Model) sessionsForTask(taskID string) []storage.Session {
	if taskID == "" {
		return nil
	}
	var out []storage.Session
	for _, s := range m.sessions {
		if s.TaskID == taskID {
			out = append(out, s)
		}
	}
	return out
}
