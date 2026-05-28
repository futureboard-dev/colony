package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestStripANSI(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"\033[32mhello\033[0m", "hello"},
		{"\033[90m━━━\033[0m", "━━━"},
		{"\033[1A\r\033[Kline", "\rline"},
		{"no ansi here", "no ansi here"},
		{"multi\033[32mline\033[0manymore", "multilineanymore"},
	}
	for _, tc := range tests {
		got := stripANSI(tc.input)
		if got != tc.expected {
			t.Errorf("stripANSI(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestHeatmapColor(t *testing.T) {
	tests := []struct {
		line     string
		wantGray bool
	}{
		{"✓ Worktree ready", false},    // success → green
		{"✗ Type check failed", false}, // error → amber
		{"▶ STEP 1/4 Setup", false},    // step → blue
		{"Scanning directory...", true},
		{"Installing dependencies...", true},
		{"→ Write main.go", false},   // file change → cyan
		{"⚠ fix agent error", false}, // error → amber
		{"Setting up worktree", true},
	}
	for _, tc := range tests {
		got := heatmapColor(tc.line)
		isGray := got == ansiDimGray
		if isGray != tc.wantGray {
			t.Errorf("heatmapColor(%q) = %q (gray=%v), want gray=%v", tc.line, got, isGray, tc.wantGray)
		}
	}
}

func TestHeatmapWriter(t *testing.T) {
	var buf bytes.Buffer
	hw := NewHeatmapWriter(&buf)
	input := "✓ passed\n✗ failed\n▶ step\n"
	n, err := hw.Write([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if n != len(input) {
		t.Errorf("Write returned %d, want %d", n, len(input))
	}
	output := buf.String()
	// Should contain ANSI codes.
	if !strings.Contains(output, "\033[") {
		t.Error("heatmap output missing ANSI codes")
	}
	// Should contain the original text.
	if !strings.Contains(output, "✓ passed") {
		t.Error("heatmap output missing original text")
	}
	if !strings.Contains(output, "✗ failed") {
		t.Error("heatmap output missing original text")
	}
}

func TestHeatmapWriterStripsExistingANSI(t *testing.T) {
	var buf bytes.Buffer
	hw := NewHeatmapWriter(&buf)
	// Input already has green ANSI codes.
	input := "\033[32m✓ passed\033[0m\n"
	n, err := hw.Write([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	_ = n
	output := buf.String()
	// Output should contain heatmap color (bright green, not plain green).
	if !strings.Contains(output, ansiBrightGreen) {
		t.Errorf("expected heatmap bright green, got: %q", output)
	}
}

func TestStatusLineNonTerminal(t *testing.T) {
	// Without a real terminal, StatusLine should pass through.
	var buf bytes.Buffer
	sl := NewStatusLine(&buf, nil)
	input := "hello\nworld\n"
	n, err := sl.Write([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if n != len(input) {
		t.Errorf("Write returned %d, want %d", n, len(input))
	}
	if buf.String() != input {
		t.Errorf("non-terminal pass-through: got %q, want %q", buf.String(), input)
	}
}

func TestStatusLineSetState(t *testing.T) {
	var buf bytes.Buffer
	sl := NewStatusLine(&buf, nil)
	// SetState on non-terminal should not write anything (no terminal, no written yet).
	sl.SetState(StateThinking)
	if buf.Len() != 0 {
		t.Errorf("SetState on non-terminal should be no-op, wrote: %q", buf.String())
	}
}

func TestStateColorCode(t *testing.T) {
	tests := []struct {
		state string
		color string
	}{
		{StateIdle, ansiDimGray},
		{StateThinking, ansiElectricBlue},
		{StateWorking, ansiBrightGreen},
		{"unknown", ansiDimGray},
	}
	for _, tc := range tests {
		got := stateColorCode(tc.state)
		if got != tc.color {
			t.Errorf("stateColorCode(%q) = %q, want %q", tc.state, got, tc.color)
		}
	}
}

func TestHeatmapAllLineTypes(t *testing.T) {
	var buf bytes.Buffer
	hw := NewHeatmapWriter(&buf)

	lines := []string{
		"✓ Worktree ready",
		"✗ Type check failed",
		"▶ STEP 1/4 Setup",
		"Scanning directory...",
		"→ Write main.go",
		"⚠ fix agent error",
		"Installing deps...",
	}
	var input string
	for _, l := range lines {
		input += l + "\n"
	}
	hw.Write([]byte(input))

	output := buf.String()
	// Each line should have a color code before it.
	for _, l := range lines {
		if !strings.Contains(output, l) {
			t.Errorf("output missing line: %s", l)
		}
	}
}
