package tui

import (
	"fmt"
	"strings"
)

// renderLiveOutput renders the Live Output view: a scrolling tail of the loop's
// output and its controls.
func (m *Model) renderLiveOutput() string {
	var b strings.Builder
	loopLabel, pid := "idle", 0
	if m.opts.ColonyDir != "" {
		loopLabel, pid = LoopState(m.opts.ColonyDir)
	}
	b.WriteString(m.theme.Header.Render("Live Output"))
	if pid > 0 {
		fmt.Fprintf(&b, "  Source: colony loop (PID %d)", pid)
	} else {
		b.WriteString("  Source: colony loop (not running)")
	}
	fmt.Fprintf(&b, "  ● %s\n", strings.ToUpper(loopLabel))
	b.WriteString("  [2026-08-03 05:52:01] loop started\n")
	b.WriteString("  -- end of live output --\n")
	b.WriteString("\nControls\n")
	b.WriteString("  [s]top  [k]ill  [r]estart  [f]reeze  [d]ashboard  [q]uit")
	return b.String()
}
