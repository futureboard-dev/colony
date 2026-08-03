package tui

import (
	"testing"
	"time"

	"github.com/futureboard-dev/colony/pkg/storage"
)

func TestPollerSmartRefresh(t *testing.T) {
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)

	t.Run("no newer data means no-op", func(t *testing.T) {
		p := NewPoller(nil)
		// Seed the high-water mark with a fetch.
		task := storage.Task{CreatedAt: base}
		p.SetFromTasks([]storage.Task{task})
		p.SetFromSessions([]storage.Session{{StartedAt: base}})

		// A tick observing only older/equal timestamps must be a no-op.
		markers := map[string]time.Time{
			tsTasks:    base,
			tsSessions: base,
		}
		if p.ChangedSince(markers) {
			t.Error("expected no change when timestamps are equal to the high-water mark")
		}
		if p.Changed() {
			t.Error("expected Changed flag to stay false")
		}
	})

	t.Run("newer task timestamp triggers re-query", func(t *testing.T) {
		p := NewPoller(nil)
		p.SetFromTasks([]storage.Task{{CreatedAt: base}})

		updated := base.Add(5 * time.Minute)
		markers := map[string]time.Time{tsTasks: updated}
		if !p.ChangedSince(markers) {
			t.Error("expected a change when updated_at is newer than the high-water mark")
		}
		if !p.Changed() {
			t.Error("expected Changed flag true after a change")
		}
	})

	t.Run("updated_at on task is detected", func(t *testing.T) {
		p := NewPoller(nil)
		p.SetFromTasks([]storage.Task{{CreatedAt: base}})

		up := base.Add(2 * time.Minute)
		upPtr := up
		task := storage.Task{CreatedAt: base, UpdatedAt: &upPtr}
		p.SetFromTasks([]storage.Task{task})

		// The seed itself already recorded the newer updated_at; a subsequent
		// tick with the same timestamp must show no change.
		if p.ChangedSince(TickMarkers([]storage.Task{task}, nil)) {
			t.Error("after seeding with the newer updated_at, equal timestamp should be a no-op")
		}
	})

	t.Run("newer session appears", func(t *testing.T) {
		p := NewPoller(nil)
		p.SetFromSessions([]storage.Session{{StartedAt: base}})
		if !p.ChangedSince(map[string]time.Time{tsSessions: base.Add(3 * time.Minute)}) {
			t.Error("expected change when a newer session started_at appears")
		}
	})
}

func TestLatestTaskTimestamp(t *testing.T) {
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	tasks := []storage.Task{
		{CreatedAt: base},
		{CreatedAt: base.Add(time.Hour)},
	}
	if got := LatestTaskTimestamp(tasks); !got.Equal(base.Add(time.Hour)) {
		t.Errorf("expected newest created_at, got %v", got)
	}

	// UpdatedAt wins over CreatedAt.
	up := base.Add(time.Hour + time.Minute)
	tasks = append(tasks, storage.Task{CreatedAt: base.Add(time.Hour), UpdatedAt: &up})
	if got := LatestTaskTimestamp(tasks); !got.Equal(up) {
		t.Errorf("expected newest updated_at, got %v", got)
	}
}
