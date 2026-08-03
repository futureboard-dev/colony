package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/futureboard-dev/colony/pkg/storage"
)

// maxVisibleSessions caps the live-rendered session list; the rest is reachable
// through the "[load more]" trigger.
const maxVisibleSessions = 200

// renderSessions lays out the Sessions view: filter bar, session table, and a
// detail pane holding the selected session's step timeline.
func (m *Model) renderSessions(w, h int) string {
	filterH := 3
	tableH := clamp((h-filterH)/2, 5, h-filterH-6)
	detailH := h - filterH - tableH

	return joinV(
		m.theme.pane("Filter", m.sessionFilterBar(), w, filterH, false),
		m.theme.pane(fmt.Sprintf("Sessions (%d)", len(m.sessions)),
			m.sessionTable(w-2, tableH-2), w, tableH, true),
		m.theme.pane("Session Detail", m.sessionDetail(w-2, detailH-2), w, detailH, false),
	)
}

// sessionFilterBar renders the session filters. Filter state is not yet
// user-editable; the bar reports the active (default) selection.
func (m *Model) sessionFilterBar() string {
	return fmt.Sprintf(" %s %s   %s %s   %s %s",
		m.theme.Dim.Render("Type:"), m.theme.Accent.Render("all"),
		m.theme.Dim.Render("Status:"), m.theme.Accent.Render("all"),
		m.theme.Dim.Render("Sort:"), m.theme.Accent.Render("started"))
}

// sessionTable renders the session list, windowed around the cursor.
func (m *Model) sessionTable(w, rows int) string {
	header := m.theme.Dim.Render(fmt.Sprintf("   %-36s %-12s %-9s %s",
		"SESSION ID", "STATUS", "DURATION", "STARTED"))
	if len(m.sessions) == 0 {
		return header + "\n\n" + m.theme.Dim.Render("   (no sessions yet)")
	}

	visible := m.sessions
	truncated := false
	if len(visible) > maxVisibleSessions {
		visible, truncated = visible[:maxVisibleSessions], true
	}
	start, end := window(len(visible), m.cursor, rows-1)

	var b strings.Builder
	b.WriteString(header + "\n")
	for i := start; i < end; i++ {
		s := visible[i]
		marker := " "
		if i == m.cursor {
			marker = m.theme.icon("▶")
		}
		icon, style := m.sessionBadge(s.Status)
		row := fmt.Sprintf(" %s %-36s %s %-10s %-9s %s",
			marker, truncate(s.ID, 36), icon, s.Status,
			humanDuration(sessionDuration(s.StartedAt, s.FinishedAt)),
			timeAgo(s.StartedAt))

		if i == m.cursor {
			b.WriteString(m.theme.Selected.Render(fitLine(row, w)))
		} else {
			b.WriteString(style.Render(fitLine(row, w)))
		}
		b.WriteString("\n")
	}
	if hidden := len(visible) - end; hidden > 0 {
		b.WriteString(scrollHint(m.theme, hidden))
	}
	if truncated {
		b.WriteString(m.theme.Accent.Render("  [load more]"))
	}
	return b.String()
}

// sessionDetail renders the selected session's metadata and step timeline.
func (m *Model) sessionDetail(w, rows int) string {
	if len(m.sessions) == 0 || m.cursor >= len(m.sessions) {
		return m.theme.Dim.Render("  (no session selected)")
	}
	s := m.sessions[m.cursor]
	icon, style := m.sessionBadge(s.Status)

	var b strings.Builder
	fmt.Fprintf(&b, " %s %s  %s\n", m.theme.Dim.Render("Session:"),
		m.theme.Accent.Render(s.ID), style.Render(icon+" "+s.Status))

	finished := "—"
	if s.FinishedAt != nil {
		finished = s.FinishedAt.Format("2006-01-02 15:04:05")
	}
	fmt.Fprintf(&b, " %s\n\n", m.theme.Dim.Render(fmt.Sprintf(
		"Duration: %s   Started: %s   Finished: %s",
		humanDuration(sessionDuration(s.StartedAt, s.FinishedAt)),
		s.StartedAt.Format("2006-01-02 15:04:05"), finished)))

	b.WriteString(m.theme.Subtitle.Render(" Step timeline:") + "\n")
	b.WriteString(m.stepTimeline(s.ID, w, rows-4))
	return b.String()
}

// stepTimeline renders the session's steps as a proportional gantt strip.
func (m *Model) stepTimeline(sessionID string, w, rows int) string {
	steps := make([]storage.Step, 0)
	for _, s := range m.steps {
		if s.SessionID == sessionID {
			steps = append(steps, s)
		}
	}
	if len(steps) == 0 {
		return m.theme.Dim.Render("  (no steps recorded)")
	}

	total := int64(0)
	for _, s := range steps {
		total += s.DurationMS
	}
	barW := clamp(w-52, 8, 28)

	var b strings.Builder
	used := 0
	for i, s := range steps {
		if used >= rows {
			b.WriteString(scrollHint(m.theme, len(steps)-i))
			break
		}
		icon, style := m.decisionBadge(s.Decision)
		fmt.Fprintf(&b, "  %d. %-6s %-10s %s %-9s %8s  %s\n",
			s.StepNum, "ROLE", or(s.Role, "—"), style.Render(icon),
			style.Render(or(s.Decision, "—")),
			humanDuration(durationOf(s.DurationMS)),
			m.theme.Dim.Render(bar(int(s.DurationMS), int(maxInt64(total, 1)), barW)))
		used++

		if s.Decision == "REJECTED" && used < rows {
			first := firstLine(RenderGateOutput(s.Output))
			b.WriteString("     " + m.theme.Dim.Render("└─ ") +
				m.theme.StateBlocked.Render(truncate(first, maxInt(10, w-10))) + "\n")
			used++
		}
	}
	return b.String()
}

// sessionBadge maps a session status to its icon and style.
func (m *Model) sessionBadge(status string) (string, lipgloss.Style) {
	switch status {
	case "completed", "complete", "done":
		return m.theme.icon(iconApproved), m.theme.StateDone
	case "failed", "error":
		return m.theme.icon(iconCrashed), m.theme.StateBlocked
	case "running":
		return m.theme.icon(iconProgress), m.theme.StateProgress
	default:
		return m.theme.icon(iconOpen), m.theme.Dim
	}
}

// decisionBadge maps a step decision to its icon and style.
func (m *Model) decisionBadge(decision string) (string, lipgloss.Style) {
	switch decision {
	case "APPROVED":
		return m.theme.icon(iconApproved), m.theme.StateDone
	case "REJECTED":
		return m.theme.icon(iconRejected), m.theme.StateBlocked
	default:
		return m.theme.icon(iconOpen), m.theme.Dim
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func durationOf(ms int64) time.Duration {
	return time.Duration(ms) * time.Millisecond
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
