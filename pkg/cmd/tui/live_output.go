package tui

import (
	"fmt"
	"strings"
)

// LiveOutputCapacity is the ring buffer depth for the Live Output view.
const LiveOutputCapacity = 50000

// ringBuffer is a fixed-capacity line buffer for streamed loop output.
type ringBuffer struct {
	lines []string
	cap   int
}

func newRingBuffer(capacity int) *ringBuffer {
	return &ringBuffer{cap: capacity}
}

// Push appends a line, dropping the oldest once the buffer is full.
func (r *ringBuffer) Push(line string) {
	r.lines = append(r.lines, line)
	if len(r.lines) > r.cap {
		r.lines = r.lines[len(r.lines)-r.cap:]
	}
}

// Tail returns the last n lines, oldest first.
func (r *ringBuffer) Tail(n int) []string {
	if n >= len(r.lines) {
		return r.lines
	}
	return r.lines[len(r.lines)-n:]
}

// Len reports the number of buffered lines.
func (r *ringBuffer) Len() int { return len(r.lines) }

// Clear empties the buffer.
func (r *ringBuffer) Clear() { r.lines = r.lines[:0] }

// renderLiveOutput lays out the Live Output view: a source header and the
// scrolling output pane.
func (m *Model) renderLiveOutput(w, h int) string {
	headerH := 4
	outH := maxInt(3, h-headerH)

	return joinV(
		m.theme.pane("Source", m.liveHeader(w-2), w, headerH, false),
		m.theme.pane(m.liveTitle(), m.liveBody(w-2, outH-2), w, outH, true),
	)
}

// liveTitle labels the output pane with its scroll state.
func (m *Model) liveTitle() string {
	if m.frozen {
		return fmt.Sprintf("Live Output (%d lines) — FROZEN", m.output.Len())
	}
	return fmt.Sprintf("Live Output (%d lines) — auto-scrolling", m.output.Len())
}

// liveHeader renders the loop source line and its current task.
func (m *Model) liveHeader(w int) string {
	label, pid := m.loopState()
	st := m.theme.LoopStatusStyle(label)

	source := m.theme.Dim.Render("colony loop (not running)")
	if pid > 0 {
		source = fmt.Sprintf("colony loop (PID %d)", pid)
	}

	var b strings.Builder
	fmt.Fprintf(&b, " %s %s\n", m.theme.Dim.Render("Source:"), source)
	b.WriteString(" " + st.Render(m.theme.icon(LoopStatusIcon(label))+" "+strings.ToUpper(label)))

	if cur, ok := m.currentTask(); ok {
		fmt.Fprintf(&b, "   %s %s (cycle %d)",
			m.theme.Dim.Render("Current:"), cur.ID, cur.CycleCount)
	}
	return b.String()
}

// liveBody renders the buffered output tail. The streaming source (subprocess
// pipe / .colony/loop.log tail) is not wired yet, so an unfed buffer reports
// that rather than inventing output.
func (m *Model) liveBody(w, rows int) string {
	if m.output == nil || m.output.Len() == 0 {
		return "\n " + m.theme.Dim.Render("(no output captured — the loop log stream is not attached)") +
			"\n " + m.theme.Dim.Render("Start a loop with [r] or view logs with: tail -f .colony/loop.log")
	}

	var b strings.Builder
	for _, line := range m.output.Tail(rows) {
		if m.wrapLines {
			for _, seg := range wrapText(line, w-2) {
				b.WriteString(" " + seg + "\n")
			}
			continue
		}
		b.WriteString(" " + truncate(line, w-2) + "\n")
	}
	if !m.frozen {
		b.WriteString(m.theme.Dim.Render(" ── end of live output ──"))
	}
	return b.String()
}
