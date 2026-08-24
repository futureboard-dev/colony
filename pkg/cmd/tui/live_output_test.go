package tui

import (
	"strings"
	"testing"
)

func TestRingBufferWindow(t *testing.T) {
	r := newRingBuffer(10)
	for _, s := range []string{"a", "b", "c", "d"} {
		r.Push(s)
	}

	tests := []struct {
		name         string
		offset, rows int
		want         []string
	}{
		{"tail", 0, 2, []string{"c", "d"}},
		{"scrolled back", 2, 2, []string{"a", "b"}},
		{"larger than buffer", 0, 99, []string{"a", "b", "c", "d"}},
		{"offset past the top keeps a line visible", 99, 2, []string{"a"}},
		{"no rows", 0, 0, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := r.Window(tc.offset, tc.rows)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("Window(%d, %d) = %v, want %v", tc.offset, tc.rows, got, tc.want)
			}
		})
	}

	if got := newRingBuffer(4).Window(0, 3); got != nil {
		t.Errorf("empty buffer window = %v, want nil", got)
	}
}

func TestLiveOutputScrollKeys(t *testing.T) {
	m := newTestModel(t, ViewLiveOutput, sampleStore())
	for i := range 200 {
		m.appendOutput("line " + itoa(i))
	}

	m.Update(key("k"))
	if m.scrollOff != 1 || !m.frozen {
		t.Errorf("k: scrollOff=%d frozen=%v, want 1/true", m.scrollOff, m.frozen)
	}

	m.Update(key("j"))
	if m.scrollOff != 0 || m.frozen {
		t.Errorf("j back to the tail: scrollOff=%d frozen=%v, want 0/false", m.scrollOff, m.frozen)
	}

	m.Update(key("pgup"))
	if m.scrollOff != m.liveRows() {
		t.Errorf("pgup: scrollOff=%d, want one page (%d)", m.scrollOff, m.liveRows())
	}

	m.Update(key("g"))
	if m.scrollOff != m.output.Len()-1 {
		t.Errorf("g: scrollOff=%d, want the oldest line (%d)", m.scrollOff, m.output.Len()-1)
	}

	m.Update(key("G"))
	if m.scrollOff != 0 || m.frozen {
		t.Errorf("G: scrollOff=%d frozen=%v, want 0/false", m.scrollOff, m.frozen)
	}
}

func TestLiveOutputClearResetsScroll(t *testing.T) {
	m := newTestModel(t, ViewLiveOutput, sampleStore())
	m.appendOutput("one")
	m.appendOutput("two")
	m.Update(key("k"))

	m.Update(key("C"))
	if m.output.Len() != 0 || m.scrollOff != 0 || m.frozen {
		t.Errorf("clear left len=%d scrollOff=%d frozen=%v", m.output.Len(), m.scrollOff, m.frozen)
	}
}

func TestTailedLogLinesFeedTheBuffer(t *testing.T) {
	m := newTestModel(t, ViewLiveOutput, sampleStore())
	m.opts.ColonyDir = t.TempDir()
	m.tailer = NewLogTailer(m.opts.ColonyDir + "/" + loopLogFile)

	m.Update(logLineMsg{line: "from the daemon log"})
	if m.output.Len() != 1 {
		t.Fatalf("tailed line was not buffered (len=%d)", m.output.Len())
	}
	if got := m.output.Window(0, 1)[0]; got != "from the daemon log" {
		t.Errorf("buffered %q", got)
	}
}

func TestLiveOutputBodyShowsScrollPosition(t *testing.T) {
	m := newTestModel(t, ViewLiveOutput, sampleStore())
	for i := range 20 {
		m.appendOutput("line " + itoa(i))
	}

	if body := m.liveBody(80, 5); !strings.Contains(body, "line 19") {
		t.Error("tail view should show the newest line")
	}

	m.scrollOutput(10)
	body := m.liveBody(80, 5)
	if strings.Contains(body, "line 19") {
		t.Error("scrolled-back view should not show the newest line")
	}
	if !strings.Contains(body, "line 9") {
		t.Error("scrolled-back view should end 10 lines above the newest")
	}
	if !strings.Contains(m.liveTitle(), "FROZEN") {
		t.Errorf("title = %q, want a frozen marker", m.liveTitle())
	}
}
