package tui

import (
	"fmt"
	"strings"
)

// helpEntry is one keybind row in the help overlay.
type helpEntry struct{ key, desc string }

// navHelp is shared across every view.
var navHelp = []helpEntry{
	{"j / ↓", "move down"},
	{"k / ↑", "move up"},
	{"g", "top of list"},
	{"G", "bottom of list"},
	{"Enter", "select / drill in"},
	{"Esc", "back / close"},
	{"/", "search / filter"},
	{"q", "quit"},
}

// actionHelp is the per-view action column.
var actionHelp = map[View][]helpEntry{
	ViewDashboard: {
		{"a", "add task"},
		{"l", "loop control"},
		{"c", "run command…"},
		{"o", "observe"},
		{"R", "review results"},
	},
	ViewQueue: {
		{"a", "add task"},
		{"c", "run command…"},
		{"r", "retry task"},
		{"x", "delete task"},
		{"m", "mark done"},
		{"b", "block task"},
		{"e", "edit spec ($EDITOR)"},
		{"y", "copy task id"},
		{"f", "cycle sort order"},
	},
	ViewTaskDetail: {
		{"r", "retry task"},
		{"b", "block task"},
		{"m", "mark done"},
		{"x", "delete task"},
		{"y", "copy task id"},
	},
	ViewSessions: {
		{"y", "copy session id"},
		{"c", "run command…"},
	},
	ViewLiveOutput: {
		{"s", "stop after current"},
		{"k", "kill (SIGTERM)"},
		{"r", "restart loop"},
		{"f", "freeze scroll"},
		{"w", "wrap lines"},
		{"C", "clear output"},
	},
}

// renderHelp renders the view-aware Help overlay as a two-column keybind sheet.
func (m *Model) renderHelp(frameW, frameH int) string {
	w := modalWidth(frameW, 72)
	colW := (w - 6) / 2

	actions := actionHelp[m.view]
	rows := maxInt(len(navHelp), len(actions))

	var b strings.Builder
	b.WriteString(" " + fitLine(m.theme.Bold.Render("Navigation"), colW) +
		m.theme.Bold.Render("Actions") + "\n")

	for i := 0; i < rows; i++ {
		left := ""
		if i < len(navHelp) {
			left = m.helpRow(navHelp[i], colW)
		}
		right := ""
		if i < len(actions) {
			right = m.helpRow(actions[i], colW)
		}
		b.WriteString(" " + fitLine(left, colW) + right + "\n")
	}

	b.WriteString("\n " + m.theme.Bold.Render("Global views") + "\n")
	var views strings.Builder
	for i, v := range tabs {
		fmt.Fprintf(&views, " %s %s ", m.theme.StatusKey.Render(itoa(i+1)), v.String())
	}
	b.WriteString(" " + m.theme.StatusText.Render(views.String()) + "\n\n")
	b.WriteString(" " + m.theme.Dim.Render("Press any key to close"))

	return m.theme.modalBox("Help ("+m.view.String()+" view)", b.String(), w)
}

// helpRow formats one "key  description" pair.
func (m *Model) helpRow(e helpEntry, w int) string {
	key := m.theme.StatusKey.Render(fitLine(m.theme.icon(e.key), 10))
	return key + m.theme.StatusText.Render(truncate(e.desc, maxInt(4, w-12)))
}
