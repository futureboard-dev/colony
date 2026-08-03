package tui

import (
	"fmt"
	"strings"
)

// renderSessions renders the Sessions view: a session table (capped at a
// sensible limit) with a detail pane for the selected session and its step
// timeline.
func (m *Model) renderSessions() string {
	var b strings.Builder
	b.WriteString(m.theme.Header.Render("Sessions"))
	if len(m.sessions) == 0 {
		b.WriteString("\n  (no sessions)")
		return b.String()
	}
	b.WriteString("\n  Session ID                     Status      Task\n")
	for i, s := range m.sessions {
		if i >= 200 {
			b.WriteString("\n  [load more]\n")
			break
		}
		mark := " "
		if i == m.cursor {
			mark = "▶"
		}
		fmt.Fprintf(&b, "  %s %-30s %-10s\n", mark, s.ID, s.Status)
	}
	if m.cursor < len(m.sessions) {
		sel := m.sessions[m.cursor]
		b.WriteString("\n" + m.theme.Accent.Render("Session: "+sel.ID))
		fmt.Fprintf(&b, "\n  Status: %s   Started: %s", sel.Status, sel.StartedAt.Format("2006-01-02 15:04:05"))
		b.WriteString("\n  Step timeline: (select a task to expand)")
	}
	return b.String()
}
