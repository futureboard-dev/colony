package cmd

import (
	"bufio"
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/futureboard-dev/colony/pkg/fix"
	"github.com/futureboard-dev/colony/pkg/storage"
)

// fakeResolver and fakeSession stand in for the agent CLIs, which can't be
// driven from a test: one reports what is installed, the other records launches.
type fakeResolver struct{ tools []fix.Tool }

func (f fakeResolver) InstalledTools(context.Context) ([]fix.Tool, error) { return f.tools, nil }
func (f fakeResolver) ResolveChoice(_ context.Context, req fix.Request) (fix.Choice, error) {
	if req.Choice.Tool != "" {
		return req.Choice, nil
	}
	return fix.Choice{Tool: f.tools[0]}, nil
}

type fakeSession struct {
	reqs []fix.Request
	err  error
}

func (f *fakeSession) OpenSession(_ context.Context, req fix.Request) error {
	f.reqs = append(f.reqs, req)
	return f.err
}

// runBlockPrompt runs the menu for a blocked task, answering with input.
func runBlockPrompt(t *testing.T, input string, session *fakeSession) (*storage.SQLiteStore, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "missions.db")
	t.Setenv("COLONY_DB_PATH", dbPath) // fix.Retry opens the default DB path
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	task := storage.Task{
		ID: "t1", Description: "d", State: "blocked", BlockTool: "claude",
		LastFeedback: "lint failed: x.go:1", CreatedAt: time.Now(),
	}
	if err := store.InsertTask(task); err != nil {
		t.Fatal(err)
	}

	fix.SetResolver(fakeResolver{tools: []fix.Tool{fix.ToolClaude, fix.ToolCodex}})
	fix.SetSession(session)
	t.Cleanup(func() { fix.SetResolver(nil); fix.SetSession(nil) })

	var out bytes.Buffer
	p := blockPrompt{root: t.TempDir(), store: store, in: bufio.NewReader(strings.NewReader(input)), out: &out}
	p.run(context.Background(), &task)
	return store, out.String()
}

func TestBlockPrompt(t *testing.T) {
	t.Run("shows the options", func(t *testing.T) {
		_, out := runBlockPrompt(t, "s\n", &fakeSession{})
		for _, want := range []string{"Task t1 blocked", "[f] Fix with agent", "[t] Tell it what to do", "[o] Open shell", "[s] Skip"} {
			if !strings.Contains(out, want) {
				t.Errorf("menu missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("enter defaults to fix, then resume re-queues", func(t *testing.T) {
		session := &fakeSession{}
		store, _ := runBlockPrompt(t, "\n\n\n\n", session) // choice, tool, model, resume
		if len(session.reqs) != 1 {
			t.Fatalf("sessions opened = %d, want 1", len(session.reqs))
		}
		req := session.reqs[0]
		if req.Reason != "lint failed: x.go:1" {
			t.Errorf("session Reason = %q, want the block reason", req.Reason)
		}
		if req.Choice.Tool != fix.ToolClaude {
			t.Errorf("session tool = %q, want the stuck tool preselected", req.Choice.Tool)
		}
		if got := taskState(t, store, "t1"); got.State != "needs-fix" || got.LastFeedback != "lint failed: x.go:1" {
			t.Errorf("task = %q/%q, want needs-fix with the reason kept", got.State, got.LastFeedback)
		}
	})

	t.Run("tool and model can be changed", func(t *testing.T) {
		session := &fakeSession{}
		runBlockPrompt(t, "f\ncodex\ngpt-x\nn\n", session)
		if len(session.reqs) != 1 {
			t.Fatalf("sessions opened = %d, want 1", len(session.reqs))
		}
		if got := session.reqs[0].Choice; got.Tool != fix.ToolCodex || got.Model != "gpt-x" {
			t.Errorf("Choice = %+v, want codex/gpt-x", got)
		}
	})

	t.Run("declining resume stays blocked", func(t *testing.T) {
		store, _ := runBlockPrompt(t, "f\n\n\nn\n", &fakeSession{})
		if got := taskState(t, store, "t1"); got.State != "blocked" {
			t.Errorf("state = %q, want blocked", got.State)
		}
	})

	t.Run("a failed session returns to the menu without resuming", func(t *testing.T) {
		session := &fakeSession{err: errString("unknown model")}
		store, out := runBlockPrompt(t, "f\n\nbad-model\ns\n", session)
		if !strings.Contains(out, "unknown model") {
			t.Errorf("output should show the tool's error:\n%s", out)
		}
		if got := taskState(t, store, "t1"); got.State != "blocked" {
			t.Errorf("state = %q, want blocked", got.State)
		}
	})

	t.Run("hint re-queues without a session", func(t *testing.T) {
		session := &fakeSession{}
		store, _ := runBlockPrompt(t, "t\nuse the v2 schema\n", session)
		if len(session.reqs) != 0 {
			t.Errorf("sessions opened = %d, want 0", len(session.reqs))
		}
		got := taskState(t, store, "t1")
		if got.State != "needs-fix" || !strings.Contains(got.LastFeedback, "use the v2 schema") || !strings.Contains(got.LastFeedback, "lint failed") {
			t.Errorf("task = %q/%q, want needs-fix with reason and hint", got.State, got.LastFeedback)
		}
	})

	t.Run("shell then resume re-queues", func(t *testing.T) {
		t.Setenv("SHELL", "/usr/bin/true")
		store, _ := runBlockPrompt(t, "o\ny\n", &fakeSession{})
		if got := taskState(t, store, "t1"); got.State != "needs-fix" {
			t.Errorf("state = %q, want needs-fix", got.State)
		}
	})

	t.Run("skip and end of input leave it blocked", func(t *testing.T) {
		for _, input := range []string{"s\n", ""} {
			store, _ := runBlockPrompt(t, input, &fakeSession{})
			if got := taskState(t, store, "t1"); got.State != "blocked" {
				t.Errorf("input %q: state = %q, want blocked", input, got.State)
			}
		}
	})
}

func TestOfferBlockRecovery_NoTerminalNeverPrompts(t *testing.T) {
	orig := loopHasTerminal
	loopHasTerminal = func() bool { return false }
	t.Cleanup(func() { loopHasTerminal = orig })

	store := openBlockTestStore(t)
	if err := store.InsertTask(storage.Task{ID: "t1", State: "blocked", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	session := &fakeSession{}
	fix.SetSession(session)
	t.Cleanup(func() { fix.SetSession(nil) })

	offerBlockRecovery(context.Background(), t.TempDir(), store, "t1")

	if len(session.reqs) != 0 {
		t.Errorf("sessions opened = %d, want 0 without a terminal", len(session.reqs))
	}
	if got := taskState(t, store, "t1"); got.State != "blocked" {
		t.Errorf("state = %q, want blocked", got.State)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
