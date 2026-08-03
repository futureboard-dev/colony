package tui

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestClipboardFallbackOrder(t *testing.T) {
	t.Run("tmux buffer preferred", func(t *testing.T) {
		res, err := clipboardWith(
			func() bool { return true },
			func(b []byte) error { t.Fatal("tmux present: OSC 52 should not be written"); return nil },
			func(buf string) error { return nil },
			"hello",
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Fallback != ClipboardTMUX {
			t.Errorf("expected tmux fallback, got %s", res.Fallback)
		}
	})

	t.Run("falls back to OSC 52 when tmux missing", func(t *testing.T) {
		var written string
		res, err := clipboardWith(
			func() bool { return false },
			func(b []byte) error { written = string(b); return nil },
			func(buf string) error { return nil },
			"data",
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Fallback != ClipboardOSC52 {
			t.Errorf("expected OSC 52 fallback, got %s", res.Fallback)
		}
		if !strings.Contains(written, "\x1b]52;c;") {
			t.Errorf("expected OSC 52 escape sequence, got %q", written)
		}
	})

	t.Run("tmux fails then OSC 52 used", func(t *testing.T) {
		var written string
		res, err := clipboardWith(
			func() bool { return true },
			func(b []byte) error { written = string(b); return nil },
			func(buf string) error { return errors.New("tmux not installed") },
			"fallback",
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Fallback != ClipboardOSC52 {
			t.Errorf("expected OSC 52 after tmux failure, got %s", res.Fallback)
		}
		_ = written
	})

	t.Run("nothing available yields inline and error", func(t *testing.T) {
		res, err := clipboardWith(
			func() bool { return false },
			func(b []byte) error { return errors.New("no stdout") },
			func(buf string) error { return nil },
			"lost",
		)
		if !errors.Is(err, ErrClipboardUnavailable) {
			t.Errorf("expected ErrClipboardUnavailable, got %v", err)
		}
		if res.Fallback != ClipboardInline {
			t.Errorf("expected inline fallback, got %s", res.Fallback)
		}
		if res.InlineText != "lost" {
			t.Errorf("expected inline text preserved, got %q", res.InlineText)
		}
	})
}

func TestOSC52Encoding(t *testing.T) {
	in := "copy me"
	seq := osc52(in)
	// Extract base64 payload between "c;" and the BEL.
	payload := strings.TrimPrefix(seq, "\x1b]52;c;")
	payload = strings.TrimSuffix(payload, "\x07")
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("payload was not valid base64: %v", err)
	}
	if string(decoded) != in {
		t.Errorf("osc52 round-trip mismatch: got %q want %q", decoded, in)
	}
}

func TestFallbackName(t *testing.T) {
	if ClipboardTMUX.String() != "tmux" {
		t.Errorf("unexpected tmux name: %s", ClipboardTMUX)
	}
	if ClipboardOSC52.String() != "OSC 52" {
		t.Errorf("unexpected OSC52 name: %s", ClipboardOSC52)
	}
	if ClipboardInline.String() != "inline" {
		t.Errorf("unexpected inline name: %s", ClipboardInline)
	}
}
