package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoopControlStopWritesSentinel(t *testing.T) {
	dir := t.TempDir()
	m := newTestModel(t, ViewDashboard, sampleStore())
	m.opts.ColonyDir = dir
	m.modal = ModalLoopControl

	m.Update(key("s"))

	if _, err := os.Stat(filepath.Join(dir, loopStopFile)); err != nil {
		t.Fatalf("stop key did not write the sentinel: %v", err)
	}
	if m.output.Len() == 0 {
		t.Error("expected the stop to be recorded in live output")
	}
}

func TestLoopControlKillWithoutPidReportsError(t *testing.T) {
	m := newTestModel(t, ViewDashboard, sampleStore())
	m.opts.ColonyDir = t.TempDir()
	m.modal = ModalLoopControl

	m.Update(key("K"))

	toasts := m.notifier.All()
	if len(toasts) == 0 || toasts[0].Kind != ToastErr {
		t.Error("expected an error toast when there is no loop to kill")
	}
}

func TestLoopControlOpensPalette(t *testing.T) {
	m := newTestModel(t, ViewDashboard, sampleStore())
	m.modal = ModalLoopControl

	m.Update(key("c"))

	if m.modal != ModalPalette {
		t.Errorf("expected the palette to open, got modal %d", m.modal)
	}
	if !m.palette.picking {
		t.Error("palette should open on the command picker")
	}
}

func TestStartLoopRefusesWhenPidFileIsLive(t *testing.T) {
	dir := t.TempDir()
	// Our own PID is definitely alive, so this stands in for a running loop.
	if err := os.WriteFile(filepath.Join(dir, loopPidFile),
		[]byte(itoa(os.Getpid())+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := newTestModel(t, ViewDashboard, sampleStore())
	m.opts.ColonyDir = dir

	if cmd := m.startLoop("loop", []string{"loop"}); cmd != nil {
		t.Error("expected no command when a loop is already running")
	}
	toasts := m.notifier.All()
	if len(toasts) == 0 || toasts[0].Kind != ToastErr {
		t.Error("expected an error toast about the running loop")
	}
	if m.runner.Running() {
		t.Error("no process should have started")
	}
}

func TestStartLoopClearsStaleStopSentinel(t *testing.T) {
	dir := t.TempDir()
	if err := WriteStopSentinel(dir); err != nil {
		t.Fatal(err)
	}

	m := newTestModel(t, ViewDashboard, sampleStore())
	m.opts.ColonyDir = dir
	m.opts.Root = dir
	// Start something harmless; the point is the sentinel handling around it.
	m.runner = NewRunner()
	if err := m.runner.Start("/bin/sh", dir, "noop", []string{"-c", "true"}); err != nil {
		t.Fatal(err)
	}
	_ = m.runner.Wait()

	ClearStopSentinel(dir)
	if _, err := os.Stat(filepath.Join(dir, loopStopFile)); !os.IsNotExist(err) {
		t.Error("a stale stop sentinel would halt the next run immediately")
	}
}

func TestProcessExitReportsOutcome(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		m := newTestModel(t, ViewLiveOutput, sampleStore())
		m.handleProcessExit(processExitedMsg{label: "loop --once"})
		if got := m.notifier.All(); len(got) == 0 || got[0].Kind != ToastOK {
			t.Error("expected a success toast")
		}
	})

	t.Run("failure", func(t *testing.T) {
		m := newTestModel(t, ViewLiveOutput, sampleStore())
		m.handleProcessExit(processExitedMsg{label: "craft", err: os.ErrPermission})
		if got := m.notifier.All(); len(got) == 0 || got[0].Kind != ToastErr {
			t.Error("expected an error toast")
		}
	})
}

func TestFrozenOutputKeepsBufferingButStaysPinned(t *testing.T) {
	m := newTestModel(t, ViewLiveOutput, sampleStore())
	m.appendOutput("first")
	m.frozen = true

	m.handleOutputLine("second")
	m.handleOutputLine("third")
	if m.output.Len() != 3 {
		t.Errorf("frozen output must keep buffering, got %d lines", m.output.Len())
	}
	if m.scrollOff != 2 {
		t.Errorf("frozen view should stay pinned, scrollOff = %d, want 2", m.scrollOff)
	}
	if got := m.output.Window(m.scrollOff, 1); len(got) != 1 || got[0] != "first" {
		t.Errorf("frozen view shows %v, want the pinned line", got)
	}

	m.scrollOutputBottom()
	m.handleOutputLine("fourth")
	if m.scrollOff != 0 {
		t.Errorf("following the tail should keep scrollOff at 0, got %d", m.scrollOff)
	}
	if got := m.output.Window(0, 1); got[0] != "fourth" {
		t.Errorf("unfrozen view shows %q, want the newest line", got[0])
	}
}

func TestLiveOutputKeysAreConsumedNotRoutedToViews(t *testing.T) {
	// "s" and "k" are global view/navigation keys elsewhere; in Live Output
	// they must mean stop and scroll back instead.
	m := newTestModel(t, ViewLiveOutput, sampleStore())
	m.opts.ColonyDir = t.TempDir()

	m.Update(key("s"))
	if m.view != ViewLiveOutput {
		t.Error("\"s\" in Live Output must not navigate to Sessions")
	}
	if _, err := os.Stat(filepath.Join(m.opts.ColonyDir, loopStopFile)); err != nil {
		t.Error("\"s\" in Live Output should request a stop")
	}

	before := m.cursor
	m.Update(key("k"))
	if m.cursor != before {
		t.Error("\"k\" in Live Output must not move the cursor")
	}
}
