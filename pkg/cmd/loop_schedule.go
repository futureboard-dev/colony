package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var scheduleEvery string

// loopScheduler is the platform-specific interface for OS-level scheduling.
type loopScheduler interface {
	// Install creates or updates the scheduled job to run every d.
	Install(binaryPath, projectRoot string, d time.Duration) error
	// Remove deletes the scheduled job. It is a no-op if nothing is installed.
	Remove() error
	// Status returns whether a job is currently installed and its interval.
	Status() (installed bool, interval time.Duration, err error)
	// Label returns a human-readable name for the scheduler backend.
	Label() string
}

// newLoopScheduler returns the platform-appropriate scheduler.
// Defined in _darwin.go and _linux.go via build tags.
var newLoopScheduler func() loopScheduler

// projectHash returns a short hash of the absolute project root for
// use in launchd labels and cron comment identifiers.
func projectHash(root string) string {
	// Use a simple hash based on the directory path.
	h := 0
	for _, c := range root {
		h = h*31 + int(c)
	}
	return fmt.Sprintf("%x", h)
}

var loopScheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Manage OS-level loop scheduling (launchd / crontab)",
	Long: `Install, remove, or check the status of OS-level scheduling for the
loop command. Uses launchd on macOS and crontab on Linux.

Subcommands:
  start    Install or update the schedule
  stop     Remove the schedule
  status   Show whether the schedule is active`,
}

var loopScheduleStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Install or update the loop schedule",
	Long: `Install (or update) an OS-level scheduled job that runs 'colony loop'
at the given interval.

  --every <duration>   How often to run the loop (e.g. 5m, 1h). Minimum 1m.

On macOS this creates a launchd plist in ~/Library/LaunchAgents/.
On Linux this creates a crontab entry.`,
	RunE: runScheduleStart,
}

var loopScheduleStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Remove the loop schedule",
	Long: `Remove the OS-level scheduled job. Safe to call when nothing is
installed (no-op).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		sched := newLoopScheduler()
		if err := sched.Remove(); err != nil {
			return fmt.Errorf("remove schedule: %w", err)
		}
		fmt.Printf("✓ %s schedule removed\n", sched.Label())
		return nil
	},
}

var loopScheduleStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show whether the loop schedule is active",
	RunE: func(cmd *cobra.Command, args []string) error {
		sched := newLoopScheduler()
		installed, interval, err := sched.Status()
		if err != nil {
			return fmt.Errorf("schedule status: %w", err)
		}
		if !installed {
			fmt.Printf("%s schedule: not installed\n", sched.Label())
		} else {
			fmt.Printf("%s schedule: active (every %s)\n", sched.Label(), interval)
		}
		return nil
	},
}

func init() {
	loopScheduleStartCmd.Flags().StringVar(&scheduleEvery, "every", "10m", "interval between loop runs (e.g. 5m, 1h; minimum 1m)")
	loopScheduleCmd.AddCommand(loopScheduleStartCmd)
	loopScheduleCmd.AddCommand(loopScheduleStopCmd)
	loopScheduleCmd.AddCommand(loopScheduleStatusCmd)
}

func runScheduleStart(cmd *cobra.Command, args []string) error {
	d, err := time.ParseDuration(scheduleEvery)
	if err != nil {
		return fmt.Errorf("invalid --every %q: %w", scheduleEvery, err)
	}
	if d < time.Minute {
		return fmt.Errorf("--every must be at least 1m, got %s", d)
	}

	_, root, err := loadConfig()
	if err != nil {
		return err
	}

	// Resolve the colony binary path.
	binaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve binary path: %w", err)
	}
	// Resolve symlinks so the path is stable.
	resolved, err := filepath.EvalSymlinks(binaryPath)
	if err == nil {
		binaryPath = resolved
	}

	sched := newLoopScheduler()
	if err := sched.Install(binaryPath, root, d); err != nil {
		return fmt.Errorf("install schedule: %w", err)
	}
	fmt.Printf("✓ %s schedule installed: every %s\n", sched.Label(), friendlyDuration(d))
	return nil
}

func friendlyDuration(d time.Duration) string {
	s := d.String()
	// Remove trailing "0s" etc. for readability.
	s = strings.TrimSuffix(s, "0s")
	s = strings.TrimSuffix(s, "0m")
	if strings.HasSuffix(s, "m") {
		return s + "0s"
	}
	return d.Round(time.Second).String()
}
