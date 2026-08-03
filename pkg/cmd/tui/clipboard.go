package tui

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// ClipboardFallback is the ordering strategy used when writing to the system
// clipboard: tmux set-buffer → OSC 52 → inline (last resort).
type ClipboardFallback int

const (
	// ClipboardTMUX tries the tmux set-buffer escape when inside tmux.
	ClipboardTMUX ClipboardFallback = iota
	// ClipboardOSC52 writes the OSC 52 terminal escape sequence.
	ClipboardOSC52
	// ClipboardInline makes the text available only inline (no real copy).
	ClipboardInline
)

// ErrClipboardUnavailable is returned when no clipboard mechanism is available.
var ErrClipboardUnavailable = errors.New("clipboard unavailable: no tmux and no OSC 52 support")

// CopyResult reports which fallback wrote the value to the clipboard.
type CopyResult struct {
	Fallback   ClipboardFallback
	InlineText string
}

// Clipboard writes text using the configured fallback order. When inside tmux,
// it shells out to `tmux set-buffer`; otherwise it emits the OSC 52 escape
// sequence. If neither is usable it returns ErrClipboardUnavailable so the UI
// can show a toast.
func Clipboard(text string) (CopyResult, error) {
	return clipboardWith(func() bool { return os.Getenv("TMUX") != "" },
		func(b []byte) error { _, err := os.Stdout.Write(b); return err },
		func(buf string) error { return exec.Command("tmux", "set-buffer", buf).Run() },
		text)
}

// clipboardWith is the testable core of Clipboard. The tmuxEnabled predicate,
// stdout writer, and tmux command runner are injected.
func clipboardWith(tmuxEnabled func() bool, write func([]byte) error, tmuxRun func(string) error, text string) (CopyResult, error) {
	if tmuxEnabled() {
		if err := tmuxRun(text); err == nil {
			return CopyResult{Fallback: ClipboardTMUX}, nil
		}
	}

	if err := write([]byte(osc52(text))); err == nil {
		return CopyResult{Fallback: ClipboardOSC52}, nil
	}

	return CopyResult{Fallback: ClipboardInline, InlineText: text}, ErrClipboardUnavailable
}

// osc52 builds the OSC 52 terminal control sequence to copy text.
func osc52(text string) string {
	return fmt.Sprintf("\x1b]52;c;%s\x07", b64(text))
}

// b64 base64-encodes text for embedding in an OSC 52 sequence.
func b64(text string) string {
	return base64.StdEncoding.EncodeToString([]byte(text))
}

// FallbackName returns a human-readable label for a fallback.
func (c ClipboardFallback) String() string {
	switch c {
	case ClipboardTMUX:
		return "tmux"
	case ClipboardOSC52:
		return "OSC 52"
	case ClipboardInline:
		return "inline"
	default:
		return "unknown"
	}
}
