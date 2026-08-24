package tui

import (
	"bufio"
	"io"
	"os"
	"sync"
	"time"
)

// loopLogFile is the log the watch daemon appends to. Following it is how the
// TUI shows output from a loop it did not start itself.
const loopLogFile = "loop.log"

// logTailBacklogBytes bounds how much of an existing log is replayed when the
// TUI attaches, so a long-lived daemon log does not flood the buffer.
const logTailBacklogBytes = 64 * 1024

// logTailInterval is how often the log is checked for appended bytes.
const logTailInterval = 500 * time.Millisecond

// LogTailer follows a file the way `tail -f` does: it replays the end of what
// is already there, then emits each appended line.
type LogTailer struct {
	path     string
	interval time.Duration
	lines    chan string
	stop     chan struct{}
	stopOnce sync.Once

	// Poll state: offset is the byte position already emitted, seeded is false
	// until the file has been seen, and partial marks a first line that was cut
	// mid-way by the backlog trim.
	offset  int64
	seeded  bool
	partial bool
}

// NewLogTailer builds a tailer for path. It does nothing until Run is called.
func NewLogTailer(path string) *LogTailer {
	return &LogTailer{
		path:     path,
		interval: logTailInterval,
		lines:    make(chan string, 256),
		stop:     make(chan struct{}),
	}
}

// Lines exposes the output channel for the reader command.
func (t *LogTailer) Lines() <-chan string { return t.lines }

// Stop ends the polling goroutine. It is safe to call more than once.
func (t *LogTailer) Stop() { t.stopOnce.Do(func() { close(t.stop) }) }

// Run polls until Stop is called; it is meant to run in its own goroutine.
func (t *LogTailer) Run() {
	tick := time.NewTicker(t.interval)
	defer tick.Stop()
	t.poll()
	for {
		select {
		case <-t.stop:
			return
		case <-tick.C:
			t.poll()
		}
	}
}

// poll emits whatever was appended since the last call. A file that shrank was
// rotated or truncated, so it is re-read from the start; a file that vanished
// resets the seed so its replacement is picked up from its tail again.
func (t *LogTailer) poll() {
	info, err := os.Stat(t.path)
	if err != nil {
		t.seeded, t.offset = false, 0
		return
	}
	size := info.Size()
	switch {
	case !t.seeded:
		t.seeded = true
		t.offset = maxInt64(0, size-logTailBacklogBytes)
		t.partial = t.offset > 0
	case size < t.offset:
		t.offset, t.partial = 0, false
	case size == t.offset:
		return
	}
	t.read(size)
}

// read emits the lines between the current offset and size.
func (t *LogTailer) read(size int64) {
	f, err := os.Open(t.path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return
	}

	sc := bufio.NewScanner(io.LimitReader(f, size-t.offset))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if t.partial {
			// The backlog trim landed mid-line; that fragment is not a line.
			t.partial = false
			continue
		}
		t.emit(sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.emit("[log tail truncated: " + err.Error() + "]")
	}
	t.offset = size
}

// emit queues a line, dropping it when the TUI has fallen behind rather than
// stalling the tailer.
func (t *LogTailer) emit(line string) {
	select {
	case t.lines <- line:
	default:
	}
}
