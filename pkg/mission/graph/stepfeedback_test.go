package graph

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestStepFeedback(t *testing.T) {
	t.Run("gate REJECTED stores captured output", func(t *testing.T) {
		out := Output{
			AgentID: "gate",
			Envelope: Envelope{
				Decision: REJECTED,
				Output:   json.RawMessage(`"FAIL: TestRateLimit_Burst\nexpected 429, got 200\n"`),
			},
		}
		got := stepFeedback(RoleGate, out, nil)
		if got == "" {
			t.Error("expected gate REJECTED to capture output")
		}
	})

	t.Run("gate APPROVED stores empty", func(t *testing.T) {
		out := Output{Envelope: Envelope{Decision: APPROVED, Output: json.RawMessage(`"all gates passed"`)}}
		if got := stepFeedback(RoleGate, out, nil); got != "" {
			t.Errorf("expected APPROVED gate to store empty output, got %q", got)
		}
	})

	t.Run("gate with exec error does not capture", func(t *testing.T) {
		out := Output{Envelope: Envelope{Decision: REJECTED, Output: json.RawMessage(`"boom"`)}}
		if got := stepFeedback(RoleGate, out, errors.New("exec failed")); got != "" {
			t.Errorf("expected exec-error gate to store empty, got %q", got)
		}
	})

	t.Run("LLM step stores nothing by default", func(t *testing.T) {
		// Even a REJECTED decision on a non-gate role stores nothing.
		out := Output{Envelope: Envelope{Decision: REJECTED, Output: json.RawMessage(`"nope"`)}}
		for _, role := range []string{RoleBuilder, RoleFixer, RoleEscalation, RoleReview} {
			if got := stepFeedback(role, out, nil); got != "" {
				t.Errorf("role %s should not store output, got %q", role, got)
			}
		}
	})
}
