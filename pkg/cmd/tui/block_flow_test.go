package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/futureboard-dev/colony/pkg/fix"
)

// openBlocked focuses task t-2 (blocked, in sampleStore) and opens the block
// flow, draining the automatic-triage step so tests land on the action menu
// without depending on a real Triager.
func openBlocked(t *testing.T, m *Model) {
	t.Helper()
	focusTask(t, m, "t-2", "blocked")
	for i := range m.tasks {
		if m.tasks[i].ID == "t-2" {
			m.tasks[i].LastFeedback = "cannot find config/schema.yaml"
		}
	}
	if st, ok := m.store.(*stubStore); ok {
		for i := range st.tasks {
			if st.tasks[i].ID == "t-2" {
				st.tasks[i].LastFeedback = "cannot find config/schema.yaml"
			}
		}
	}

	out, cmd := m.Update(key("f"))
	*m = *out.(*Model)
	if m.modal != ModalBlockFlow {
		t.Fatalf("f on a blocked task should open ModalBlockFlow, got modal %d", m.modal)
	}
	if m.blockFlow == nil || m.blockFlow.stage != blockStageDiagnosing {
		t.Fatalf("expected the diagnosing stage first, got %+v", m.blockFlow)
	}
	if cmd == nil {
		t.Fatal("opening the block flow should fire a diagnosis command")
	}
	out, _ = m.Update(cmd())
	*m = *out.(*Model)
	if m.blockFlow == nil || m.blockFlow.stage != blockStageAction {
		t.Fatalf("expected the action stage after diagnosis, got %+v", m.blockFlow)
	}
}

// swapFix temporarily replaces the fix.* package vars this file calls
// through, restoring them after the test.
func swapFix(t *testing.T) {
	t.Helper()
	origInstalled, origResolve, origExcl := fixInstalledTools, fixResolveChoice, fixResolveChoiceExcluding
	origOpen, origRetry, origDiag, origExplain, origInvalid := fixOpenSession, fixRetry, fixDiagnose, fixExplain, fixIsInvalidModel
	t.Cleanup(func() {
		fixInstalledTools, fixResolveChoice, fixResolveChoiceExcluding = origInstalled, origResolve, origExcl
		fixOpenSession, fixRetry, fixDiagnose, fixExplain, fixIsInvalidModel = origOpen, origRetry, origDiag, origExplain, origInvalid
	})
	// Deterministic no-op diagnosis/explanation by default so openBlocked
	// doesn't depend on a real Triager/Explainer being registered.
	fixDiagnose = func(context.Context, fix.Request) (fix.Diagnosis, error) {
		return fix.Diagnosis{}, fix.ErrNotConfigured
	}
	fixExplain = func(ctx context.Context, req fix.Request) (fix.Explanation, error) {
		return fix.Explanation{Cause: req.Reason}, nil
	}
}

func TestBlockFlowDiagnosingEscShowsOptions(t *testing.T) {
	swapFix(t)
	m := newTestModel(t, ViewQueue, sampleStore())
	focusTask(t, m, "t-2", "blocked")

	var attemptCtx context.Context
	fixDiagnose = func(ctx context.Context, _ fix.Request) (fix.Diagnosis, error) {
		attemptCtx = ctx
		return fix.Diagnosis{Fixed: true, Change: "edited a file"}, nil
	}

	out, cmd := m.Update(key("f"))
	m = out.(*Model)
	if m.blockFlow == nil || m.blockFlow.stage != blockStageDiagnosing {
		t.Fatalf("expected the diagnosing stage, got %+v", m.blockFlow)
	}

	out, _ = m.Update(key("esc"))
	m = out.(*Model)
	if m.modal != ModalBlockFlow || m.blockFlow.stage != blockStageAction {
		t.Fatalf("esc should move to the action menu, got modal %d, flow %+v", m.modal, m.blockFlow)
	}

	// The abandoned attempt sees its cancelled context, and its late result
	// must not close the menu or retry the task.
	out, _ = m.Update(cmd())
	m = out.(*Model)
	if attemptCtx.Err() == nil {
		t.Error("esc should cancel the triage attempt")
	}
	if m.blockFlow == nil || m.blockFlow.stage != blockStageAction {
		t.Fatalf("a stale triage result should leave the menu open, got %+v", m.blockFlow)
	}
}

func TestBlockFlowActionKeys(t *testing.T) {
	t.Run("t opens the hint stage", func(t *testing.T) {
		swapFix(t)
		m := newTestModel(t, ViewQueue, sampleStore())
		openBlocked(t, m)

		out, _ := m.Update(key("t"))
		m2 := out.(*Model)
		if m2.blockFlow.stage != blockStageHint {
			t.Fatalf("t should open the hint stage, got %d", m2.blockFlow.stage)
		}
	})

	t.Run("o opens a shell command", func(t *testing.T) {
		swapFix(t)
		t.Setenv("SHELL", "/bin/sh")
		m := newTestModel(t, ViewQueue, sampleStore())
		openBlocked(t, m)

		_, cmd := m.Update(key("o"))
		if cmd == nil {
			t.Fatal("o should suspend the TUI with a shell command")
		}
		if m.modal != ModalNone {
			t.Fatalf("suspending for a shell should clear the modal, got %d", m.modal)
		}
	})

	t.Run("s skips and leaves the task blocked", func(t *testing.T) {
		swapFix(t)
		st := sampleStore()
		m := newTestModel(t, ViewQueue, st)
		openBlocked(t, m)

		out, _ := m.Update(key("s"))
		m2 := out.(*Model)
		if m2.modal != ModalNone || m2.blockFlow != nil {
			t.Fatal("skip should close the block flow")
		}
		for _, task := range st.tasks {
			if task.ID == "t-2" && task.State != "blocked" {
				t.Errorf("skip must not change task state, got %q", task.State)
			}
		}
	})

	t.Run("esc cancels without mutating the task", func(t *testing.T) {
		swapFix(t)
		st := sampleStore()
		m := newTestModel(t, ViewQueue, st)
		openBlocked(t, m)

		out, _ := m.Update(key("esc"))
		m2 := out.(*Model)
		if m2.modal != ModalNone || m2.blockFlow != nil {
			t.Fatal("esc should close the block flow")
		}
		for _, task := range st.tasks {
			if task.ID == "t-2" && task.State != "blocked" {
				t.Errorf("esc must not change task state, got %q", task.State)
			}
		}
	})
}

func TestBlockFlowPickerInstallCounts(t *testing.T) {
	t.Run("none installed blocks the flow", func(t *testing.T) {
		swapFix(t)
		fixInstalledTools = func(context.Context) ([]fix.Tool, error) { return nil, nil }
		m := newTestModel(t, ViewQueue, sampleStore())
		openBlocked(t, m)

		out, cmd := m.Update(key("f"))
		m2 := out.(*Model)
		if cmd == nil {
			t.Fatal("f should look up installed tools")
		}
		out, _ = m2.Update(cmd())
		m3 := out.(*Model)
		if m3.blockFlow.stage != blockStageAction {
			t.Fatalf("no tools installed should return to the action stage, got %d", m3.blockFlow.stage)
		}
		if m3.blockFlow.err == "" {
			t.Fatal("expected an install hint when no tool is found")
		}
	})

	t.Run("one installed tool is used without asking", func(t *testing.T) {
		swapFix(t)
		fixInstalledTools = func(context.Context) ([]fix.Tool, error) { return []fix.Tool{fix.ToolClaude}, nil }
		fixResolveChoice = func(ctx context.Context, req fix.Request) (fix.Choice, error) {
			return fix.Choice{Tool: fix.ToolClaude}, nil
		}
		m := newTestModel(t, ViewQueue, sampleStore())
		openBlocked(t, m)

		out, cmd := m.Update(key("f"))
		out, _ = out.(*Model).Update(cmd())
		m2 := out.(*Model)
		if m2.blockFlow.stage != blockStagePicker {
			t.Fatalf("expected the picker stage, got %d", m2.blockFlow.stage)
		}
		if len(m2.blockFlow.picker.tools) != 1 || m2.blockFlow.picker.tools[0] != fix.ToolClaude {
			t.Fatalf("expected claude preselected as the sole tool, got %+v", m2.blockFlow.picker.tools)
		}
	})

	t.Run("several installed tools show the picker preselected", func(t *testing.T) {
		swapFix(t)
		fixInstalledTools = func(context.Context) ([]fix.Tool, error) {
			return []fix.Tool{fix.ToolClaude, fix.ToolCodex, fix.ToolCrush}, nil
		}
		fixResolveChoice = func(ctx context.Context, req fix.Request) (fix.Choice, error) {
			return fix.Choice{Tool: fix.ToolCodex}, nil
		}
		m := newTestModel(t, ViewQueue, sampleStore())
		openBlocked(t, m)

		out, cmd := m.Update(key("f"))
		out, _ = out.(*Model).Update(cmd())
		m2 := out.(*Model)
		if m2.blockFlow.stage != blockStagePicker {
			t.Fatalf("expected the picker stage, got %d", m2.blockFlow.stage)
		}
		p := m2.blockFlow.picker
		if len(p.tools) != 3 || p.tools[p.index] != fix.ToolCodex {
			t.Fatalf("expected codex preselected among 3 tools, got index %d of %+v", p.index, p.tools)
		}
	})
}

func TestBlockFlowInvalidModelReturnsToPickerWithoutBlocking(t *testing.T) {
	swapFix(t)
	fixInstalledTools = func(context.Context) ([]fix.Tool, error) {
		return []fix.Tool{fix.ToolClaude, fix.ToolCodex}, nil
	}
	fixResolveChoice = func(ctx context.Context, req fix.Request) (fix.Choice, error) {
		return fix.Choice{Tool: fix.ToolClaude, Model: "bogus-model"}, nil
	}
	fixIsInvalidModel = func(err error) bool { return err != nil && err.Error() == "model rejected" }
	fixResolveChoiceExcluding = func(ctx context.Context, req fix.Request, bad string) (fix.Choice, error) {
		if bad != "bogus-model" {
			t.Fatalf("expected to exclude the rejected model, got %q", bad)
		}
		return fix.Choice{Tool: fix.ToolClaude, Model: ""}, nil
	}

	st := sampleStore()
	m := newTestModel(t, ViewQueue, st)
	openBlocked(t, m)

	out, cmd := m.Update(key("f"))
	out, _ = out.(*Model).Update(cmd())
	m2 := out.(*Model)
	if m2.blockFlow.stage != blockStagePicker {
		t.Fatalf("expected the picker stage, got %d", m2.blockFlow.stage)
	}

	// Confirm the picker, which launchFixSession would normally suspend the
	// terminal for; simulate the session coming back with an invalid-model
	// failure instead of actually suspending.
	out, _ = m2.Update(key("enter"))
	m3 := out.(*Model)
	if m3.modal != ModalNone {
		t.Fatal("confirming the picker should suspend (clear the modal) for the session")
	}

	out, _ = m3.Update(sessionFinishedMsg{kind: "fix", err: errors.New("model rejected")})
	m4 := out.(*Model)

	if m4.modal != ModalBlockFlow || m4.blockFlow == nil || m4.blockFlow.stage != blockStagePicker {
		t.Fatalf("an invalid model should return to the picker, got modal=%d blockFlow=%+v", m4.modal, m4.blockFlow)
	}
	if m4.blockFlow.picker.err == "" {
		t.Error("expected the tool's rejection to be shown on the picker")
	}
	for _, task := range st.tasks {
		if task.ID == "t-2" && task.State != "blocked" {
			t.Errorf("an invalid-model failure must not create a new block, got state %q", task.State)
		}
	}
}

func TestBlockFlowResumeKeepsReason(t *testing.T) {
	swapFix(t)
	var gotReq fix.Request
	fixRetry = func(ctx context.Context, req fix.Request) error {
		gotReq = req
		return nil
	}

	m := newTestModel(t, ViewQueue, sampleStore())
	openBlocked(t, m)

	// Reach the resume stage the way a finished fix/shell session would.
	m.blockFlow.stage = blockStageResume

	out, _ := m.Update(key("enter"))
	m2 := out.(*Model)

	if m2.modal != ModalNone || m2.blockFlow != nil {
		t.Fatal("resuming should close the block flow")
	}
	if gotReq.TaskID != "t-2" {
		t.Fatalf("expected a retry for t-2, got %q", gotReq.TaskID)
	}
	if gotReq.Reason != "cannot find config/schema.yaml" {
		t.Errorf("resume should keep the block reason, got %q", gotReq.Reason)
	}
}

func TestBlockFlowResumeNoLeavesTaskBlocked(t *testing.T) {
	swapFix(t)
	called := false
	fixRetry = func(ctx context.Context, req fix.Request) error {
		called = true
		return nil
	}

	m := newTestModel(t, ViewQueue, sampleStore())
	openBlocked(t, m)
	m.blockFlow.stage = blockStageResume

	out, _ := m.Update(key("n"))
	m2 := out.(*Model)

	if m2.modal != ModalNone || m2.blockFlow != nil {
		t.Fatal("declining resume should still close the prompt")
	}
	if called {
		t.Error("declining resume must not retry the task")
	}
}

func TestBlockFlowTellRetriesWithHint(t *testing.T) {
	swapFix(t)
	var gotReq fix.Request
	fixRetry = func(ctx context.Context, req fix.Request) error {
		gotReq = req
		return nil
	}

	m := newTestModel(t, ViewQueue, sampleStore())
	openBlocked(t, m)

	out, _ := m.Update(key("t"))
	m2 := out.(*Model)
	for _, r := range "wrong path" {
		out, _ = m2.Update(key(string(r)))
		m2 = out.(*Model)
	}
	out, _ = m2.Update(key("enter"))
	m3 := out.(*Model)

	if m3.modal != ModalNone || m3.blockFlow != nil {
		t.Fatal("submitting a hint should close the block flow")
	}
	if gotReq.Hint != "wrong path" {
		t.Errorf("expected the typed hint to reach fix.Retry, got %q", gotReq.Hint)
	}
	if gotReq.Reason != "cannot find config/schema.yaml" {
		t.Errorf("tell should keep the block reason too, got %q", gotReq.Reason)
	}
}

func TestRetryTaskKeepsFeedback(t *testing.T) {
	st := sampleStore()
	m := newTestModel(t, ViewQueue, st)
	focusTask(t, m, "t-1", "needs-fix")
	for i := range st.tasks {
		if st.tasks[i].ID == "t-1" {
			st.tasks[i].LastFeedback = "FAIL: TestLogin"
		}
	}
	for i := range m.tasks {
		if m.tasks[i].ID == "t-1" {
			m.tasks[i].LastFeedback = "FAIL: TestLogin"
		}
	}

	m.Update(key("r"))

	for _, task := range st.tasks {
		if task.ID == "t-1" {
			if task.State != "open" {
				t.Errorf("state = %q, want open", task.State)
			}
			if task.LastFeedback != "FAIL: TestLogin" {
				t.Errorf("retryTask dropped the feedback, got %q", task.LastFeedback)
			}
		}
	}
}

func TestBlockFlowModalRendersStages(t *testing.T) {
	swapFix(t)
	m := newTestModel(t, ViewQueue, sampleStore())
	openBlocked(t, m)

	view := m.renderBlockFlowModal(120, 40)
	for _, want := range []string{"Fix with agent", "Tell it what to do", "Open shell", "Skip"} {
		if !strings.Contains(view, want) {
			t.Errorf("action menu should mention %q, got:\n%s", want, view)
		}
	}
}
