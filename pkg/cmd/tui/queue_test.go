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

	t.Run("counts running", func(t *testing.T) {
		withRunning := append(baseTasks(), storage.Task{ID: "t-5", Description: "running task", State: "running", CreatedAt: time.Now()})
		counts := CountByState(withRunning)
		if counts["running"] != 1 {
			t.Errorf("expected 1 running task, got %d", counts["running"])
		}
	})

	t.Run("filter running", func(t *testing.T) {
		withRunning := append(baseTasks(), storage.Task{ID: "t-5", Description: "running task", State: "running", CreatedAt: time.Now()})
		got := Apply(withRunning, QueueFilter{State: "running"})
		if len(got) != 1 || got[0].ID != "t-5" {
			t.Errorf("expected only the running task, got %v", ids(got))
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

func TestQueueDetail_GroupInfo(t *testing.T) {
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	st := &stubStore{tasks: []storage.Task{
		{
			ID: "t-models", Description: "models", State: "done", GroupID: "g1",
			SpecPath:   "/repo/.colony/specs/payments/parts/01-models.md",
			BaseBranch: "feature/payments", CreatedAt: base,
		},
		{
			ID: "t-api", Description: "api", State: "open", GroupID: "g1", DependsOn: "t-models",
			SpecPath:   "/repo/.colony/specs/payments/parts/02-api.md",
			BaseBranch: "feature/payments", CreatedAt: base.Add(time.Minute),
		},
		{
			ID: "t-ui", Description: "ui", State: "open", GroupID: "g1", DependsOn: "t-models,t-cli",
			SpecPath:   "/repo/.colony/specs/payments/parts/03-ui.md",
			BaseBranch: "feature/payments", CreatedAt: base.Add(2 * time.Minute),
		},
		{
			ID: "t-cli", Description: "cli", State: "blocked", GroupID: "g1",
			SpecPath:   "/repo/.colony/specs/payments/parts/04-cli.md",
			BaseBranch: "feature/payments", CreatedAt: base.Add(3 * time.Minute),
		},
		{ID: "t-solo", Description: "solo task", State: "open", CreatedAt: base.Add(4 * time.Minute)},
	}}
	m := newTestModel(t, ViewQueue, st)

	t.Run("group task shows group, epic, and unmet deps", func(t *testing.T) {
		m.cursor = 1 // t-api, depends on t-models (already done)
		out := m.queueDetail(m.tasks, 118, 20)
		if !strings.Contains(out, "payments") {
			t.Errorf("expected the group name, got:\n%s", out)
		}
		if !strings.Contains(out, "feature/payments") {
			t.Errorf("expected the epic branch, got:\n%s", out)
		}
		if strings.Contains(out, "Waiting on:") {
			t.Errorf("expected no unmet deps for t-api (its only dep is done), got:\n%s", out)
		}
	})

	t.Run("group task with an unmet dependency shows it", func(t *testing.T) {
		m.cursor = 2 // t-ui, depends on t-models (done) and t-cli (blocked)
		out := m.queueDetail(m.tasks, 118, 20)
		if !strings.Contains(out, "Waiting on:") || !strings.Contains(out, "04-cli") {
			t.Errorf("expected the unmet blocked dependency, got:\n%s", out)
		}
		if strings.Contains(out, "01-models") {
			t.Errorf("did not expect the already-done dependency listed, got:\n%s", out)
		}
	})

	t.Run("non-group task shows none of it", func(t *testing.T) {
		m.cursor = 4 // t-solo
		out := m.queueDetail(m.tasks, 118, 20)
		if strings.Contains(out, "Group:") || strings.Contains(out, "Epic:") || strings.Contains(out, "Waiting on:") {
			t.Errorf("expected no group/epic/waiting info for a non-group task, got:\n%s", out)
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
