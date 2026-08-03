package tui

import (
	"fmt"
	"strings"
)

// renderAddTaskModal renders the Add Task modal with inline validation errors.
func (m *Model) renderAddTaskModal() string {
	spec := m.addSpec
	if spec == "" {
		spec = "(optional)"
	}
	return m.theme.Header.Render("Add Task") + fmt.Sprintf(`
Description:
  %s

Spec file (optional):
  %s

Base branch: [main]

[Enter] add   [Esc] cancel`, or(m.addDesc, "(empty)"), spec) + modalErr(m.addErr)
}

// renderLoopControlModal renders the Loop Control modal.
func (m *Model) renderLoopControlModal() string {
	label, pid := "idle", 0
	if m.opts.ColonyDir != "" {
		label, pid = LoopState(m.opts.ColonyDir)
	}
	return m.theme.Header.Render("Loop Control") + fmt.Sprintf(`
Status: ● %s`, strings.ToUpper(label)) + fmt.Sprintf(`
Pid: %d

  [s] Stop after current task
  [K] Kill now (SIGTERM)
  [r] Restart loop

Schedule:
  (none)   [d] disable   [e] change interval

[Esc] close`, pid)
}

// renderScheduleModal renders the Schedule Setup modal.
func (m *Model) renderScheduleModal() string {
	return m.theme.Header.Render("Schedule Setup") + `
Run colony loop --once every:
  [15m]

Backend: crontab (Linux) / launchd (macOS) — auto-detected

[Enter] install   [Esc] cancel`
}

// renderConfirmModal renders the destructive-action confirmation modal.
func (m *Model) renderConfirmModal() string {
	if m.confirm == nil {
		return m.theme.Header.Render("Confirm")
	}
	return m.theme.Header.Render("Confirm") + fmt.Sprintf(`
%s
%s
This cannot be undone.

[Enter] confirm   [Esc] cancel`, m.confirm.Prompt, m.confirm.Detail)
}

// renderReviewModal renders the Review Results modal.
func (m *Model) renderReviewModal() string {
	return m.theme.Header.Render("Review") + `
Overall: (no verdict)
[Enter] back  [y] copy as markdown  [n] next review`
}

// renderObserveModal renders the Observe modal.
func (m *Model) renderObserveModal() string {
	return m.theme.Header.Render("Observe") + `
PR: (none)
State: (none)

[Enter] view linked tasks  [r]efresh now  [q] close`
}
