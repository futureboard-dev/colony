package tui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// loopPidFile is the shared name for the loop PID file.
const loopPidFile = "loop.pid"

// tuiLockFile is the lock file guarding against multiple TUI instances.
const tuiLockFile = "tui.lock"

// ErrTUIRunning is returned when another TUI instance holds the lock.
var ErrTUIRunning = errors.New("another TUI instance is running (found tui.lock)")

// LoopState derives the apparent loop status from the project dir: not running,
// running (alive PID), stale (dead PID present), or crashed (dead PID w/o
// sentinel handled by caller). Returns a normalized label plus the PID.
func LoopState(colonyDir string) (label string, pid int) {
	pidPath := filepath.Join(colonyDir, loopPidFile)
	pid = pidFromFile(pidPath)
	if pid == 0 {
		return "idle", 0
	}
	if pidAlive(pid) {
		return "running", pid
	}
	return "crashed", pid
}

// pidAlive checks whether a process exists via `kill -0`, falling back to
// os.FindProcess when the kill executable is unavailable.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// Prefer kill -0 as specified; ignore "permission denied" (still alive).
	if err := exec.Command("kill", "-0", strconv.Itoa(pid)).Run(); err == nil {
		return true
	} else if ee, ok := err.(*exec.ExitError); ok {
		// Exit code 1 => no such process. Permission errors are other codes.
		return ee.ExitCode() != 1
	}
	// Fallback: OS-level probe.
	if proc, err := os.FindProcess(pid); err == nil {
		return proc.Signal(syscall.Signal(0)) == nil
	}
	return false
}

// pidFromFile reads a PID from a file, returning 0 if absent or unparsable.
func pidFromFile(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

// RecoverStalePid removes a stale PID file (a dead PID) so the loop may be
// started afresh. Returns true if a stale file was removed.
func RecoverStalePid(colonyDir string) (recovered bool) {
	pidPath := filepath.Join(colonyDir, loopPidFile)
	pid := pidFromFile(pidPath)
	if pid == 0 {
		return false // no PID file at all
	}
	if pidAlive(pid) {
		return false // live loop; leave it
	}
	_ = os.Remove(pidPath)
	return true
}

// TUIInstanceRunning reports whether another TUI instance holds the lock with a
// live PID. Stale locks (dead PID) are dropped and reported as not running.
func TUIInstanceRunning(colonyDir string) bool {
	lockPath := filepath.Join(colonyDir, tuiLockFile)
	pid := pidFromFile(lockPath)
	if pid == 0 {
		return false
	}
	if pidAlive(pid) {
		return true
	}
	// Stale lock: reclaim it so --force and normal launches both proceed.
	_ = os.Remove(lockPath)
	return false
}

// AcquireTUILock writes the current PID to tui.lock, refusing when another live
// instance is present. When force is true the lock is taken unconditionally.
func AcquireTUILock(colonyDir string, force bool) error {
	lockPath := filepath.Join(colonyDir, tuiLockFile)
	if !force {
		if existing := pidFromFile(lockPath); existing > 0 && existing != os.Getpid() {
			if pidAlive(existing) {
				return ErrTUIRunning
			}
			_ = os.Remove(lockPath)
		}
	}
	return os.WriteFile(lockPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0644)
}

// ReleaseTUILock removes the TUI lock file (best-effort).
func ReleaseTUILock(colonyDir string) {
	_ = os.Remove(filepath.Join(colonyDir, tuiLockFile))
}
