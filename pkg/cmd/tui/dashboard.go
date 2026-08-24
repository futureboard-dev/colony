package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/futureboard-dev/colony/pkg/storage"
)

// renderDashboard lays out the Dashboard: queue summary and loop status side by
// side, an activity feed below, and the schedule pinned at the bottom.
func (m *Model) renderDashboard(w, h int) string {
	topH := clamp(h*2/5, 7, 12)
	schedH := 4
	activityH := maxInt(3, h-topH-schedH)
	if activityH+topH+schedH > h {
		topH = maxInt(3, h-schedH-activityH)
	}

	leftW, rightW := splitW(w, 0.38)
	top := joinH(
		m.theme.pane("Queue Summary", m.dashQueueSummary(leftW-2), leftW, topH, false),
		m.theme.pane("Loop Status", m.dashLoopStatus(rightW-2), rightW, topH, false),
	)

	return joinV(
		top,
		m.theme.pane("Recent Activity", m.dashActivity(w-2, activityH-2), w, activityH, false),
		m.theme.pane("Schedule", m.dashSchedule(), w, schedH, false),
	)
}

// dashQueueSummary renders per-state counts with proportional bars.
func (m *Model) dashQueueSummary(w int) string {
	counts := CountByState(m.tasks)
	order := []string{"open", "in-progress", "needs-fix", "blocked", "done"}

	max := 1
	for _, s := range order {
		if counts[s] > max {
			max = counts[s]
		}
	}
	barW := clamp(w-24, 4, 20)

	var b strings.Builder
	for _, state := range order {
		st := m.theme.StateStyle(state)
		row := fmt.Sprintf("%s %-12s %3d  ", m.theme.icon(StateIcon(state)), state, counts[state])
		b.WriteString(st.Render(row) + m.theme.Dim.Render(bar(counts[state], max, barW)) + "\n")
	}
	b.WriteString("\n" + m.theme.Dim.Render(fmt.Sprintf("  %d tasks total", len(m.tasks))))
	return b.String()
}

// dashLoopStatus renders the loop's live state and its controls.
func (m *Model) dashLoopStatus(w int) string {
	label, pid := m.loopState()
	st := m.theme.LoopStatusStyle(label)

	var b strings.Builder
	b.WriteString(st.Render(m.theme.icon(LoopStatusIcon(label))+" "+strings.ToUpper(label)) + "\n\n")

	if pid > 0 {
		fmt.Fprintf(&b, "PID: %d\n", pid)
	} else {
		b.WriteString(m.theme.Dim.Render("No loop process") + "\n")
	}

	if cur, ok := m.currentTask(); ok {
		fmt.Fprintf(&b, "Current: %s (cycle %d)\n", cur.ID, cur.CycleCount)
	} else {
		b.WriteString(m.theme.Dim.Render("Current: —") + "\n")
	}

	done := CountByState(m.tasks)["done"]
	fmt.Fprintf(&b, "Tasks done: %d\n", done)

	if next, ok := m.nextTask(); ok {
		fmt.Fprintf(&b, "Next: %s (open)\n", next.ID)
	}

	b.WriteString("\n" + m.styleHints("[s]top  [r]estart  [v] live output"))
	return b.String()
}

// dashActivity renders the newest task transitions, newest first.
func (m *Model) dashActivity(w, rows int) string {
	if len(m.tasks) == 0 {
		return m.theme.Dim.Render("  (no tasks yet — press [a] to add one)")
	}

	recent := append([]storage.Task(nil), m.tasks...)
	sort.SliceStable(recent, func(i, j int) bool {
		return taskTimestamp(recent[i]).After(taskTimestamp(recent[j]))
	})
	if len(recent) > 20 {
		recent = recent[:20]
	}

	var b strings.Builder
	shown := minInt(rows, len(recent))
	descW := maxInt(10, w-40)
	for _, t := range recent[:shown] {
		st := m.theme.StateStyle(t.State)
		fmt.Fprintf(&b, "  %-8s %s %-12s %10s  %s\n",
			t.ID,
			st.Render(m.theme.icon(StateIcon(t.State))),
			st.Render(t.State),
			m.theme.Dim.Render(timeAgo(taskTimestamp(t))),
			truncate(t.Description, descW),
		)
	}
	if hidden := len(recent) - shown; hidden > 0 {
		b.WriteString(scrollHint(m.theme, hidden))
	}
	return b.String()
}

// dashSchedule renders schedule status. Schedule data is not yet wired into the
// TUI's store surface, so this reports the unconfigured state.
func (m *Model) dashSchedule() string {
	return m.theme.Dim.Render("  cron: (none configured)") + "\n" +
		m.styleHints("[e]nable schedule  [l]oop control")
}

// currentTask returns the task the loop is actively working, if any.
func (m *Model) currentTask() (storage.Task, bool) {
	for _, t := range m.tasks {
		if t.State == "in-progress" || t.State == "building" {
			return t, true
		}
	}
	return storage.Task{}, false
}

// nextTask returns the oldest open task — what the loop picks up next.
func (m *Model) nextTask() (storage.Task, bool) {
	var best storage.Task
	found := false
	for _, t := range m.tasks {
		if t.State != "open" {
			continue
		}
		if !found || t.CreatedAt.Before(best.CreatedAt) {
			best, found = t, true
		}
	}
	return best, found
}

// truncate shortens plain text with an ellipsis.
func truncate(s string, w int) string {
	r := []rune(s)
	if w <= 0 || len(r) <= w {
		return s
	}
	if w <= 1 {
		return string(r[:w])
	}
	return string(r[:w-1]) + "…"
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
