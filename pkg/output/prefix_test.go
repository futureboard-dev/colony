package output

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// syncBuffer is a concurrency-safe io.Writer for tests that share one sink
// across multiple PrefixWriters.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestPrefixWriter(t *testing.T) {
	t.Run("prefixes each line", func(t *testing.T) {
		var buf bytes.Buffer
		w := NewPrefixWriter(&buf, "task-1")
		if _, err := w.Write([]byte("hello\nworld\n")); err != nil {
			t.Fatal(err)
		}
		want := "[task-1] hello\n[task-1] world\n"
		if buf.String() != want {
			t.Errorf("got %q, want %q", buf.String(), want)
		}
	})

	t.Run("buffers partial line", func(t *testing.T) {
		var buf bytes.Buffer
		w := NewPrefixWriter(&buf, "task-1")
		if _, err := w.Write([]byte("partial")); err != nil {
			t.Fatal(err)
		}
		if buf.String() != "" {
			t.Errorf("expected nothing forwarded yet, got %q", buf.String())
		}
		if _, err := w.Write([]byte(" line\n")); err != nil {
			t.Fatal(err)
		}
		want := "[task-1] partial line\n"
		if buf.String() != want {
			t.Errorf("got %q, want %q", buf.String(), want)
		}
	})

	t.Run("flushes on close", func(t *testing.T) {
		var buf bytes.Buffer
		w := NewPrefixWriter(&buf, "task-1")
		if _, err := w.Write([]byte("no newline yet")); err != nil {
			t.Fatal(err)
		}
		if buf.String() != "" {
			t.Errorf("expected nothing forwarded before Close, got %q", buf.String())
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		want := "[task-1] no newline yet\n"
		if buf.String() != want {
			t.Errorf("got %q, want %q", buf.String(), want)
		}
		// Closing again with nothing buffered is a no-op, not an error.
		if err := w.Close(); err != nil {
			t.Errorf("second Close returned an error: %v", err)
		}
	})

	t.Run("concurrent writers don't interleave within a line", func(t *testing.T) {
		sink := &syncBuffer{}
		const writers = 8
		const linesPerWriter = 50

		var wg sync.WaitGroup
		for wID := range writers {
			wg.Go(func() {
				id := fmt.Sprintf("w%d", wID)
				w := NewPrefixWriter(sink, id)
				for lID := range linesPerWriter {
					_, _ = fmt.Fprintf(w, "line-%d\n", lID)
				}
			})
		}
		wg.Wait()

		lineRe := regexp.MustCompile(`^\[w\d+\] line-\d+$`)
		lines := strings.Split(strings.TrimRight(sink.String(), "\n"), "\n")
		if len(lines) != writers*linesPerWriter {
			t.Fatalf("expected %d lines, got %d", writers*linesPerWriter, len(lines))
		}
		for _, line := range lines {
			if !lineRe.MatchString(line) {
				t.Errorf("line does not match expected single-writer pattern (interleaved?): %q", line)
			}
		}
	})
}
