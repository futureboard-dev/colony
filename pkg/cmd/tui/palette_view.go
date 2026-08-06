package tui

import (
	"strings"
)

// paletteFormRows is the number of form rows visible at once. The form scrolls
// when a command has more flags than this, keeping the modal inside 80x24.
const paletteFormRows = 8

// renderPaletteModal renders either the command chooser or the flag form for
// the selected command.
func (m *Model) renderPaletteModal(frameW, frameH int) string {
	if m.palette.picking {
		return m.renderPalettePicker(frameW)
	}
	return m.renderPaletteForm(frameW)
}

// renderPalettePicker lists the runnable commands.
func (m *Model) renderPalettePicker(frameW int) string {
	w := modalWidth(frameW, 66)
	var b strings.Builder
	for i, spec := range paletteCommands {
		marker, style := "  ", m.theme.StatusText
		if i == m.palette.cursor {
			marker, style = m.theme.icon("▶")+" ", m.theme.Selected
		}
		b.WriteString(" ")
		b.WriteString(style.Render(fitLine(marker+spec.Name, 24)))
		b.WriteString(m.theme.Dim.Render(truncate(spec.Summary, w-30)))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(m.styleHints("[Enter] configure  [j/k] move  [Esc] cancel"))
	return m.theme.modalBox("Run Command", b.String(), w)
}

// renderPaletteForm renders the selected command's flags as a scrolling list
// with a live argv preview.
func (m *Model) renderPaletteForm(frameW int) string {
	w := modalWidth(frameW, 72)
	inner := w - 2
	spec := paletteCommands[m.palette.spec]
	p := &m.palette

	rows := m.paletteRows(spec)
	total := len(rows)
	start := p.scroll
	if start > maxInt(0, total-paletteFormRows) {
		start = maxInt(0, total-paletteFormRows)
	}
	end := minInt(total, start+paletteFormRows)

	var b strings.Builder
	if start > 0 {
		b.WriteString(" " + m.theme.Dim.Render(m.theme.icon("▲")+" more above") + "\n")
	}
	for i := start; i < end; i++ {
		b.WriteString(" " + fitLine(rows[i], inner-1) + "\n")
	}
	if end < total {
		b.WriteString(" " + m.theme.Dim.Render(m.theme.icon("↓")+" more below") + "\n")
	}

	b.WriteString("\n " + m.theme.Subtitle.Render("Command:") + "\n")
	b.WriteString(" " + m.theme.Accent.Render(truncate(p.commandLine(spec), inner-2)) + "\n")
	if p.err != "" {
		b.WriteString(" " + m.theme.Error.Render(m.theme.icon(iconCrashed)+" "+p.err) + "\n")
	}
	b.WriteString(m.styleHints("[Enter] run  [Tab] next  [Space] toggle  [Esc] back"))
	return m.theme.modalBox("Run: colony "+spec.Name, b.String(), w)
}

// paletteRows renders one display line per focusable form field.
func (m *Model) paletteRows(spec cmdSpec) []string {
	p := &m.palette
	rows := make([]string, 0, p.fieldCount(spec)+1)
	row := 0

	if spec.Positional != "" {
		rows = append(rows, m.paletteTextRow(spec.Positional, p.posValue,
			"<"+spec.Positional+">", true, p.focus == row))
		row++
	}
	for i, f := range spec.Flags {
		focused := p.focus == row
		if f.Kind == flagBool {
			rows = append(rows, m.paletteBoolRow(f, p.bools[i], focused))
		} else {
			rows = append(rows, m.paletteTextRow("--"+f.Name, p.values[i],
				f.Placeholder, f.Required, focused))
		}
		row++
	}
	return rows
}

// paletteTextRow formats "  --flag  [value]  help" for a string flag.
func (m *Model) paletteTextRow(label, value, placeholder string, required, focused bool) string {
	marker := "  "
	if focused {
		marker = m.theme.icon("▶") + " "
	}
	name := label
	if required {
		name += "*"
	}

	shown, style := value, m.theme.Field
	if shown == "" {
		shown, style = placeholder, m.theme.Dim
	}
	if focused {
		style = m.theme.FieldFocus
		shown = value + "_" // cursor
	}
	return marker + fitLine(m.theme.Bold.Render(name), 18) +
		style.Render(" "+truncate(shown, 34)+" ")
}

// paletteBoolRow formats "  [x] --flag  help" for a boolean flag.
func (m *Model) paletteBoolRow(f cmdFlag, on, focused bool) string {
	marker := "  "
	if focused {
		marker = m.theme.icon("▶") + " "
	}
	box := "[ ]"
	if on {
		box = "[x]"
	}
	style := m.theme.StatusText
	if focused {
		style = m.theme.FieldFocus
	}
	// Pad to the same column as the string rows so the two shapes line up.
	return marker + fitLine(style.Render(box+" --"+f.Name), 18) +
		m.theme.Dim.Render(truncate(f.Help, 30))
}
