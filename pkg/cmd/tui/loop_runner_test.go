package tui

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// collect drains n lines from the runner or fails after a timeout.
func collect(t *testing.T, r *Runner, n int) []string {
	t.Helper()
	out := make([]string, 0, n)
	deadline := time.After(5 * time.Second)
	for len(out) < n {
		select {
		case line := <-r.Lines():
			out = append(out, line)
		case <-deadline:
			t.Fatalf("timed out after %d/%d lines: %v", len(out), n, out)
		}
	}
	return out
}

func TestRunnerStreamsStdoutAndStderr(t *testing.T) {
	r := NewRunner()
	if err := r.Start("/bin/sh", t.TempDir(), "echo",
		[]string{"-c", "echo out-line; echo err-line 1>&2"}); err != nil {
		t.Fatalf("start: %v", err)
	}

	got := collect(t, r, 2)
	if err := r.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}

	seen := map[string]bool{got[0]: true, got[1]: true}
	if !seen["out-line"] || !seen["err-line"] {
		t.Errorf("expected both streams merged, got %v", got)
	}
}

func TestRunnerRefusesConcurrentStart(t *testing.T) {
	r := NewRunner()
	dir := t.TempDir()
	if err := r.Start("/bin/sh", dir, "sleep", []string{"-c", "sleep 5"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		_ = r.Signal(syscall.SIGKILL)
		_ = r.Wait()
	}()

	if err := r.Start("/bin/sh", dir, "second", []string{"-c", "true"}); err == nil {
		t.Error("expected the second start to be refused while one is running")
	}
	if !r.Running() {
		t.Error("runner should report the first process as running")
	}
	if r.Label() != "sleep" {
		t.Errorf("label = %q, want \"sleep\"", r.Label())
	}
}

func TestRunnerSignalTerminatesProcess(t *testing.T) {
	r := NewRunner()
	if err := r.Start("/bin/sh", t.TempDir(), "sleep", []string{"-c", "sleep 30"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := r.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- r.Wait() }()
	select {
	case <-done: // a signalled process exits non-zero; that it exited is the point
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit after SIGTERM")
	}
	if r.Running() {
		t.Error("runner should be idle after the process exits")
	}
}

func TestRunnerSignalWithoutProcess(t *testing.T) {
	if err := NewRunner().Signal(syscall.SIGTERM); err == nil {
		t.Error("expected an error when signalling an idle runner")
	}
}

func TestStopSentinelLifecycle(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, loopStopFile)

	if err := WriteStopSentinel(dir); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sentinel not created: %v", err)
	}

	ClearStopSentinel(dir)
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Error("sentinel should be removed")
	}

	if err := WriteStopSentinel(""); err == nil {
		t.Error("expected an error when the colony dir is unknown")
	}
}

func TestStopSentinelPathMatchesCLI(t *testing.T) {
	// `colony loop stop` writes .colony/loop.stop; the TUI must use the same
	// name or a TUI stop will not be seen by a CLI-started loop.
	if loopStopFile != "loop.stop" {
		t.Errorf("sentinel name = %q, want \"loop.stop\"", loopStopFile)
	}
}

func TestKillLoopReportsMissingAndStalePid(t *testing.T) {
	dir := t.TempDir()

	if err := KillLoop(dir); err == nil {
		t.Error("expected an error when loop.pid is absent")
	}

	// A PID that cannot be running: write one and confirm it is reported stale
	// rather than signalled.
	pidPath := filepath.Join(dir, loopPidFile)
	if err := os.WriteFile(pidPath, []byte("21474836\n"), 0644); err != nil {
		t.Fatal(err)
	}
	err := KillLoop(dir)
	if err == nil {
		t.Fatal("expected an error for a dead PID")
	}
	if got := err.Error(); got == "" {
		t.Error("expected a descriptive stale-pid error")
	}

	if err := KillLoop(""); err == nil {
		t.Error("expected an error when the colony dir is unknown")
	}
}

func TestColonyBinaryResolves(t *testing.T) {
	if colonyBinary() == "" {
		t.Error("colonyBinary must never return an empty path")
	}
}
