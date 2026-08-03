package tui

import (
	"fmt"
	"strings"
)

// renderTaskDetail renders the full-screen Task Detail view: header, metadata,
// description, per-cycle gate feedback, sessions, and actions.
func (m *Model) renderTaskDetail() string {
	sel, ok := m.selectedTask()
	if !ok {
		return m.theme.Header.Render("Task Detail") + "\n  (no task selected)\n  Press 2 to choose a task from the queue."
	}
	var b strings.Builder
	b.WriteString(m.theme.Header.Render("Task: " + sel.ID + " — " + sel.Description + "  [" + sel.State + "]"))
	fmt.Fprintf(&b, "\n  State: %s   Base branch: %s", sel.State, or(sel.BaseBranch, "main"))
	fmt.Fprintf(&b, "\n  Cycles: %d   Lang: %s", sel.CycleCount, or(sel.Lang, "go"))
	if sel.SpecPath != "" {
		b.WriteString("\n  Spec: " + sel.SpecPath)
	}

	b.WriteString("\n\nDescription\n" + sel.Description)

	b.WriteString("\n\nGate Feedback\n")
	// Per-cycle gate feedback comes from steps with role "gate".
	hasGate := false
	for _, step := range m.steps {
		if step.Role == "gate" && step.Decision == "REJECTED" {
			hasGate = true
			b.WriteString("\n  Gate \"go\" failed.\n")
			block := RenderGateOutput(step.Output)
			b.WriteString(indentEach(block, "    "))
		}
	}
	if !hasGate {
		b.WriteString("  (none)")
	}

	b.WriteString("\n\nSessions\n")
	for _, s := range m.sessions {
		fmt.Fprintf(&b, "  %s  %-10s\n", s.ID, s.Status)
	}

	b.WriteString("\n\nActions\n")
	b.WriteString("  [q]uit  [Esc] back  [r]etry  [b]lock  [m]ark done  [x]delete  [v] live")
	return b.String()
}
