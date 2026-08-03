package tui

// renderHelp renders the view-aware Help overlay.
func (m *Model) renderHelp() string {
	return m.theme.Header.Render("Help ("+m.view.String()+" view)") + `

Navigation                 Actions
j / down    move down      a   add task
k / up      move up        r   retry task
g           top            x   delete task
G           bottom         m   mark done
Enter       select/drill   b   block task
Esc         back/close     c   close task
/           search/filter  l   loop control
q           quit

Global Views
1 dashboard  2 queue  3 task detail  4 sessions  5 live

Press any key to close`
}
