package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// scheduleSpecIndex locates the "loop schedule start" command so the Schedule
// modal can hand off to the palette form.
func scheduleSpecIndex() int {
	for i, spec := range paletteCommands {
		if spec.Name == "loop schedule start" {
			return i
		}
	}
	return 0
}

// handlePaletteKey routes keys for both palette phases: the command chooser and
// the flag form.
func (m *Model) handlePaletteKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.palette.picking {
		return m.handlePalettePickerKey(key)
	}
	return m.handlePaletteFormKey(key)
}

// handlePalettePickerKey drives the command chooser.
func (m *Model) handlePalettePickerKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc", "q":
		m.modal = ModalNone
	case "j", "down":
		if m.palette.cursor < len(paletteCommands)-1 {
			m.palette.cursor++
		}
	case "k", "up":
		if m.palette.cursor > 0 {
			m.palette.cursor--
		}
	case "g":
		m.palette.cursor = 0
	case "G":
		m.palette.cursor = len(paletteCommands) - 1
	case "enter":
		m.selectPaletteCommand(m.palette.cursor)
	}
	return m, nil
}

// handlePaletteFormKey drives the flag form: navigation, text entry, boolean
// toggles, and submission.
func (m *Model) handlePaletteFormKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	spec := paletteCommands[m.palette.spec]
	p := &m.palette
	count := p.fieldCount(spec)

	switch key.String() {
	case "esc":
		// Back to the chooser rather than closing outright, so a mistyped
		// command selection costs one keystroke instead of reopening.
		m.palette = paletteState{picking: true, cursor: p.spec}
		return m, nil
	case "enter":
		return m, m.runPaletteCommand()
	case "tab", "down":
		if count > 0 {
			p.focus = (p.focus + 1) % count
		}
		p.scrollToFocus()
		return m, nil
	case "shift+tab", "up":
		if count > 0 {
			p.focus = (p.focus + count - 1) % count
		}
		p.scrollToFocus()
		return m, nil
	}

	idx, isFlag := p.flagIndex(spec, p.focus)
	if isFlag && spec.Flags[idx].Kind == flagBool {
		if key.String() == " " {
			p.bools[idx] = !p.bools[idx]
			p.err = ""
		}
		return m, nil
	}

	// Text field: the positional argument or a string flag.
	target := &p.posValue
	if isFlag {
		target = &p.values[idx]
	}
	switch key.Type {
	case tea.KeyBackspace:
		if n := len(*target); n > 0 {
			*target = (*target)[:n-1]
			p.err = ""
		}
	case tea.KeyRunes, tea.KeySpace:
		if len(key.Runes) > 0 {
			*target += string(key.Runes)
		} else {
			*target += " "
		}
		p.err = ""
	}
	return m, nil
}

// scrollToFocus keeps the focused row inside the visible window.
func (p *paletteState) scrollToFocus() {
	if p.focus < p.scroll {
		p.scroll = p.focus
	}
	if p.focus >= p.scroll+paletteFormRows {
		p.scroll = p.focus - paletteFormRows + 1
	}
}
