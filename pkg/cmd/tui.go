package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/futureboard-dev/colony/pkg/cmd/tui"
	"github.com/futureboard-dev/colony/pkg/storage"
	"github.com/spf13/cobra"
)

// command identifies this command for process/lock bookkeeping.
const command = "tui"

var (
	tuiRefresh   string
	tuiNoColor   bool
	tuiForce     bool
	tuiStartView string
)

// minRefreshRate is the fastest permitted poll interval.
const minRefreshRate = 100 * time.Millisecond

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch the full-screen terminal UI",
	Long: `Launches a full-screen, keyboard-driven terminal interface for Colony.
Read-only by default; all mutations happen behind explicit keybinds.

Flags:
  --refresh <dur>   Poll interval (default 1s, min 100ms)
  --no-color        Force monochrome rendering
  --force           Start even when another TUI instance is detected
  --view <v>        Initial view (dashboard, queue, detail, sessions, live)`,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return parseRefreshFlag()
	},
	RunE: runTUI,
}

func init() {
	tuiCmd.Flags().StringVar(&tuiRefresh, "refresh", "1s", "poll interval (min 100ms)")
	tuiCmd.Flags().BoolVar(&tuiNoColor, "no-color", false, "force monochrome rendering")
	tuiCmd.Flags().BoolVar(&tuiForce, "force", false, "start even when another TUI instance is detected")
	tuiCmd.Flags().StringVar(&tuiStartView, "view", "", "initial view (dashboard, queue, detail, sessions, live)")
}

// parseRefreshFlag validates/normalizes the --refresh duration.
func parseRefreshFlag() error {
	if strings.TrimSpace(tuiRefresh) == "" {
		tuiRefresh = "1s"
	}
	d, err := time.ParseDuration(tuiRefresh)
	if err != nil {
		return fmt.Errorf("invalid --refresh %q: %w", tuiRefresh, err)
	}
	if d < minRefreshRate {
		return fmt.Errorf("--refresh must be at least %s (got %s)", minRefreshRate, d)
	}
	tuiRefresh = d.String()
	return nil
}

// resolveStartView maps the --view flag to a tui.View.
func resolveStartView(s string) (tui.View, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "dashboard":
		return tui.ViewDashboard, nil
	case "queue":
		return tui.ViewQueue, nil
	case "detail", "task", "taskdetail":
		return tui.ViewTaskDetail, nil
	case "sessions":
		return tui.ViewSessions, nil
	case "live", "output", "liveoutput":
		return tui.ViewLiveOutput, nil
	default:
		return tui.ViewDashboard, fmt.Errorf("unknown --view %q", s)
	}
}

func runTUI(cmd *cobra.Command, args []string) error {
	_, root, err := loadConfig()
	if err != nil {
		return err
	}

	colonyDir := filepath.Join(root, ".colony")
	if info, statErr := os.Stat(colonyDir); statErr != nil || !info.IsDir() {
		return fmt.Errorf("no .colony directory found in %s — run `colony init` first", root)
	}

	view, err := resolveStartView(tuiStartView)
	if err != nil {
		return err
	}

	// command identifies this process to shared tooling; referenced here to keep
	// the process's invocation self-describing.
	_ = command

	refresh, _ := time.ParseDuration(tuiRefresh)

	// Guard against a second TUI instance (unless --force).
	if err := tui.AcquireTUILock(colonyDir, tuiForce); err != nil {
		return err
	}
	defer tui.ReleaseTUILock(colonyDir)

	// Stale loop PID recovery at launch.
	var startupLog string
	if tui.RecoverStalePid(colonyDir) {
		startupLog = "Removed stale loop PID file"
	}

	dbPath := filepath.Join(colonyDir, "missions.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = store.Close() }()

	return tui.Launch(tui.Options{
		Refresh:    refresh,
		NoColor:    tuiNoColor,
		Force:      tuiForce,
		StartView:  view,
		ColonyDir:  colonyDir,
		Root:       root,
		DBPath:     dbPath,
		StartupLog: startupLog,
	}, store)
}
