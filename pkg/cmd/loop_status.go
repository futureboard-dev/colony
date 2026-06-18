package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	// loopStatusCmd is added to loopCmd in loop.go.
}

// loopStatusCmd reports queue status and daemon liveness.
var loopStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show queue status and daemon liveness",
	RunE:  runLoopStatus,
}

type daemonState struct {
	running bool
	pid     int
	uptime  time.Duration
}

func readDaemonState() daemonState {
	var ds daemonState
	pidPath := filepath.Join(".colony", "loop.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return ds // not running
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil {
		return ds
	}
	// Check if the process is alive.
	proc, err := os.FindProcess(pid)
	if err != nil || proc == nil {
		return ds
	}
	// On Unix, FindProcess always succeeds; send signal 0 to check liveness.
	if err := proc.Signal(os.Signal(nil)); err != nil {
		return ds // stale pid
	}
	ds.running = true
	ds.pid = pid
	// Uptime: check the modtime of the pidfile.
	if fi, err := os.Stat(pidPath); err == nil {
		ds.uptime = time.Since(fi.ModTime()).Round(time.Second)
	}
	return ds
}

func runLoopStatus(cmd *cobra.Command, args []string) error {
	// Queue status.
	store, err := openStore()
	if err != nil {
		fmt.Printf("⚠  Store not available: %v\n", err)
	} else {
		defer func() { _ = store.Close() }()
		tasks, err := store.QueryTasks()
		if err != nil {
			fmt.Printf("⚠  Query error: %v\n", err)
		} else {
			var open, needsFix, inProgress, done int
			for _, t := range tasks {
				switch t.Status {
				case "open":
					open++
				case "needs-fix":
					needsFix++
				case "in-progress":
					inProgress++
				case "done":
					done++
				}
			}
			fmt.Printf("Queue:\n")
			fmt.Printf("  open:       %d\n", open)
			fmt.Printf("  needs-fix:  %d\n", needsFix)
			fmt.Printf("  in-progress:%d\n", inProgress)
			fmt.Printf("  done:       %d\n", done)
			fmt.Printf("  total:      %d\n", len(tasks))
		}
	}

	// Daemon liveness.
	ds := readDaemonState()
	fmt.Printf("\nDaemon:\n")
	if ds.running {
		fmt.Printf("  status: running\n")
		fmt.Printf("  pid:    %d\n", ds.pid)
		fmt.Printf("  uptime: %s\n", ds.uptime)
	} else {
		fmt.Printf("  status: not running\n")
	}
	return nil
}
