package tui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
)

// loopStopFile is the sentinel `colony loop` polls to stop after the current
// task. It must match the path written by `colony loop stop`.
const loopStopFile = "loop.stop"

// outputLineMsg carries one line of child-process output into the update loop.
type outputLineMsg struct{ line string }

// logLineMsg carries one line tailed from .colony/loop.log.
type logLineMsg struct{ line string }

// processExitedMsg reports that the running child finished.
type processExitedMsg struct {
	label string
	err   error
}

// Runner owns at most one child process and funnels its merged stdout/stderr
// into a channel the TUI drains one line at a time.
type Runner struct {
	mu    sync.Mutex
	cmd   *exec.Cmd
	label string
	lines chan string
	done  chan error
}

// NewRunner builds an idle runner.
func NewRunner() *Runner {
	return &Runner{lines: make(chan string, 256)}
}

// Running reports whether a child process is currently attached.
func (r *Runner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cmd != nil
}

// Label returns the human-readable name of the running command, if any.
func (r *Runner) Label() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.label
}

// Lines exposes the output channel for the reader command.
func (r *Runner) Lines() <-chan string { return r.lines }

// Start launches `<colony> args...` in dir, merging stdout and stderr into the
// line channel. It refuses to start a second process.
func (r *Runner) Start(bin, dir, label string, args []string) error {
	r.mu.Lock()
	if r.cmd != nil {
		r.mu.Unlock()
		return fmt.Errorf("%s is already running", r.label)
	}

	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	// A dedicated process group lets kill reach the agent subprocesses the loop
	// spawns, not just the loop itself.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.mu.Unlock()
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		r.mu.Unlock()
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		r.mu.Unlock()
		return fmt.Errorf("start %s: %w", label, err)
	}
	r.cmd, r.label = cmd, label
	done := make(chan error, 1)
	r.done = done
	r.mu.Unlock()

	var wg sync.WaitGroup
	for _, pipe := range []io.Reader{stdout, stderr} {
		wg.Add(1)
		go func(p io.Reader) {
			defer wg.Done()
			r.scan(p)
		}(pipe)
	}

	go func() {
		wg.Wait()
		err := cmd.Wait()
		r.mu.Lock()
		r.cmd, r.label = nil, ""
		r.mu.Unlock()
		done <- err
	}()
	return nil
}

// scan forwards a pipe's lines into the channel, dropping them when the TUI
// falls behind rather than blocking the child on a full buffer.
func (r *Runner) scan(p io.Reader) {
	sc := bufio.NewScanner(p)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		select {
		case r.lines <- sc.Text():
		default:
		}
	}
	if err := sc.Err(); err != nil {
		select {
		case r.lines <- "[output truncated: " + err.Error() + "]":
		default:
		}
	}
}

// Wait blocks until the running child exits, returning its error.
func (r *Runner) Wait() error {
	r.mu.Lock()
	done := r.done
	r.mu.Unlock()
	if done == nil {
		return nil
	}
	return <-done
}

// Signal sends sig to the child's process group.
func (r *Runner) Signal(sig syscall.Signal) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd == nil || r.cmd.Process == nil {
		return errors.New("no running process")
	}
	// Negative PID targets the whole group; fall back to the bare PID.
	if err := syscall.Kill(-r.cmd.Process.Pid, sig); err == nil {
		return nil
	}
	return r.cmd.Process.Signal(sig)
}

// WriteStopSentinel creates .colony/loop.stop so a running loop finishes its
// current task and exits. This works for loops started outside the TUI too.
func WriteStopSentinel(colonyDir string) error {
	if colonyDir == "" {
		return errors.New("colony directory unknown")
	}
	if err := os.MkdirAll(colonyDir, 0755); err != nil {
		return fmt.Errorf("create colony dir: %w", err)
	}
	return os.WriteFile(filepath.Join(colonyDir, loopStopFile), nil, 0644)
}

// ClearStopSentinel removes a leftover stop sentinel so the next start is not
// halted immediately by the previous stop.
func ClearStopSentinel(colonyDir string) {
	if colonyDir != "" {
		_ = os.Remove(filepath.Join(colonyDir, loopStopFile))
	}
}

// KillLoop sends SIGTERM to the loop identified by .colony/loop.pid. It targets
// the process group when possible so agent children die with it.
func KillLoop(colonyDir string) error {
	if colonyDir == "" {
		return errors.New("colony directory unknown")
	}
	pid := pidFromFile(filepath.Join(colonyDir, loopPidFile))
	if pid == 0 {
		return errors.New("no loop.pid file — is the loop running?")
	}
	if !pidAlive(pid) {
		return fmt.Errorf("loop pid %d is not running (stale pid file)", pid)
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err == nil {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find pid %d: %w", pid, err)
	}
	return proc.Signal(syscall.SIGTERM)
}

// colonyBinary resolves the running colony executable so spawned children are
// the same build as the TUI, falling back to $PATH lookup.
func colonyBinary() string {
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			return resolved
		}
		return exe
	}
	if path, err := exec.LookPath("colony"); err == nil {
		return path
	}
	return "colony"
}
