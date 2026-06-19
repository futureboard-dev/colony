package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// writePidfile writes the current PID to pidPath. If the file already exists
// and the process referenced is still alive, it returns an error (duplicate
// daemon guard). If the process is dead, the stale file is removed first.
func writePidfile(pidPath string) error {
	dir := filepath.Dir(pidPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create pidfile dir: %w", err)
	}

	data, err := os.ReadFile(pidPath)
	if err == nil && len(data) > 0 {
		var oldPid int
		if _, err := fmt.Sscanf(string(data), "%d", &oldPid); err == nil {
			// Check if the process is still alive.
			if proc, err := os.FindProcess(oldPid); err == nil {
				if err := proc.Signal(syscall.Signal(0)); err == nil {
					return fmt.Errorf("daemon already running (pid=%d); found in %s", oldPid, pidPath)
				}
			}
		}
		// Stale pidfile — remove it.
		os.Remove(pidPath) //nolint:errcheck
	}

	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644); err != nil {
		return fmt.Errorf("write pidfile: %w", err)
	}
	return nil
}

// readPidfile reads the PID from pidPath, returning 0 if the file doesn't exist
// or can't be parsed.
func readPidfile(pidPath string) int {
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0
	}
	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil {
		return 0
	}
	return pid
}

// maybeRotate rotates a log file if its size exceeds maxSize bytes. It keeps up
// to maxFiles rotated files, renaming like log.1, log.2, etc. (logrotate style).
func maybeRotate(logPath string, maxSize int64, maxFiles int) error {
	info, err := os.Stat(logPath)
	if err != nil {
		return nil // file doesn't exist yet — nothing to rotate
	}
	if info.Size() < maxSize {
		return nil
	}

	// Remove the oldest file if it exists.
	oldest := fmt.Sprintf("%s.%d", logPath, maxFiles)
	os.Remove(oldest) //nolint:errcheck

	// Shift existing rotated files.
	for i := maxFiles - 1; i >= 1; i-- {
		old := fmt.Sprintf("%s.%d", logPath, i)
		if _, err := os.Stat(old); err == nil {
			_ = os.Rename(old, fmt.Sprintf("%s.%d", logPath, i+1))
		}
	}

	// Rename current log.
	if err := os.Rename(logPath, logPath+".1"); err != nil {
		return fmt.Errorf("rotate log: %w", err)
	}
	// Re-create the empty log file.
	f, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("re-create log after rotation: %w", err)
	}
	f.Close()
	return nil
}

// daemonUptime returns the duration since the daemon process started, based on
// the pidfile's existence and the current process. This is a simple heuristic:
// when called from `loop status` in the daemon process itself, it measures
// self-uptime. In other cases the caller provides the start time.
func daemonUptime(colonyPath string) (bool, int, string) {
	pidPath := filepath.Join(colonyPath, "loop.pid")
	pid := readPidfile(pidPath)
	if pid == 0 || pid == os.Getpid() {
		// No pidfile or we are the daemon.
		return pid > 0, pid, ""
	}
	// Check if process is alive.
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, 0, ""
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false, 0, ""
	}
	return true, pid, ""
}
