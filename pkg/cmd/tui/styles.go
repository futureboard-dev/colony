package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Theme holds the resolved color profile plus prebuilt styles. When
// monochrome is forced (--no-color, NO_COLOR, or TERM=dumb), colors are
// disabled and accessibility is carried by icons, bold, and reverse-video.
type Theme struct {
	Monochrome bool

	Title    lipgloss.Style
	Subtitle lipgloss.Style
	Header   lipgloss.Style
	Accent   lipgloss.Style
	Border   lipgloss.Style
	Dim      lipgloss.Style
	Bold     lipgloss.Style
	Selected lipgloss.Style
	Help     lipgloss.Style
	Error    lipgloss.Style
	ToastOK  lipgloss.Style
	ToastErr lipgloss.Style
}

// State theme colors, dual-encoded with icons.
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
	iconWarn       = "▲"
	iconReviewPass = "✓"
	iconReviewWarn = "▲"
	iconReviewFail = "✗"
)

// DefaultTheme builds a theme honoring monochrome detection order:
// NO_COLOR wins over --no-color; --no-color wins over auto-detection.
func DefaultTheme(noColor bool) *Theme {
	mono := noColor
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		mono = true
	}

	t := &Theme{Monochrome: mono}
	if mono {
		t.Title = lipgloss.NewStyle().Bold(true)
		t.Subtitle = lipgloss.NewStyle().Faint(true)
		t.Header = lipgloss.NewStyle().Bold(true).Underline(true)
		t.Accent = lipgloss.NewStyle().Bold(true)
		t.Border = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1)
		t.Dim = lipgloss.NewStyle().Faint(true)
		t.Bold = lipgloss.NewStyle().Bold(true)
		t.Selected = lipgloss.NewStyle().Bold(true).Reverse(true)
		t.Help = lipgloss.NewStyle().Faint(true)
		t.Error = lipgloss.NewStyle().Bold(true).Reverse(true)
		t.ToastOK = lipgloss.NewStyle().Bold(true)
		t.ToastErr = lipgloss.NewStyle().Bold(true).Reverse(true)
		return t
	}

	t.Title = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	t.Subtitle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	t.Header = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	t.Accent = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	t.Border = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, true, false).
		BorderForeground(lipgloss.Color("236"))
	t.Dim = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	t.Bold = lipgloss.NewStyle().Bold(true)
	t.Selected = lipgloss.NewStyle().Bold(true).Background(lipgloss.Color("240")).
		Foreground(lipgloss.Color("255"))
	t.Help = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	t.Error = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
	t.ToastOK = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2")).
		Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("2")).Padding(0, 1)
	t.ToastErr = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1")).
		Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("1")).Padding(0, 1)
	return t
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
		return "3"
	case "blocked":
		return "1"
	case "done":
		return "2"
	case "in-progress", "building":
		return "6"
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
