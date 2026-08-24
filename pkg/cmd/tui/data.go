package tui

import (
	"sort"
	"strings"
	"time"

	"github.com/futureboard-dev/colony/pkg/storage"
)

// QueueFilter captures the client-side queue view configuration.
type QueueFilter struct {
	State  string // "" = all, otherwise a task state
	Sort   string // "created" | "updated" | "state"
	Search string // fuzzy description search
}

// Apply reads all tasks and filters/sorts them client-side into the view model.
func Apply(tasks []storage.Task, f QueueFilter) []storage.Task {
	filtered := make([]storage.Task, 0, len(tasks))
	for _, t := range tasks {
		if f.State != "" && t.State != f.State {
			continue
		}
		if f.Search != "" && !fuzzyMatch(t.Description, f.Search) {
			continue
		}
		filtered = append(filtered, t)
	}

	switch f.Sort {
	case "updated":
		sort.SliceStable(filtered, func(i, j int) bool {
			// Tasks without an updated_at fall back to created_at.
			return taskTimestamp(filtered[i]).After(taskTimestamp(filtered[j]))
		})
	case "state":
		sort.SliceStable(filtered, func(i, j int) bool {
			if filtered[i].State != filtered[j].State {
				return stateOrder(filtered[i].State) < stateOrder(filtered[j].State)
			}
			return filtered[i].CreatedAt.Before(filtered[j].CreatedAt)
		})
	default: // "created"
		sort.SliceStable(filtered, func(i, j int) bool {
			return filtered[i].CreatedAt.Before(filtered[j].CreatedAt)
		})
	}
	return filtered
}

// SessionsFilter captures the client-side Sessions view configuration.
type SessionsFilter struct {
	Type   string // "" = all, otherwise a sessionType value
	Status string // "" = all, otherwise a session status
	Sort   string // "started" | "duration" | "status"
	TaskID string // "" = all tasks, otherwise scope to one task
}

// Session type and status values the filter bar cycles through. "" leads each
// list so cycling always returns to "all".
var (
	sessionTypes    = []string{"", "loop", "retry-gate", "escalation", "mission"}
	sessionStatuses = []string{"", "running", "completed", "failed", "interrupted"}
	sessionSorts    = []string{"started", "duration", "status"}
)

// sessionType classifies a session by its ID prefix. The loop names sessions
// "loop-", "retry-gate-" and "escalation-"; anything else is a standalone
// mission run.
func sessionType(s storage.Session) string {
	switch {
	case strings.HasPrefix(s.ID, "retry-gate-"):
		return "retry-gate"
	case strings.HasPrefix(s.ID, "escalation-"):
		return "escalation"
	case strings.HasPrefix(s.ID, "loop-"):
		return "loop"
	default:
		return "mission"
	}
}

// ApplySessions filters and sorts sessions for the Sessions view.
func ApplySessions(sessions []storage.Session, f SessionsFilter) []storage.Session {
	filtered := make([]storage.Session, 0, len(sessions))
	for _, s := range sessions {
		if f.Type != "" && sessionType(s) != f.Type {
			continue
		}
		if f.Status != "" && s.Status != f.Status {
			continue
		}
		if f.TaskID != "" && s.TaskID != f.TaskID {
			continue
		}
		filtered = append(filtered, s)
	}

	switch f.Sort {
	case "duration":
		sort.SliceStable(filtered, func(i, j int) bool {
			return sessionDuration(filtered[i].StartedAt, filtered[i].FinishedAt) >
				sessionDuration(filtered[j].StartedAt, filtered[j].FinishedAt)
		})
	case "status":
		sort.SliceStable(filtered, func(i, j int) bool {
			if filtered[i].Status != filtered[j].Status {
				return filtered[i].Status < filtered[j].Status
			}
			return filtered[i].StartedAt.After(filtered[j].StartedAt)
		})
	default: // "started" — newest first
		sort.SliceStable(filtered, func(i, j int) bool {
			return filtered[i].StartedAt.After(filtered[j].StartedAt)
		})
	}
	return filtered
}

// cycle returns the value after cur in values, wrapping at the end. An unknown
// cur restarts the cycle.
func cycle(values []string, cur string) string {
	for i, v := range values {
		if v == cur {
			return values[(i+1)%len(values)]
		}
	}
	return values[0]
}

// CountByState tallies tasks per state for the dashboard queue summary.
func CountByState(tasks []storage.Task) map[string]int {
	counts := map[string]int{
		"open": 0, "needs-fix": 0, "blocked": 0, "done": 0, "in-progress": 0,
	}
	for _, t := range tasks {
		counts[t.State]++
	}
	return counts
}

// taskTimestamp prefers UpdatedAt and falls back to CreatedAt.
func taskTimestamp(t storage.Task) time.Time {
	if t.UpdatedAt != nil {
		return *t.UpdatedAt
	}
	return t.CreatedAt
}

// stateOrder orders states for the "state" sort: open first, then in-progress,
// needs-fix, blocked, done.
func stateOrder(state string) int {
	switch state {
	case "open":
		return 0
	case "in-progress", "building":
		return 1
	case "needs-fix":
		return 2
	case "blocked":
		return 3
	case "done":
		return 4
	default:
		return 9
	}
}

// fuzzyMatch is a client-side fuzzy search on description. It succeeds when all
// query tokens appear in order (case-insensitive), tolerating gaps.
func fuzzyMatch(description, query string) bool {
	hay := strings.ToLower(description)
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return true
	}
	pos := 0
	for _, term := range terms {
		idx := strings.Index(hay[pos:], term)
		if idx < 0 {
			return false
		}
		pos += idx + len(term)
	}
	return true
}
