package output

import (
	"bytes"
	"io"
	"sync"
)

// PrefixWriter prefixes each complete line written through it with "[<id>] "
// before forwarding it to the underlying writer, buffering partial lines
// until a newline arrives. It is used by the loop's parallel workers so
// output from concurrently-running tasks stays attributable to the task (or
// part) that produced it. Each line is forwarded in a single Write call, so
// as long as the underlying writer is itself safe for concurrent use,
// multiple PrefixWriters sharing it never interleave content within a line.
type PrefixWriter struct {
	out    io.Writer
	prefix string
	mu     sync.Mutex
	buf    bytes.Buffer
}

// NewPrefixWriter returns a PrefixWriter that prefixes every line written
// through it with "[id] " before forwarding it to out.
func NewPrefixWriter(out io.Writer, id string) *PrefixWriter {
	return &PrefixWriter{out: out, prefix: "[" + id + "] "}
}

// Write implements io.Writer. Complete lines (ending in '\n') are forwarded
// immediately with the prefix attached; any trailing partial line is held
// until a future Write completes it, or until Close flushes it.
func (w *PrefixWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buf.Write(p)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			// No newline in what's left — ReadString already drained the
			// buffer into line, so put it back and wait for more input.
			w.buf.Reset()
			w.buf.WriteString(line)
			break
		}
		if _, werr := io.WriteString(w.out, w.prefix+line); werr != nil {
			return 0, werr
		}
	}
	return len(p), nil
}

// Close flushes any buffered partial line, appending a trailing newline, and
// implements io.Closer.
func (w *PrefixWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() == 0 {
		return nil
	}
	_, err := io.WriteString(w.out, w.prefix+w.buf.String()+"\n")
	w.buf.Reset()
	return err
}
