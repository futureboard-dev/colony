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
