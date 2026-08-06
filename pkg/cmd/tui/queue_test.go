package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/futureboard-dev/colony/pkg/storage"
)

func baseTasks() []storage.Task {
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	return []storage.Task{
		{ID: "t-1", Description: "fix login redirect loop", State: "open", CreatedAt: base},
		{ID: "t-2", Description: "add rate limiter to middleware", State: "needs-fix", CreatedAt: base.Add(time.Minute)},
		{ID: "t-3", Description: "update docs for auth", State: "done", CreatedAt: base.Add(2 * time.Minute)},
		{ID: "t-4", Description: "blocked refactor", State: "blocked", CreatedAt: base.Add(3 * time.Minute)},
	}
}

func ids(tasks []storage.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}

func TestQueueStateFilter(t *testing.T) {
	tasks := baseTasks()
	t.Run("all states", func(t *testing.T) {
		got := Apply(tasks, QueueFilter{State: ""})
		if len(got) != 4 {
			t.Fatalf("expected 4 tasks, got %d", len(got))
		}
	})
	t.Run("single state", func(t *testing.T) {
		got := Apply(tasks, QueueFilter{State: "needs-fix"})
		if len(got) != 1 || got[0].ID != "t-2" {
			t.Errorf("expected only the needs-fix task, got %v", ids(got))
		}
	})
	t.Run("counts by state", func(t *testing.T) {
		counts := CountByState(tasks)
		if counts["open"] != 1 || counts["done"] != 1 || counts["blocked"] != 1 || counts["needs-fix"] != 1 {
			t.Errorf("unexpected counts: %v", counts)
		}
	})
}

func TestQueueSort(t *testing.T) {
	tasks := baseTasks()
	t.Run("created order", func(t *testing.T) {
		got := Apply(tasks, QueueFilter{Sort: "created"})
		want := []string{"t-1", "t-2", "t-3", "t-4"}
		if !reflect.DeepEqual(ids(got), want) {
			t.Errorf("created sort mismatch: got %v want %v", ids(got), want)
		}
	})
	t.Run("state order groups", func(t *testing.T) {
		got := Apply(tasks, QueueFilter{Sort: "state"})
		if got[0].State != "open" {
			t.Errorf("expected open first, got %s", got[0].State)
		}
		if got[len(got)-1].State != "done" {
			t.Errorf("expected done last, got %s", got[len(got)-1].State)
		}
	})
	t.Run("updated sort uses updated_at", func(t *testing.T) {
		up := baseTasks()
		up[0].ID = "t-1"
		stamp := time.Date(2026, 8, 3, 11, 0, 0, 0, time.UTC)
		// Give t-1 (now removed) an updated_at so it sorts newest-first.
		for i := range up {
			if up[i].ID == "t-1" {
				up[i].UpdatedAt = &stamp
			}
		}
		got := Apply(up, QueueFilter{Sort: "updated"})
		if got[0].ID != "t-1" {
			t.Errorf("expected t-1 to sort first by updated_at, got %v", ids(got))
		}
	})
}

func TestQueueFuzzySearch(t *testing.T) {
	tasks := baseTasks()
	t.Run("matches description tokens", func(t *testing.T) {
		got := Apply(tasks, QueueFilter{Search: "rate limiter"})
		if len(got) != 1 || got[0].ID != "t-2" {
			t.Errorf("expected only rate-limiter task, got %v", ids(got))
		}
	})
	t.Run("no match", func(t *testing.T) {
		got := Apply(tasks, QueueFilter{Search: "zzz notpresent"})
		if len(got) != 0 {
			t.Errorf("expected no matches, got %v", ids(got))
		}
	})
	t.Run("case insensitive", func(t *testing.T) {
		got := Apply(tasks, QueueFilter{Search: "FIX LOGIN"})
		if len(got) != 1 || got[0].ID != "t-1" {
			t.Errorf("expected case-insensitive match, got %v", ids(got))
		}
	})
}

func TestQueueLangUnsetBadge(t *testing.T) {
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	st := &stubStore{tasks: []storage.Task{
		{ID: "t-legacy", Description: "created before --lang", State: "open", CreatedAt: base},
		{ID: "t-new", Description: "created after --lang", State: "open", Lang: "go", CreatedAt: base},
	}}
	m := newTestModel(t, ViewQueue, st)

	t.Run("table marks the legacy task", func(t *testing.T) {
		out := m.queueTable(m.tasks, 118, 10)
		if !strings.Contains(out, "unset") {
			t.Errorf("expected an unset lang badge in the table, got:\n%s", out)
		}
		if !strings.Contains(out, "go") {
			t.Errorf("expected the set language to still render, got:\n%s", out)
		}
	})

	t.Run("detail explains the fallback", func(t *testing.T) {
		m.cursor = 0
		if out := m.queueDetail(m.tasks, 118, 12); !strings.Contains(out, langUnsetNote) {
			t.Errorf("expected the unset-lang note for a legacy task, got:\n%s", out)
		}
		m.cursor = 1
		if out := m.queueDetail(m.tasks, 118, 12); strings.Contains(out, langUnsetNote) {
			t.Errorf("did not expect the unset-lang note for a task with a language, got:\n%s", out)
		}
	})
}
