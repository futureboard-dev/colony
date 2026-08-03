package tui

import (
	"time"

	"github.com/futureboard-dev/colony/pkg/storage"
)

// Poller computes whether a DB re-query is warranted on each tick, based on
// the newest `updated_at`/`started_at` seen since the last poll. This lets the
// TUI skip re-querying when nothing changed ("smart refresh").
type Poller struct {
	lastSeen map[string]time.Time
	now      func() time.Time
	// changed is set when the latest tick detected a change. It is read by the
	// model to decide whether to re-query storage.
	changed bool
}

// Timestamps is the set of high-water marks the poller tracks.
const (
	tsTasks    = "tasks"
	tsSessions = "sessions"
)

// NewPoller builds a poller whose clock can be injected for tests.
func NewPoller(now func() time.Time) *Poller {
	if now == nil {
		now = time.Now
	}
	return &Poller{
		lastSeen: make(map[string]time.Time),
		now:      now,
	}
}

// Record seeds the poller's high-water mark for an entity so the first tick
// behaves like a no-op unless a change occurs after the seed.
func (p *Poller) Record(entity string, ts time.Time) {
	if ts.After(p.lastSeen[entity]) {
		p.lastSeen[entity] = ts
	}
}

// SetFromTasks records the high-water mark for tasks from a fetched list.
func (p *Poller) SetFromTasks(tasks []storage.Task) {
	p.Record(tsTasks, LatestTaskTimestamp(tasks))
}

// SetFromSessions records the high-water mark for sessions from a fetched list.
func (p *Poller) SetFromSessions(sessions []storage.Session) {
	p.Record(tsSessions, LatestSessionTimestamp(sessions))
}

// ChangedSince reports whether any entity timestamp is newer than the stored
// high-water mark. It updates the poller's internal state and changed flag.
func (p *Poller) ChangedSince(updated map[string]time.Time) bool {
	changed := false
	for entity, ts := range updated {
		if ts.IsZero() {
			continue
		}
		if ts.After(p.lastSeen[entity]) {
			p.lastSeen[entity] = ts
			changed = true
		}
	}
	p.changed = changed
	return changed
}

// SetChanged forces the changed flag (e.g. after a full re-query returns data).
func (p *Poller) SetChanged(changed bool) {
	p.changed = changed
}

// Changed returns the current changed flag without mutating state.
func (p *Poller) Changed() bool {
	return p.changed
}

// TickMarkers gathers the latest timestamps from fresh fetched data into a map
// suitable for ChangedSince. Pass the freshly loaded lists.
func TickMarkers(tasks []storage.Task, sessions []storage.Session) map[string]time.Time {
	return map[string]time.Time{
		tsTasks:    LatestTaskTimestamp(tasks),
		tsSessions: LatestSessionTimestamp(sessions),
	}
}

// LatestTaskTimestamp extracts the newest non-zero timestamp among tasks.
func LatestTaskTimestamp(tasks []storage.Task) time.Time {
	var max time.Time
	for _, t := range tasks {
		if t.CreatedAt.After(max) {
			max = t.CreatedAt
		}
		if t.UpdatedAt != nil && t.UpdatedAt.After(max) {
			max = *t.UpdatedAt
		}
	}
	return max
}

// LatestSessionTimestamp extracts the newest started_at among sessions.
func LatestSessionTimestamp(sessions []storage.Session) time.Time {
	var max time.Time
	for _, s := range sessions {
		if s.StartedAt.After(max) {
			max = s.StartedAt
		}
	}
	return max
}
