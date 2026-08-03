package tui

import (
	"fmt"
	"strings"
)

// renderDashboard renders the Dashboard view: queue summary counts, loop
// status, recent activity, and schedule status.
func (m *Model) renderDashboard() string {
	counts := CountByState(m.tasks)
	var b strings.Builder
	b.WriteString(m.theme.Header.Render("Dashboard"))
	b.WriteString("\n\nQueue Summary\n")
	fmt.Fprintf(&b, "  open %d  needs-fix %d  blocked %d  done %d\n",
		counts["open"], counts["needs-fix"], counts["blocked"], counts["done"])

	var loopLabel string
	var pid int
	if m.opts.ColonyDir != "" {
		loopLabel, pid = LoopState(m.opts.ColonyDir)
	} else {
		loopLabel = "idle"
	}
	b.WriteString("\nLoop Status\n")
	fmt.Fprintf(&b, "  %s %s", LoopStatusIcon(loopLabel), strings.ToUpper(loopLabel))
	if pid > 0 {
		fmt.Fprintf(&b, "  (pid %d)", pid)
	}
	b.WriteString("\n")

	b.WriteString("\nRecent Activity\n")
	if len(m.sessions) == 0 {
		b.WriteString("  (none)\n")
	} else {
		limit := len(m.sessions)
		if limit > 20 {
			limit = 20
		}
		for i := 0; i < limit; i++ {
			s := m.sessions[i]
			fmt.Fprintf(&b, "  %s  %-10s\n", s.ID, s.Status)
		}
	}

	b.WriteString("\nSchedule\n")
	b.WriteString("  (none)\n")
	return b.String()
}
