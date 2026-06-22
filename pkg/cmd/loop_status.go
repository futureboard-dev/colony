package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jirateep/colony/pkg/storage"
	"github.com/spf13/cobra"
)

var (
	statusState string
	statusJSON  bool
)

var loopStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the loop queue, feedback, daemon liveness, and recent sessions",
	Long: `Display the current loop status: daemon running/not (pid, uptime),
queued tasks (open/needs-fix), blocked/needs-fix feedback, and recent loop and
escalation sessions.

Flags:
  --state <s>    Filter queue by state (open, needs-fix, blocked, done)
  --json         Emit structured JSON output`,
	RunE: runLoopStatus,
}

func init() {
	loopStatusCmd.Flags().StringVar(&statusState, "state", "", "filter queue by state (open, needs-fix, blocked, done)")
	loopStatusCmd.Flags().BoolVar(&statusJSON, "json", false, "emit structured JSON output")
}

func runLoopStatus(cmd *cobra.Command, args []string) error {
	_, root, err := loadConfig()
	if err != nil {
		return err
	}

	store, err := openLoopStore(root)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = store.Close() }()

	// 0. Daemon liveness (pid, uptime) — only in text mode.
	if !statusJSON {
		emitDaemonStatus(cmd, root)
	}

	// 1. Build queue filter.
	stateFilter := []string{"open", "needs-fix", "blocked"}
	if statusState != "" {
		stateFilter = []string{statusState}
	}

	tasks, err := store.QueryTasks(storage.TaskFilter{States: stateFilter})
	if err != nil {
		return fmt.Errorf("query tasks: %w", err)
	}

	// 2. Fetch all sessions, filter in-memory.
	allSessions, err := store.QuerySessions(storage.SessionFilter{})
	if err != nil {
		return fmt.Errorf("query sessions: %w", err)
	}

	loopSessions := filterLoopSessions(allSessions)
	sortSessionsByStartedAt(loopSessions, false) // newest first

	// Cap to 10.
	if len(loopSessions) > 10 {
		loopSessions = loopSessions[:10]
	}

	if statusJSON {
		return emitJSONStatus(cmd, tasks, loopSessions)
	}

	return emitTextStatus(cmd, tasks, loopSessions)
}

// filterLoopSessions returns only sessions with mission_name starting with "loop-" or "escalation-".
func filterLoopSessions(sessions []storage.Session) []storage.Session {
	var out []storage.Session
	for _, s := range sessions {
		if strings.HasPrefix(s.MissionName, "loop-") || strings.HasPrefix(s.MissionName, "escalation-") {
			out = append(out, s)
		}
	}
	return out
}

func sortSessionsByStartedAt(sessions []storage.Session, asc bool) {
	sort.Slice(sessions, func(i, j int) bool {
		if asc {
			return sessions[i].StartedAt.Before(sessions[j].StartedAt)
		}
		return sessions[i].StartedAt.After(sessions[j].StartedAt)
	})
}

// ----- text output -----

func emitTextStatus(cmd *cobra.Command, tasks []storage.Task, sessions []storage.Session) error {
	cmd.Println("=== Queue ===")
	if len(tasks) == 0 {
		cmd.Println("  (empty)")
	} else {
		for _, t := range tasks {
			cmd.Printf("  %-12s %-12s %s\n", t.ID, t.State, truncate(t.Description, 60))
		}
	}

	cmd.Println()
	cmd.Println("=== Feedback ===")
	hasFeedback := false
	for _, t := range tasks {
		if (t.State == "blocked" || t.State == "needs-fix") && t.LastFeedback != "" {
			hasFeedback = true
			cmd.Printf("  [%s] %s\n", t.ID, t.State)
			cmd.Printf("    %s\n", indentBlock(t.LastFeedback, 4))
		}
	}
	if !hasFeedback {
		cmd.Println("  (none)")
	}

	cmd.Println()
	cmd.Println("=== Recent Sessions ===")
	if len(sessions) == 0 {
		cmd.Println("  (none)")
	} else {
		for _, s := range sessions {
			dur := durationStr(s.StartedAt, s.FinishedAt)
			cmd.Printf("  %-30s %-10s %s\n", s.ID, s.Status, dur)
		}
	}

	return nil
}

// ----- JSON output -----

type jsonStatus struct {
	Queue    []jsonTask    `json:"queue"`
	Sessions []jsonSession `json:"sessions"`
}

type jsonTask struct {
	ID           string `json:"id"`
	Description  string `json:"description"`
	State        string `json:"state"`
	LastFeedback string `json:"last_feedback,omitempty"`
	CycleCount   int    `json:"cycle_count"`
}

type jsonSession struct {
	ID          string  `json:"id"`
	MissionName string  `json:"mission_name"`
	Status      string  `json:"status"`
	StartedAt   string  `json:"started_at"`
	FinishedAt  *string `json:"finished_at,omitempty"`
	Duration    string  `json:"duration"`
}

func emitJSONStatus(cmd *cobra.Command, tasks []storage.Task, sessions []storage.Session) error {
	out := jsonStatus{}

	for _, t := range tasks {
		out.Queue = append(out.Queue, jsonTask{
			ID:           t.ID,
			Description:  t.Description,
			State:        t.State,
			LastFeedback: t.LastFeedback,
			CycleCount:   t.CycleCount,
		})
	}

	for _, s := range sessions {
		js := jsonSession{
			ID:          s.ID,
			MissionName: s.MissionName,
			Status:      s.Status,
			StartedAt:   s.StartedAt.Format(time.RFC3339),
			Duration:    durationStr(s.StartedAt, s.FinishedAt),
		}
		if s.FinishedAt != nil {
			f := s.FinishedAt.Format(time.RFC3339)
			js.FinishedAt = &f
		}
		out.Sessions = append(out.Sessions, js)
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// ----- helpers -----

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func indentBlock(s string, indent int) string {
	prefix := strings.Repeat(" ", indent)
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func durationStr(start time.Time, finished *time.Time) string {
	if finished == nil {
		return "–"
	}
	d := finished.Sub(start).Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
}

// emitDaemonStatus prints the --watch daemon liveness (running/not, pid,
// uptime) based on the project's loop.pid file.
func emitDaemonStatus(cmd *cobra.Command, root string) {
	colonyPath := filepath.Join(root, ".colony")
	pidPath := filepath.Join(colonyPath, "loop.pid")

	cmd.Println("=== Daemon ===")
	running, pid, _ := daemonUptime(colonyPath)
	switch {
	case running:
		cmd.Printf("  running (pid=%d)\n", pid)
		if fi, err := os.Stat(pidPath); err == nil {
			uptime := time.Since(fi.ModTime()).Truncate(time.Second)
			cmd.Printf("  uptime: %s\n", uptime)
		}
	case pid > 0:
		cmd.Printf("  stale pidfile (pid=%d) — process gone\n", pid)
	default:
		cmd.Println("  not running")
	}
	cmd.Println()
}
