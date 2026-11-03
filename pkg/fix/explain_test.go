package fix

import (
	"context"
	"testing"
)

func TestRuleExplainer_Explain(t *testing.T) {
	e := ruleExplainer{}
	ctx := context.Background()

	cases := []struct {
		name         string
		reason       string
		wantCause    string
		wantNextStep string
	}{
		{
			name:         "missing file message",
			reason:       `open config/schema.yaml: no such file or directory`,
			wantCause:    "A file or path the task needed could not be found. This usually means the spec points at a wrong or outdated path.",
			wantNextStep: "Check the path referenced in the error against the spec, then fix the path or create the missing file.",
		},
		{
			name:         "cannot find phrasing",
			reason:       "cannot find config/schema.yaml",
			wantCause:    "A file or path the task needed could not be found. This usually means the spec points at a wrong or outdated path.",
			wantNextStep: "Check the path referenced in the error against the spec, then fix the path or create the missing file.",
		},
		{
			name:         "failing gate: tests",
			reason:       "tests failed: 3 failures in pkg/foo",
			wantCause:    "A quality gate (lint, type check, build, or tests) failed.",
			wantNextStep: "Open the gate's output to find the failure, fix it, and retry.",
		},
		{
			name:         "failing gate: lint",
			reason:       "lint failed with 2 errors",
			wantCause:    "A quality gate (lint, type check, build, or tests) failed.",
			wantNextStep: "Open the gate's output to find the failure, fix it, and retry.",
		},
		{
			name:         "review rejection",
			reason:       "review rejected: missing error handling",
			wantCause:    "A reviewer rejected the change or asked for changes.",
			wantNextStep: "Read the review notes, make the requested changes, and retry.",
		},
		{
			name:         "changes requested",
			reason:       "changes requested on PR #42",
			wantCause:    "A reviewer rejected the change or asked for changes.",
			wantNextStep: "Read the review notes, make the requested changes, and retry.",
		},
		{
			name:         "max cycles",
			reason:       "max cycles reached after 5 attempts",
			wantCause:    "The task used up its allowed fix cycles without resolving.",
			wantNextStep: "Review what was already tried, add a hint or adjust the approach, then retry.",
		},
		{
			name:         "too many retries",
			reason:       "too many retries, giving up",
			wantCause:    "The task used up its allowed fix cycles without resolving.",
			wantNextStep: "Review what was already tried, add a hint or adjust the approach, then retry.",
		},
		{
			name:         "unknown reason passes through unchanged",
			reason:       "panic: runtime error: index out of range [3] with length 2",
			wantCause:    "panic: runtime error: index out of range [3] with length 2",
			wantNextStep: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := e.Explain(ctx, Request{Reason: tc.reason})
			if err != nil {
				t.Fatalf("Explain() error = %v", err)
			}
			if got.Cause != tc.wantCause {
				t.Errorf("Cause = %q, want %q", got.Cause, tc.wantCause)
			}
			if got.NextStep != tc.wantNextStep {
				t.Errorf("NextStep = %q, want %q", got.NextStep, tc.wantNextStep)
			}
		})
	}
}

func TestExplain_DelegatesToRegisteredExplainer(t *testing.T) {
	// Registers ruleExplainer itself so this test doesn't depend on test
	// ordering relative to registry_test.go's resetRegistry cleanup.
	SetExplainer(ruleExplainer{})
	t.Cleanup(func() { SetExplainer(nil) })

	got, err := Explain(context.Background(), Request{Reason: "cannot find foo.yaml"})
	if err != nil {
		t.Fatalf("Explain() error = %v", err)
	}
	if got.Cause == "" || got.NextStep == "" {
		t.Errorf("Explain() = %+v, want non-empty cause and next step", got)
	}
}
