package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Theme holds the resolved color profile, the box-drawing character set, and
// prebuilt styles. When monochrome is forced (--no-color, NO_COLOR, or
// TERM=dumb), colors are disabled and accessibility is carried by icons, bold,
// and reverse-video.
type Theme struct {
	Monochrome bool
	borders    borderSet

	Title    lipgloss.Style
	Subtitle lipgloss.Style
	Header   lipgloss.Style
	Accent   lipgloss.Style
	Dim      lipgloss.Style
	Bold     lipgloss.Style
	Selected lipgloss.Style
	Help     lipgloss.Style
	Error    lipgloss.Style
	ToastOK  lipgloss.Style
	ToastErr lipgloss.Style

	// Chrome
	PaneBorder      lipgloss.Style
	PaneBorderFocus lipgloss.Style
	PaneTitle       lipgloss.Style
	TabActive       lipgloss.Style
	TabInactive     lipgloss.Style
	StatusKey       lipgloss.Style
	StatusText      lipgloss.Style
	Banner          lipgloss.Style
	ModalBorder     lipgloss.Style
	FieldFocus      lipgloss.Style
	Field           lipgloss.Style

	// State styles, dual-encoded with icons.
	StateOpen     lipgloss.Style
	StateProgress lipgloss.Style
	StateNeedsFix lipgloss.Style
	StateBlocked  lipgloss.Style
	StateDone     lipgloss.Style
	VerdictPass   lipgloss.Style
	VerdictWarn   lipgloss.Style
	VerdictFail   lipgloss.Style
}

// State and status icons.
const (
	iconOpen       = "○"
	iconProgress   = "◷"
	iconNeedsFix   = "◉"
	iconBlocked    = "⊘"
	iconDone       = "●"
	iconRunning    = "●"
	iconStopping   = "◷"
	iconIdle       = "○"
	iconCrashed    = "✗"
	iconApproved   = "✓"
	iconRejected   = "✗"
	iconReviewPass = "✓"
	iconReviewWarn = "▲"
	iconReviewFail = "✗"
)

// asciiIcons is the fallback map for terminals without reliable Unicode.
var asciiIcons = map[string]string{
	"○": "o", "◷": "*", "◉": "!", "⊘": "x", "●": "+",
	"✗": "x", "✓": "v", "▲": "!", "▶": ">", "↓": "v",
	"…": "...", "—": "-", "·": ".",
}

// unicodeOK reports whether the terminal advertises a UTF-8 locale. Box drawing
// and icons fall back to ASCII when it doesn't.
func unicodeOK() bool {
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := os.Getenv(k); v != "" {
			up := strings.ToUpper(v)
			return strings.Contains(up, "UTF-8") || strings.Contains(up, "UTF8")
		}
	}
	// No locale set at all (common in CI); assume a modern terminal.
	return true
}

// DefaultTheme builds a theme honoring monochrome detection order:
// NO_COLOR wins over --no-color; --no-color wins over auto-detection.
func DefaultTheme(noColor bool) *Theme {
	mono := noColor
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		mono = true
	}

	t := &Theme{Monochrome: mono, borders: unicodeBorders}
	if !unicodeOK() {
		t.borders = asciiBorders
	}

	if mono {
		plain := lipgloss.NewStyle()
		bold := lipgloss.NewStyle().Bold(true)
		faint := lipgloss.NewStyle().Faint(true)

		t.Title, t.Subtitle, t.Header, t.Accent = bold, faint, bold.Underline(true), bold
		t.Dim, t.Bold, t.Help = faint, bold, faint
		t.Selected = bold.Reverse(true)
		t.Error = bold.Reverse(true)
		t.ToastOK, t.ToastErr = bold, bold.Reverse(true)

		t.PaneBorder, t.PaneBorderFocus, t.ModalBorder = faint, bold, bold
		t.PaneTitle = bold
		t.TabActive, t.TabInactive = bold.Reverse(true), faint
		t.StatusKey, t.StatusText = bold, faint
		t.Banner = bold.Reverse(true)
		t.FieldFocus, t.Field = bold.Reverse(true), plain

		t.StateOpen, t.StateProgress = faint, plain
		t.StateNeedsFix, t.StateBlocked, t.StateDone = bold, bold.Underline(true), plain
		t.VerdictPass, t.VerdictWarn, t.VerdictFail = plain, bold, bold.Underline(true)
		return t
	}

	t.Title = lipgloss.NewStyle().Bold(true).Foreground(c("212"))
	t.Subtitle = lipgloss.NewStyle().Foreground(c("245"))
	t.Header = lipgloss.NewStyle().Bold(true).Foreground(c("212"))
	t.Accent = lipgloss.NewStyle().Bold(true).Foreground(c("212"))
	t.Dim = lipgloss.NewStyle().Foreground(c("243"))
	t.Bold = lipgloss.NewStyle().Bold(true)
	t.Selected = lipgloss.NewStyle().Bold(true).Background(c("238")).Foreground(c("231"))
	t.Help = lipgloss.NewStyle().Foreground(c("245"))
	t.Error = lipgloss.NewStyle().Bold(true).Foreground(c("203"))
	t.ToastOK = lipgloss.NewStyle().Foreground(c("114"))
	t.ToastErr = lipgloss.NewStyle().Foreground(c("203"))

	t.PaneBorder = lipgloss.NewStyle().Foreground(c("240"))
	t.PaneBorderFocus = lipgloss.NewStyle().Foreground(c("212"))
	t.PaneTitle = lipgloss.NewStyle().Bold(true).Foreground(c("252"))
	t.ModalBorder = lipgloss.NewStyle().Foreground(c("212"))
	t.TabActive = lipgloss.NewStyle().Bold(true).Foreground(c("232")).Background(c("212"))
	t.TabInactive = lipgloss.NewStyle().Foreground(c("245"))
	t.StatusKey = lipgloss.NewStyle().Bold(true).Foreground(c("212"))
	t.StatusText = lipgloss.NewStyle().Foreground(c("245"))
	t.Banner = lipgloss.NewStyle().Bold(true).Foreground(c("231")).Background(c("203"))
	t.FieldFocus = lipgloss.NewStyle().Foreground(c("231")).Background(c("238"))
	t.Field = lipgloss.NewStyle().Foreground(c("250"))

	t.StateOpen = lipgloss.NewStyle().Foreground(c("250"))
	t.StateProgress = lipgloss.NewStyle().Foreground(c("117"))
	t.StateNeedsFix = lipgloss.NewStyle().Foreground(c("221"))
	t.StateBlocked = lipgloss.NewStyle().Foreground(c("203"))
	t.StateDone = lipgloss.NewStyle().Foreground(c("114"))
	t.VerdictPass = t.StateDone
	t.VerdictWarn = t.StateNeedsFix
	t.VerdictFail = t.StateBlocked
	return t
}

// c is a shorthand for an ANSI/hex color value.
func c(v string) lipgloss.Color { return lipgloss.Color(v) }

// icon downgrades a Unicode glyph to its ASCII stand-in when the terminal
// can't render it.
func (t *Theme) icon(glyph string) string {
	if t.borders.TL == unicodeBorders.TL {
		return glyph
	}
	if alt, ok := asciiIcons[glyph]; ok {
		return alt
	}
	return glyph
}

// StateIcon returns the dual-encoded icon for a task state.
func StateIcon(state string) string {
	switch state {
	case "open":
		return iconOpen
	case "in-progress", "building":
		return iconProgress
	case "needs-fix":
		return iconNeedsFix
	case "blocked":
		return iconBlocked
	case "done":
		return iconDone
	default:
		return "?"
	}
}

// StateStyle returns the style for a task state. Monochrome themes carry the
// signal with weight and the icon instead of hue.
func (t *Theme) StateStyle(state string) lipgloss.Style {
	switch state {
	case "open":
		return t.StateOpen
	case "in-progress", "building":
		return t.StateProgress
	case "needs-fix":
		return t.StateNeedsFix
	case "blocked":
		return t.StateBlocked
	case "done":
		return t.StateDone
	default:
		return t.Dim
	}
}

// StateColor returns a lipgloss ANSI color for a state. Monochrome mode uses
// no color (icons + weight carry the signal).
func StateColor(state string, theme *Theme) string {
	if theme.Monochrome {
		return ""
	}
	switch state {
	case "open":
		return "250"
	case "needs-fix":
		return "221"
	case "blocked":
		return "203"
	case "done":
		return "114"
	case "in-progress", "building":
		return "117"
	default:
		return ""
	}
}

// LoopStatusLabel normalizes a raw loop status string to a stable label.
func LoopStatusLabel(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "running":
		return "running"
	case "stopping", "stop", "stopped":
		return "stopping"
	case "idle", "":
		return "idle"
	case "crashed", "crash":
		return "crashed"
	default:
		return "idle"
	}
}

// LoopStatusIcon returns the icon for a loop status label.
func LoopStatusIcon(label string) string {
	switch label {
	case "running":
		return iconRunning
	case "stopping":
		return iconStopping
	case "crashed":
		return iconCrashed
	default:
		return iconIdle
	}
}

// LoopStatusStyle returns the style for a loop status label.
func (t *Theme) LoopStatusStyle(label string) lipgloss.Style {
	switch label {
	case "running":
		return t.StateDone
	case "stopping":
		return t.StateNeedsFix
	case "crashed":
		return t.StateBlocked
	default:
		return t.Dim
	}
}

// gateNotAvailable is the graceful-degradation message rendered for
// pre-migration rows whose steps.output is empty.
const gateNotAvailable = "(gate output not available — recorded before per-step feedback was added)"

// RenderGateOutput renders a step's stored output; empty output (pre-migration
// or never captured) is replaced with the graceful-degradation note.
func RenderGateOutput(output string) string {
	if strings.TrimSpace(output) == "" {
		return gateNotAvailable
	}
	return output
}
