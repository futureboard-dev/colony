// Package output provides terminal output formatting with a status ribbon and
// heatmap-style color coding for the Colony CLI.
package output

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/mattn/go-isatty"
)

// Heatmap ANSI color codes.
const (
	ansiReset        = "\033[0m"
	ansiDimGray      = "\033[90m"
	ansiElectricBlue = "\033[94m"
	ansiBrightGreen  = "\033[92m"
	ansiCyan         = "\033[36m"
	ansiAmber        = "\033[93m"
)

// Colony states for the status ribbon.
const (
	StateIdle     = "Idle"
	StateThinking = "Thinking"
	StateWorking  = "Working"
)

// HeatmapWriter wraps an io.Writer and colorizes each line based on its content
// using a heatmap-style scheme:
//
//	Dim Gray:      background tasks (scanning, installing, setup)
//	Electric Blue: reasoning, planning (thoughts, step headers)
//	Bright Green:  success, completion (✓, passed, ready)
//	Amber:         self-correction, errors (✗, failed, retry)
//	Cyan:          file changes, commands (→, ✎)
type HeatmapWriter struct {
	output io.Writer
	mu     sync.Mutex
}

// NewHeatmapWriter creates a HeatmapWriter wrapping the given output.
func NewHeatmapWriter(w io.Writer) *HeatmapWriter {
	return &HeatmapWriter{output: w}
}

func (hw *HeatmapWriter) Write(p []byte) (int, error) {
	hw.mu.Lock()
	defer hw.mu.Unlock()
	return hw.writeLocked(p)
}

func (hw *HeatmapWriter) writeLocked(p []byte) (int, error) {
	// Strip existing ANSI codes, then colorize by heatmap.
	raw := stripANSI(string(p))
	lines := strings.Split(raw, "\n")
	var buf bytes.Buffer
	for i, line := range lines {
		if line == "" {
			if i < len(lines)-1 {
				buf.WriteByte('\n')
			}
			continue
		}
		color := heatmapColor(line)
		buf.WriteString(color)
		buf.WriteString(line)
		buf.WriteString(ansiReset)
		if i < len(lines)-1 {
			buf.WriteByte('\n')
		}
	}
	_, err := io.Copy(hw.output, &buf)
	return len(p), err
}

// heatmapColor returns the ANSI color escape for a line based on its content.
func heatmapColor(line string) string {
	trimmed := strings.TrimSpace(line)

	// Error / self-correction — high priority.
	if strings.Contains(trimmed, "✗") || strings.Contains(trimmed, "failed") ||
		strings.HasPrefix(trimmed, "Error") || strings.Contains(trimmed, "⚠") ||
		strings.Contains(trimmed, "attempt") || strings.Contains(trimmed, "BLOCKED") {
		return ansiAmber
	}

	// Success / completion.
	if strings.Contains(trimmed, "✓") || strings.HasPrefix(trimmed, "✓") ||
		strings.Contains(trimmed, "passed") || strings.Contains(trimmed, "Ready") ||
		strings.Contains(trimmed, "complete") || strings.Contains(trimmed, "COMPLETE") ||
		strings.Contains(trimmed, "APPROVED") {
		return ansiBrightGreen
	}

	// File changes / tool actions (→, ✎, ·, ◔)
	if strings.HasPrefix(trimmed, "→") || strings.HasPrefix(trimmed, "✎") ||
		strings.HasPrefix(trimmed, "·") || strings.HasPrefix(trimmed, "◔") {
		return ansiCyan
	}

	// Code/command output (indented lines from agent tool results).
	if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "$ ") {
		return ansiDimGray
	}

	// Step headers / reasoning.
	if strings.HasPrefix(trimmed, "▶") || strings.HasPrefix(trimmed, "STEP") ||
		strings.HasPrefix(trimmed, "🔄") || strings.HasPrefix(trimmed, "──") ||
		strings.HasPrefix(trimmed, "━━") || strings.Contains(trimmed, "Coordinator") ||
		strings.Contains(trimmed, "Scout") || strings.Contains(trimmed, "Build") ||
		strings.Contains(trimmed, "Review") {
		return ansiElectricBlue
	}

	// Background / informational.
	if strings.Contains(trimmed, "Scanning") || strings.Contains(trimmed, "Installing") ||
		strings.Contains(trimmed, "Setting up") || strings.Contains(trimmed, "worktree") ||
		strings.Contains(trimmed, "Subtask") || strings.Contains(trimmed, "subtask") ||
		strings.Contains(trimmed, "dependencies") || strings.Contains(trimmed, "Setup") {
		return ansiDimGray
	}

	return ansiDimGray
}

// stripANSI removes ANSI escape sequences from a string.
func stripANSI(s string) string {
	var buf strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\033' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) {
				c := s[j]
				if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
					break
				}
				j++
			}
			if j < len(s) {
				i = j // skip terminator (will be incremented by loop)
				continue
			}
		}
		buf.WriteByte(s[i])
	}
	return buf.String()
}

// StatusLine manages a persistent status ribbon at the bottom of the terminal.
// Content is written above the ribbon, and the ribbon is redrawn after each
// complete line (content ending in \n).
type StatusLine struct {
	output  io.Writer // raw terminal output (for cursor codes)
	content io.Writer // writer for content (usually a HeatmapWriter)
	mu      sync.Mutex
	state   string
	msg     string // optional secondary message

	written   bool
	lastLines int // number of terminal lines from the previous Write

	terminal bool
}

// NewStatusLine creates a StatusLine that writes to out. If out is a terminal,
// the status ribbon will be managed. Pass heatmap as the content writer, or nil
// to use out directly.
func NewStatusLine(out io.Writer, heatmap io.Writer) *StatusLine {
	isTerm := false
	if f, ok := out.(*os.File); ok {
		isTerm = isatty.IsTerminal(f.Fd())
	}
	content := heatmap
	if content == nil {
		content = out
	}
	return &StatusLine{
		output:   out,
		content:  content,
		state:    StateIdle,
		terminal: isTerm,
	}
}

// Write implements io.Writer. It writes content above the status ribbon.
func (sl *StatusLine) Write(p []byte) (int, error) {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	if !sl.terminal || len(p) == 0 {
		return sl.output.Write(p)
	}

	// Partial line (streaming) — overwrite status bar position and do not redraw.
	if p[len(p)-1] != '\n' {
		// Clear the current line (status bar) and write streaming content in its place.
		sl.output.Write([]byte("\r\033[K"))
		sl.output.Write(p)
		return len(p), nil
	}

	contentLines := bytes.Count(p, []byte{'\n'})

	// Move up past the status bar and previous content lines.
	if sl.written {
		for i := 0; i < 1+sl.lastLines; i++ {
			sl.output.Write([]byte("\033[A\r\033[K"))
		}
	}

	// Write content through the content writer (heatmap).
	n, err := sl.content.Write(p)
	if err != nil {
		return n, err
	}

	// Draw the status ribbon.
	sl.drawStatus()

	sl.written = true
	sl.lastLines = contentLines
	return n, nil
}

// SetState changes the colony state shown in the status ribbon.
func (sl *StatusLine) SetState(state string) {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.state = state
	if sl.terminal && sl.written {
		// Redraw in-place.
		sl.output.Write([]byte("\r\033[K"))
		sl.drawStatus()
	}
}

// SetMessage sets an optional secondary message in the status ribbon.
func (sl *StatusLine) SetMessage(msg string) {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.msg = msg
	if sl.terminal && sl.written {
		sl.output.Write([]byte("\r\033[K"))
		sl.drawStatus()
	}
}

// Close clears the status ribbon from the terminal.
func (sl *StatusLine) Close() {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	if sl.terminal && sl.written {
		// Clear the status bar line and move up past the content we wrote.
		for i := 0; i < 1+sl.lastLines; i++ {
			sl.output.Write([]byte("\033[A\r\033[K"))
		}
	}
}

// drawStatus writes the status ribbon line to the terminal.
func (sl *StatusLine) drawStatus() {
	stateColor := stateColorCode(sl.state)
	dot := stateColor + "●" + ansiReset
	label := sl.state
	if sl.msg != "" {
		label += "  │  " + sl.msg
	}
	// ─── Colony: ● Thinking ───
	bar := fmt.Sprintf("%s━━━ Colony: %s %s %s━━━%s",
		ansiDimGray, ansiReset, dot, stateColor+label+ansiDimGray, ansiReset,
	)
	sl.output.Write([]byte(bar + "\n"))
}

// stateColorCode returns the color for a colony state.
func stateColorCode(state string) string {
	switch state {
	case StateIdle:
		return ansiDimGray
	case StateThinking:
		return ansiElectricBlue
	case StateWorking:
		return ansiBrightGreen
	default:
		return ansiDimGray
	}
}
