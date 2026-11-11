package fix

import (
	"context"
	"errors"
	"testing"
)

type stubResolver struct {
	tools  []Tool
	choice Choice
}

func (s stubResolver) InstalledTools(ctx context.Context) ([]Tool, error) { return s.tools, nil }
func (s stubResolver) ResolveChoice(ctx context.Context, req Request) (Choice, error) {
	return s.choice, nil
}

type stubSession struct{ called *Request }

func (s stubSession) OpenSession(ctx context.Context, req Request) error {
	*s.called = req
	return nil
}

type stubExplainer struct{ explanation Explanation }

func (s stubExplainer) Explain(ctx context.Context, req Request) (Explanation, error) {
	return s.explanation, nil
}

type stubRetrier struct{ called *Request }

func (s stubRetrier) Retry(ctx context.Context, req Request) error {
	*s.called = req
	return nil
}

type stubTriager struct{ diagnosis Diagnosis }

func (s stubTriager) Diagnose(ctx context.Context, req Request) (Diagnosis, error) {
	return s.diagnosis, nil
}

// resetRegistry clears every registered implementation so tests do not
// bleed state into each other.
func resetRegistry() {
	mu.Lock()
	defer mu.Unlock()
	resolver = nil
	session = nil
	explainer = nil
	retrier = nil
	triager = nil
}

func TestHelpersErrorBeforeConfigured(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)

	ctx := context.Background()
	req := Request{TaskID: "1"}

	if _, err := InstalledTools(ctx); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("InstalledTools: got %v, want ErrNotConfigured", err)
	}
	if _, err := ResolveChoice(ctx, req); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("ResolveChoice: got %v, want ErrNotConfigured", err)
	}
	if err := OpenSession(ctx, req); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("OpenSession: got %v, want ErrNotConfigured", err)
	}
	if _, err := Explain(ctx, req); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Explain: got %v, want ErrNotConfigured", err)
	}
	if err := Retry(ctx, req); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Retry: got %v, want ErrNotConfigured", err)
	}
	if _, err := Diagnose(ctx, req); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Diagnose: got %v, want ErrNotConfigured", err)
	}
}

func TestHelpersDelegateAfterConfigured(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)

	ctx := context.Background()
	req := Request{TaskID: "42", Flow: "fix"}

	wantTools := []Tool{ToolClaude, ToolCodex}
	wantChoice := Choice{Tool: ToolClaude, Model: "default"}
	SetResolver(stubResolver{tools: wantTools, choice: wantChoice})

	var sessionReq Request
	SetSession(stubSession{called: &sessionReq})

	wantExplanation := Explanation{Cause: "missing file", NextStep: "check the spec path"}
	SetExplainer(stubExplainer{explanation: wantExplanation})

	var retryReq Request
	SetRetrier(stubRetrier{called: &retryReq})

	wantDiagnosis := Diagnosis{Explanation: wantExplanation, Fixed: true, Change: "created missing file"}
	SetTriager(stubTriager{diagnosis: wantDiagnosis})

	tools, err := InstalledTools(ctx)
	if err != nil || len(tools) != 2 || tools[0] != ToolClaude {
		t.Errorf("InstalledTools: got %v, %v", tools, err)
	}

	choice, err := ResolveChoice(ctx, req)
	if err != nil || choice != wantChoice {
		t.Errorf("ResolveChoice: got %v, %v", choice, err)
	}

	if err := OpenSession(ctx, req); err != nil || sessionReq != req {
		t.Errorf("OpenSession: got req %v, err %v", sessionReq, err)
	}

	explanation, err := Explain(ctx, req)
	if err != nil || explanation != wantExplanation {
		t.Errorf("Explain: got %v, %v", explanation, err)
	}

	if err := Retry(ctx, req); err != nil || retryReq != req {
		t.Errorf("Retry: got req %v, err %v", retryReq, err)
	}

	diagnosis, err := Diagnose(ctx, req)
	if err != nil || diagnosis != wantDiagnosis {
		t.Errorf("Diagnose: got %v, %v", diagnosis, err)
	}
}
