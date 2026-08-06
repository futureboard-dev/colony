package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// collectTailed drains n lines from the tailer, giving the poller time to see
// the writes.
func collectTailed(t *testing.T, tl *LogTailer, n int) []string {
	t.Helper()
	var got []string
	deadline := time.After(2 * time.Second)
	for len(got) < n {
		select {
		case line := <-tl.Lines():
			got = append(got, line)
		case <-deadline:
			t.Fatalf("timed out after %d lines: %v", len(got), got)
		}
	}
	return got
}

func TestLogTailerFollowsAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loop.log")
	if err := os.WriteFile(path, []byte("existing line\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tl := NewLogTailer(path)
	tl.interval = 10 * time.Millisecond
	go tl.Run()
	defer tl.Stop()

	if got := collectTailed(t, tl, 1); got[0] != "existing line" {
		t.Errorf("backlog line = %q, want %q", got[0], "existing line")
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("appended one\nappended two\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	got := collectTailed(t, tl, 2)
	if got[0] != "appended one" || got[1] != "appended two" {
		t.Errorf("appended lines = %v", got)
	}
}

func TestLogTailerWaitsForAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loop.log")

	tl := NewLogTailer(path)
	tl.interval = 10 * time.Millisecond
	go tl.Run()
	defer tl.Stop()

	time.Sleep(30 * time.Millisecond)
	if err := os.WriteFile(path, []byte("late start\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if got := collectTailed(t, tl, 1); got[0] != "late start" {
		t.Errorf("line = %q, want %q", got[0], "late start")
	}
}

func TestLogTailerRereadsATruncatedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loop.log")
	if err := os.WriteFile(path, []byte("before rotate\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tl := NewLogTailer(path)
	tl.interval = 10 * time.Millisecond
	go tl.Run()
	defer tl.Stop()
	collectTailed(t, tl, 1)

	if err := os.WriteFile(path, []byte("after rotate\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := collectTailed(t, tl, 1); got[0] != "after rotate" {
		t.Errorf("line = %q, want %q", got[0], "after rotate")
	}
}

func TestLogTailerTrimsTheBacklogToWholeLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loop.log")
	// A log larger than the backlog window: only its tail is replayed, and the
	// line the trim cut in half must not be emitted as a line of its own.
	old := strings.Repeat("x", logTailBacklogBytes) + "\ncomplete line\n"
	if err := os.WriteFile(path, []byte(old), 0644); err != nil {
		t.Fatal(err)
	}

	tl := NewLogTailer(path)
	tl.interval = 10 * time.Millisecond
	go tl.Run()
	defer tl.Stop()

	if got := collectTailed(t, tl, 1); got[0] != "complete line" {
		t.Errorf("first replayed line = %q, want the first whole line", got[0])
	}
}

func TestLogTailerStopIsIdempotent(t *testing.T) {
	tl := NewLogTailer(filepath.Join(t.TempDir(), "loop.log"))
	tl.Stop()
	tl.Stop()
}
