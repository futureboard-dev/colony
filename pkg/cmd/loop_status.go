package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/futureboard-dev/colony/pkg/storage"
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
	stateFilter := []string{"open", "needs-fix", "running", "blocked"}
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

	groups, err := buildGroupStatuses(store)
	if err != nil {
		return fmt.Errorf("build group status: %w", err)
	}

	if statusJSON {
		return emitJSONStatus(cmd, root, tasks, loopSessions, store, groups)
	}

	return emitTextStatus(cmd, tasks, loopSessions, groups)
}

// groupPartStatus describes one group task's status line in `loop status`.
type groupPartStatus struct {
	ID     string // part label, e.g. "01-models" (from the task's SpecPath)
	Detail string // "done" | "blocked" | "running" | "waiting on <ids>[ (blocked)]"
}

// groupStatusView is the display-ready summary of one task_groups row.
type groupStatusView struct {
	Name       string
	EpicBranch string
	PRURL      string
	Done       int
	Total      int
	Parts      []groupPartStatus
}

// partLabel derives a group task's display label from its part spec file
// name (e.g. ".../parts/02-api.md" -> "02-api"), falling back to the task ID
// for tasks with no spec path.
func partLabel(t storage.Task) string {
	if t.SpecPath == "" {
		return t.ID
	}
	base := filepath.Base(t.SpecPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// buildGroupStatuses assembles the Groups section shown by both the text and
// JSON `loop status` output.
func buildGroupStatuses(store *storage.SQLiteStore) ([]groupStatusView, error) {
	groups, err := store.QueryGroups(storage.GroupFilter{})
	if err != nil {
		return nil, err
	}

	views := make([]groupStatusView, 0, len(groups))
	for _, g := range groups {
		tasks, err := store.QueryTasks(storage.TaskFilter{GroupID: g.ID})
		if err != nil {
			return nil, err
		}

		labelByID := make(map[string]string, len(tasks))
		stateByID := make(map[string]string, len(tasks))
		for _, t := range tasks {
			labelByID[t.ID] = partLabel(t)
			stateByID[t.ID] = t.State
		}

		v := groupStatusView{Name: g.Name, EpicBranch: g.EpicBranch, PRURL: g.PRURL, Total: len(tasks)}
		for _, t := range tasks {
			part := groupPartStatus{ID: partLabel(t)}
			switch t.State {
			case "done":
				part.Detail = "done"
				v.Done++
			case "blocked":
				part.Detail = "blocked"
			case "running":
				part.Detail = "running"
			default: // open, needs-fix: claimable once every dependency is done
				var unmet []string
				blockedDep := false
				for depID := range strings.SplitSeq(t.DependsOn, ",") {
					depID = strings.TrimSpace(depID)
					if depID == "" {
						continue
					}
					if stateByID[depID] != "done" {
						unmet = append(unmet, labelByID[depID])
						if stateByID[depID] == "blocked" {
							blockedDep = true
						}
					}
				}
				if len(unmet) == 0 {
					part.Detail = t.State
				} else {
					part.Detail = "waiting on " + strings.Join(unmet, ", ")
					if blockedDep {
						part.Detail += " (blocked)"
					}
				}
			}
			v.Parts = append(v.Parts, part)
		}
		views = append(views, v)
	}
	return views, nil
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

func emitTextStatus(cmd *cobra.Command, tasks []storage.Task, sessions []storage.Session, groups []groupStatusView) error {
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

	cmd.Println()
	cmd.Println("=== Groups ===")
	if len(groups) == 0 {
		cmd.Println("  (none)")
	} else {
		for _, g := range groups {
			cmd.Printf("  %s  (%s)  %d/%d done\n", g.Name, g.EpicBranch, g.Done, g.Total)
			for _, p := range g.Parts {
				cmd.Printf("    %-16s %s\n", p.ID, p.Detail)
			}
			if g.PRURL != "" {
				cmd.Printf("    PR: %s\n", g.PRURL)
			}
		}
	}

	return nil
}

// ----- JSON output -----

type jsonStatus struct {
	Queue    []jsonTask    `json:"queue"`
	Sessions []jsonSession `json:"sessions"`
	Daemon   jsonDaemon    `json:"daemon"`
	Groups   []jsonGroup   `json:"groups"`
}

type jsonGroupPart struct {
	ID     string `json:"id"`
	Detail string `json:"detail"`
}

type jsonGroup struct {
	Name       string          `json:"name"`
	EpicBranch string          `json:"epic_branch"`
	Done       int             `json:"done"`
	Total      int             `json:"total"`
	PRURL      string          `json:"pr_url,omitempty"`
	Parts      []jsonGroupPart `json:"parts"`
}

// jsonDaemon captures loop process liveness from the PID file.
type jsonDaemon struct {
	Running bool   `json:"running"`
	Pid     int    `json:"pid,omitempty"`
	Status  string `json:"status"`
}

type jsonTask struct {
	ID           string `json:"id"`
	Description  string `json:"description"`
	State        string `json:"state"`
	LastFeedback string `json:"last_feedback,omitempty"`
	CycleCount   int    `json:"cycle_count"`
	StepOutput   string `json:"step_output,omitempty"`
}

type jsonSession struct {
	ID          string  `json:"id"`
	MissionName string  `json:"mission_name"`
	TaskID      string  `json:"task_id,omitempty"`
	Status      string  `json:"status"`
	StartedAt   string  `json:"started_at"`
	FinishedAt  *string `json:"finished_at,omitempty"`
	Duration    string  `json:"duration"`
}

func emitJSONStatus(cmd *cobra.Command, root string, tasks []storage.Task, sessions []storage.Session, store *storage.SQLiteStore, groups []groupStatusView) error {
	out := jsonStatus{}

	// Daemon liveness from the PID file.
	running, pid, _ := daemonUptime(filepath.Join(root, ".colony"))
	out.Daemon.Running = running
	out.Daemon.Pid = pid
	switch {
	case running:
		out.Daemon.Status = "running"
	case pid > 0:
		out.Daemon.Status = "stale"
	default:
		out.Daemon.Status = "idle"
	}

	for _, t := range tasks {
		jt := jsonTask{
			ID:           t.ID,
			Description:  t.Description,
			State:        t.State,
			LastFeedback: t.LastFeedback,
			CycleCount:   t.CycleCount,
		}
		// Include the latest captured gate step output, if any, so the JSON
		// surface exposes per-step feedback.
		if steps, err := store.QuerySteps(storage.StepFilter{Decision: "REJECTED"}); err == nil {
			for i := len(steps) - 1; i >= 0; i-- {
				if steps[i].Role == "gate" && steps[i].Output != "" {
					jt.StepOutput = steps[i].Output
					break
				}
			}
		}
		out.Queue = append(out.Queue, jt)
	}

	for _, s := range sessions {
		js := jsonSession{
			ID:          s.ID,
			MissionName: s.MissionName,
			TaskID:      s.TaskID,
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

	for _, g := range groups {
		jg := jsonGroup{Name: g.Name, EpicBranch: g.EpicBranch, Done: g.Done, Total: g.Total, PRURL: g.PRURL}
		for _, p := range g.Parts {
			jg.Parts = append(jg.Parts, jsonGroupPart(p))
		}
		out.Groups = append(out.Groups, jg)
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
