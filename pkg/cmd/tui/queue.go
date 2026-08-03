package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderQueue renders the Queue view: a filter bar, a scrollable task table,
// and a detail pane for the selected task.
func (m *Model) renderQueue() string {
	var b strings.Builder
	b.WriteString(m.theme.Header.Render("Queue"))
	fmt.Fprintf(&b, "  Filter: state=%q sort=%q search=%q\n", m.queue.State, m.queue.Sort, m.queue.Search)

	tasks := Apply(m.tasks, m.queue)
	if len(tasks) == 0 {
		b.WriteString("\n  (empty queue)\n")
		return b.String()
	}
	b.WriteString("\n  ID        State       Cyc  Description\n")
	for i, t := range tasks {
		mark := " "
		if i == m.cursor {
			mark = "▶"
		}
		icon := StateIcon(t.State)
		color := StateColor(t.State, m.theme)
		line := fmt.Sprintf("  %s %-10s %-3s %d    %s", mark, t.ID, icon+" "+t.State, t.CycleCount, t.Description)
		if i == m.cursor {
			line = m.theme.Selected.Render(line)
		} else if color != "" {
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(line)
		}
		b.WriteString(line + "\n")
	}

	if len(tasks) > 0 && m.cursor < len(tasks) {
		sel := tasks[m.cursor]
		b.WriteString("\n" + m.theme.Accent.Render(sel.ID+" — "+sel.Description+"  ["+sel.State+"]"))
		fmt.Fprintf(&b, "\n  Cycles: %d   Lang: %s", sel.CycleCount, or(sel.Lang, "go"))
		if sel.LastFeedback != "" {
			b.WriteString("\n  -- Last Feedback --\n  " + strings.ReplaceAll(sel.LastFeedback, "\n", "\n  "))
		}
	}
	return b.String()
}
