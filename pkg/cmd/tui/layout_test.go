package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// sizes exercised by the layout invariants below.
var frameSizes = [][2]int{{80, 24}, {100, 30}, {120, 40}, {200, 60}}

func TestFrameFillsTerminalExactly(t *testing.T) {
	for _, size := range frameSizes {
		w, h := size[0], size[1]
		for _, v := range tabs {
			t.Run(v.String(), func(t *testing.T) {
				m := newTestModel(t, ViewDashboard, sampleStore())
				out, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
				m = out.(*Model)
				m.view = v

				lines := strings.Split(m.View(), "\n")
				if len(lines) != h {
					t.Errorf("%dx%d %s: got %d lines, want %d", w, h, v, len(lines), h)
				}
				for i, line := range lines {
					if lw := ansi.StringWidth(line); lw > w {
						t.Errorf("%dx%d %s: line %d is %d cells, exceeds width %d:\n%q",
							w, h, v, i, lw, w, line)
					}
				}
			})
		}
	}
}

func TestStatusBarIsPinnedToBottom(t *testing.T) {
	for _, v := range tabs {
		t.Run(v.String(), func(t *testing.T) {
			m := newTestModel(t, ViewDashboard, sampleStore())
			out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
			m = out.(*Model)
			m.view = v

			lines := strings.Split(m.View(), "\n")
			last := lines[len(lines)-1]
			want := statusHints[v][1]
			// The hint text survives styling; compare on the first key token.
			token := want[:strings.Index(want, "]")+1]
			if !strings.Contains(last, token) {
				t.Errorf("%s: last line should hold the status bar %q, got %q", v, token, last)
			}
		})
	}
}

func TestActiveTabIsMarked(t *testing.T) {
	for i, v := range tabs {
		t.Run(v.String(), func(t *testing.T) {
			m := newTestModel(t, ViewDashboard, sampleStore())
			out, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			m = out.(*Model)
			m.view = v

			first := strings.Split(m.View(), "\n")[0]
			active := "[" + itoa(i+1) + " " + v.String() + "]"
			if !strings.Contains(first, active) {
				t.Errorf("expected active tab %q in tab bar, got %q", active, first)
			}
			for j, other := range tabs {
				if other == v {
					continue
				}
				marked := "[" + itoa(j+1) + " " + other.String() + "]"
				if strings.Contains(first, marked) {
					t.Errorf("inactive view %s should not be bracketed in %q", other, first)
				}
			}
		})
	}
}

func TestModalFloatsAboveContent(t *testing.T) {
	m := newTestModel(t, ViewQueue, sampleStore())
	out, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = out.(*Model)

	base := m.View()
	m.modal = ModalHelp
	withModal := m.View()

	if !strings.Contains(withModal, "Help (Queue view)") {
		t.Fatal("expected the help modal to render")
	}
	// The background must survive: the task table header is outside the modal.
	if !strings.Contains(withModal, "DESCRIPTION") {
		t.Error("modal replaced the background instead of floating above it")
	}
	if strings.Count(base, "\n") != strings.Count(withModal, "\n") {
		t.Errorf("modal changed the frame height: %d -> %d",
			strings.Count(base, "\n"), strings.Count(withModal, "\n"))
	}
}

func TestToastsStackWithoutResizingFrame(t *testing.T) {
	m := newTestModel(t, ViewQueue, sampleStore())
	out, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = out.(*Model)

	baseLines := strings.Count(m.View(), "\n")
	m.notifier.Push("Task added", ToastOK)
	m.notifier.Push("Failed to stop loop: PID file not found", ToastErr)

	withToasts := m.View()
	if strings.Count(withToasts, "\n") != baseLines {
		t.Error("toasts changed the frame height instead of overlaying")
	}
	if !strings.Contains(withToasts, "Task added") {
		t.Error("expected the toast message in the frame")
	}
}

func TestPaneClipsRatherThanWraps(t *testing.T) {
	long := strings.Repeat("x", 400)
	got := (&Theme{borders: unicodeBorders}).pane("T", long, 40, 5, false)
	for i, line := range strings.Split(got, "\n") {
		if w := ansi.StringWidth(line); w != 40 {
			t.Errorf("line %d is %d cells, want exactly 40: %q", i, w, line)
		}
	}
}

func TestWindowKeepsCursorVisible(t *testing.T) {
	cases := []struct{ n, cursor, height, wantStart, wantEnd int }{
		{5, 0, 10, 0, 5},
		{100, 0, 10, 0, 10},
		{100, 50, 10, 45, 55},
		{100, 99, 10, 90, 100},
		{0, 0, 10, 0, 0},
	}
	for _, tc := range cases {
		start, end := window(tc.n, tc.cursor, tc.height)
		if start != tc.wantStart || end != tc.wantEnd {
			t.Errorf("window(%d,%d,%d) = (%d,%d), want (%d,%d)",
				tc.n, tc.cursor, tc.height, start, end, tc.wantStart, tc.wantEnd)
		}
		if tc.n > 0 && (tc.cursor < start || tc.cursor >= end) {
			t.Errorf("window(%d,%d,%d) hid the cursor", tc.n, tc.cursor, tc.height)
		}
	}
}

func TestOverlayPreservesBackgroundOutsideBox(t *testing.T) {
	base := strings.Join([]string{
		"aaaaaaaaaa",
		"bbbbbbbbbb",
		"cccccccccc",
	}, "\n")
	got := overlay(base, "XX\nYY", 4, 1)
	want := strings.Join([]string{
		"aaaaaaaaaa",
		"bbbbXXbbbb",
		"ccccYYcccc",
	}, "\n")
	if got != want {
		t.Errorf("overlay spliced incorrectly:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
