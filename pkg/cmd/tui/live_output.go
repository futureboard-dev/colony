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

// Window returns up to n lines ending offset lines above the newest one, which
// is how the Live Output view scrolls back without copying the buffer.
func (r *ringBuffer) Window(offset, n int) []string {
	if n <= 0 || len(r.lines) == 0 {
		return nil
	}
	end := len(r.lines) - clampScroll(offset, len(r.lines))
	start := maxInt(0, end-n)
	return r.lines[start:end]
}

// clampScroll bounds a scrollback offset so at least one line stays visible.
func clampScroll(offset, total int) int {
	if offset < 0 || total == 0 {
		return 0
	}
	if offset > total-1 {
		return total - 1
	}
	return offset
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
	if m.scrollOff > 0 {
		return fmt.Sprintf("Live Output (%d lines) — FROZEN, %d below",
			m.output.Len(), clampScroll(m.scrollOff, m.output.Len()))
	}
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
	switch {
	case m.runner != nil && m.runner.Running():
		source = "colony " + m.runner.Label() + " (attached)"
	case pid > 0:
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

// liveBody renders the visible slice of the output buffer, which is fed both by
// processes this TUI starts and by the tailer following .colony/loop.log.
func (m *Model) liveBody(w, rows int) string {
	if m.output == nil || m.output.Len() == 0 {
		return "\n " + m.theme.Dim.Render("(no output yet — nothing started here and .colony/loop.log is quiet)") +
			"\n " + m.theme.Dim.Render("Start one with [l] loop control or [c] run command…")
	}

	hidden := clampScroll(m.scrollOff, m.output.Len())
	var b strings.Builder
	for _, line := range m.output.Window(hidden, rows) {
		if m.wrapLines {
			for _, seg := range wrapText(line, w-2) {
				b.WriteString(" " + seg + "\n")
			}
			continue
		}
		b.WriteString(" " + truncate(line, w-2) + "\n")
	}
	switch {
	case hidden > 0:
		b.WriteString(scrollHint(m.theme, hidden))
	case !m.frozen:
		b.WriteString(m.theme.Dim.Render(" ── end of live output ──"))
	}
	return b.String()
}

// liveRows is how many output lines fit in the pane at the current frame size.
// It mirrors renderLiveOutput's arithmetic so paging moves exactly one screen.
func (m *Model) liveRows() int {
	_, h := m.frameSize()
	bodyH := h - tabBarHeight - statusHeight
	if m.lockBanner {
		bodyH--
	}
	return maxInt(1, maxInt(3, bodyH-4)-2)
}

// scrollOutput moves the view n lines back through the buffer (negative scrolls
// toward the newest line). Scrolling back freezes auto-scroll; returning to the
// bottom resumes it, the way a log pager behaves.
func (m *Model) scrollOutput(n int) {
	if m.output == nil {
		return
	}
	m.scrollOff = clampScroll(m.scrollOff+n, m.output.Len())
	m.frozen = m.scrollOff > 0
}

// scrollOutputBottom jumps back to the newest line and resumes auto-scroll.
func (m *Model) scrollOutputBottom() {
	m.scrollOff = 0
	m.frozen = false
}

// scrollOutputTop jumps to the oldest buffered line.
func (m *Model) scrollOutputTop() {
	if m.output == nil {
		return
	}
	m.scrollOutputBottom()
	m.scrollOutput(m.output.Len())
}
