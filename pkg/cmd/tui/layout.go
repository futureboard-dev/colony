package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Layout primitives. Everything the views draw goes through these so panes are
// sized to the terminal, clipped rather than wrapped, and composited (modals
// float above content instead of replacing it).

// borderSet is the box-drawing character set, swapped for ASCII when the
// terminal can't be trusted with Unicode.
type borderSet struct {
	TL, TR, BL, BR, H, V, LT, RT, TT, BT, X string
}

var unicodeBorders = borderSet{
	TL: "┌", TR: "┐", BL: "└", BR: "┘",
	H: "─", V: "│",
	LT: "├", RT: "┤", TT: "┬", BT: "┴", X: "┼",
}

var asciiBorders = borderSet{
	TL: "+", TR: "+", BL: "+", BR: "+",
	H: "-", V: "|",
	LT: "+", RT: "+", TT: "+", BT: "+", X: "+",
}

// pane draws a titled box of exactly w×h cells. The title is embedded in the
// top border (lazygit style); content is clipped to the interior, never wrapped
// past the edge. A focused pane gets a brighter border.
func (t *Theme) pane(title, content string, w, h int, focused bool) string {
	style := t.PaneBorder
	if focused {
		style = t.PaneBorderFocus
	}
	return t.paneStyled(style, title, content, w, h)
}

// modalBox draws a floating modal sized to its content.
func (t *Theme) modalBox(title, content string, w int) string {
	h := strings.Count(content, "\n") + 3
	return t.paneStyled(t.ModalBorder, title, content, w, h)
}

// paneStyled is the shared box renderer behind pane and modalBox.
func (t *Theme) paneStyled(style lipgloss.Style, title, content string, w, h int) string {
	if w < 4 || h < 2 {
		return ""
	}
	b := t.borders
	innerW, innerH := w-2, h-2

	// Top border with inline title: TL + H + " title " + fill + TR
	seg := ""
	if title != "" {
		seg = " " + ansi.Truncate(title, maxInt(0, innerW-4), "…") + " "
	}
	fill := innerW - 1 - ansi.StringWidth(seg)
	if fill < 0 {
		fill = 0
	}
	top := style.Render(b.TL+b.H) + t.PaneTitle.Render(seg) +
		style.Render(strings.Repeat(b.H, fill)+b.TR)

	lines := make([]string, 0, innerH)
	for _, line := range strings.Split(content, "\n") {
		if len(lines) == innerH {
			break
		}
		lines = append(lines, fitLine(line, innerW))
	}
	for len(lines) < innerH {
		lines = append(lines, strings.Repeat(" ", innerW))
	}

	var sb strings.Builder
	sb.WriteString(top)
	v := style.Render(b.V)
	for _, line := range lines {
		sb.WriteString("\n" + v + line + v)
	}
	sb.WriteString("\n" + style.Render(b.BL+strings.Repeat(b.H, innerW)+b.BR))
	return sb.String()
}

// fitLine clips a line to exactly w cells, padding short lines. ANSI-aware, so
// styled content keeps its escapes intact.
func fitLine(s string, w int) string {
	if w <= 0 {
		return ""
	}
	width := ansi.StringWidth(s)
	if width > w {
		return ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", w-width)
}

// joinH places blocks side by side. Blocks must already be equal height.
func joinH(blocks ...string) string {
	rows := make([][]string, 0, len(blocks))
	height := 0
	for _, b := range blocks {
		lines := strings.Split(b, "\n")
		rows = append(rows, lines)
		if len(lines) > height {
			height = len(lines)
		}
	}
	out := make([]string, height)
	for i := 0; i < height; i++ {
		var sb strings.Builder
		for _, lines := range rows {
			if i < len(lines) {
				sb.WriteString(lines[i])
			}
		}
		out[i] = sb.String()
	}
	return strings.Join(out, "\n")
}

// joinV stacks blocks vertically.
func joinV(blocks ...string) string {
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if b != "" {
			parts = append(parts, b)
		}
	}
	return strings.Join(parts, "\n")
}

// overlay splices a box into a base frame at (x, y), preserving the background
// around it. This is what makes modals and toasts float rather than replace.
func overlay(base, box string, x, y int) string {
	baseLines := strings.Split(base, "\n")
	boxLines := strings.Split(box, "\n")

	for i, boxLine := range boxLines {
		row := y + i
		if row < 0 || row >= len(baseLines) {
			continue
		}
		bg := baseLines[row]
		boxW := ansi.StringWidth(boxLine)

		left := ansi.Truncate(bg, x, "")
		if pad := x - ansi.StringWidth(left); pad > 0 {
			left += strings.Repeat(" ", pad)
		}
		right := ansi.TruncateLeft(bg, x+boxW, "")

		baseLines[row] = left + boxLine + right
	}
	return strings.Join(baseLines, "\n")
}

// overlayCenter floats a box in the middle of the frame.
func overlayCenter(base, box string, frameW, frameH int) string {
	boxW, boxH := blockSize(box)
	return overlay(base, box, maxInt(0, (frameW-boxW)/2), maxInt(0, (frameH-boxH)/2))
}

// blockSize measures a rendered block in cells.
func blockSize(s string) (w, h int) {
	lines := strings.Split(s, "\n")
	for _, line := range lines {
		if lw := ansi.StringWidth(line); lw > w {
			w = lw
		}
	}
	return w, len(lines)
}

// window returns the visible slice bounds for a cursor-following list of n rows
// in a viewport of the given height, keeping the cursor on screen.
func window(n, cursor, height int) (start, end int) {
	if height <= 0 || n == 0 {
		return 0, 0
	}
	if n <= height {
		return 0, n
	}
	start = cursor - height/2
	if start < 0 {
		start = 0
	}
	if start+height > n {
		start = n - height
	}
	return start, start + height
}

// scrollHint renders the "N more" affordance for a clipped region.
func scrollHint(theme *Theme, hidden int) string {
	if hidden <= 0 {
		return ""
	}
	return theme.Dim.Render("  ↓ " + itoa(hidden) + " more")
}

// splitW divides a width into two columns by a left-hand fraction, guaranteeing
// both columns stay drawable.
func splitW(total int, leftFrac float64) (left, right int) {
	left = int(float64(total) * leftFrac)
	if left < 20 {
		left = 20
	}
	if total-left < 20 {
		left = total - 20
	}
	if left < 0 {
		left = 0
	}
	return left, total - left
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// itoa avoids pulling strconv into the render path for a single conversion.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
