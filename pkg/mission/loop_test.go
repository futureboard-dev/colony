package mission

import (
	"testing"

	"github.com/jirateep/colony/pkg/config"
)

// TestBuildGateFix_HasBuilderGateFixer verifies the mission has builder, gate,
// and fixer nodes with expected roles and edges.
func TestBuildGateFix_HasBuilderGateFixer(t *testing.T) {
	m := BuildGateFix(BuildGateFixOpts{
		Name:      "test-loop",
		Input:     "implement feature X",
		Lang:      "go",
		MaxCycles: 3,
	})

	if m.Name != "test-loop" {
		t.Errorf("expected name 'test-loop', got %q", m.Name)
	}
	if m.Input != "implement feature X" {
		t.Errorf("expected input 'implement feature X', got %q", m.Input)
	}

	// Check agents.
	agentByID := make(map[string]*Agent)
	for i := range m.Agents {
		agentByID[m.Agents[i].ID] = &m.Agents[i]
	}

	if a, ok := agentByID["builder"]; !ok {
		t.Error("missing builder agent")
	} else if a.Role != RoleBuilder {
		t.Errorf("builder role: expected %q, got %q", RoleBuilder, a.Role)
	}

	if a, ok := agentByID["gate"]; !ok {
		t.Error("missing gate agent")
	} else if a.Role != RoleGate {
		t.Errorf("gate role: expected %q, got %q", RoleGate, a.Role)
	}

	if a, ok := agentByID["fixer"]; !ok {
		t.Error("missing fixer agent")
	} else if a.Role != RoleFixer {
		t.Errorf("fixer role: expected %q, got %q", RoleFixer, a.Role)
	}

	// Check edges.
	edgeMap := make(map[string]string)
	for _, e := range m.Flow {
		key := e.From + "->" + e.To
		edgeMap[key] = key
		if e.OnApprove != "" {
			edgeMap[e.From+"->"+e.OnApprove+"(approve)"] = key
		}
		if e.OnReject != "" {
			edgeMap[e.From+"->"+e.OnReject+"(reject)"] = key
		}
	}

	if _, ok := edgeMap["__input__->builder"]; !ok {
		t.Error("missing edge: __input__ -> builder")
	}
	if _, ok := edgeMap["builder->gate"]; !ok {
		t.Error("missing edge: builder -> gate")
	}
	if _, ok := edgeMap["gate->__output__(approve)"]; !ok {
		t.Error("missing edge: gate -> __output__ on approve")
	}
	if _, ok := edgeMap["gate->fixer(reject)"]; !ok {
		t.Error("missing edge: gate -> fixer on reject")
	}
	if _, ok := edgeMap["fixer->gate"]; !ok {
		t.Error("missing back-edge: fixer -> gate")
	}
}

// TestBuildGateFix_MaxCyclesSets verifies MaxCycles is set on the mission.
func TestBuildGateFix_MaxCyclesSets(t *testing.T) {
	m := BuildGateFix(BuildGateFixOpts{
		Name:      "cycles",
		Input:     "test",
		Lang:      "go",
		MaxCycles: 7,
	})
	if m.MaxCycles != 7 {
		t.Errorf("expected MaxCycles 7, got %d", m.MaxCycles)
	}

	// Default when not specified.
	m2 := BuildGateFix(BuildGateFixOpts{
		Name:  "default-cycles",
		Input: "test",
	})
	if m2.MaxCycles != 3 {
		t.Errorf("expected default MaxCycles 3, got %d", m2.MaxCycles)
	}
}

// TestBuildGateFix_EscalationNode verifies that when EscalationRole is set,
// the mission includes an escalation node.
func TestBuildGateFix_EscalationNode(t *testing.T) {
	m := BuildGateFix(BuildGateFixOpts{
		Name:           "with-escalation",
		Input:          "test",
		Lang:           "go",
		MaxCycles:      3,
		EscalationRole: "escalation",
	})

	found := false
	for _, a := range m.Agents {
		if a.ID == "escalation" && a.Role == "escalation" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected escalation agent with role 'escalation'")
	}

	// Check escalation edges: gate reject → escalation, escalation → output.
	hasRejectEdge := false
	hasOutputEdge := false
	for _, e := range m.Flow {
		if e.From == "gate" && e.OnReject == "escalation" {
			hasRejectEdge = true
		}
		if e.From == "escalation" && e.To == "__output__" {
			hasOutputEdge = true
		}
	}
	if !hasRejectEdge {
		t.Error("missing edge: gate -> escalation on reject")
	}
	if !hasOutputEdge {
		t.Error("missing edge: escalation -> __output__")
	}
}

// TestBuildGateFix_ReviewNode verifies that when ReviewRole is set, the green
// gate routes to a review node, which then routes to output (approve) or back
// to the fixer (reject) — and that the result is a valid graph.
func TestBuildGateFix_ReviewNode(t *testing.T) {
	m := BuildGateFix(BuildGateFixOpts{
		Name:       "with-review",
		Input:      "test",
		Lang:       "go",
		MaxCycles:  3,
		ReviewRole: RoleReview,
	})

	found := false
	for _, a := range m.Agents {
		if a.ID == "review" && a.Role == RoleReview {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected review agent with role %q", RoleReview)
	}

	var gateToReview, gateToOutput, reviewToOutput, reviewToFixer bool
	for _, e := range m.Flow {
		switch {
		case e.From == "gate" && e.OnApprove == "review":
			gateToReview = true
		case e.From == "gate" && e.OnApprove == "__output__":
			gateToOutput = true
		case e.From == "review" && e.OnApprove == "__output__":
			reviewToOutput = true
		case e.From == "review" && e.OnReject == "fixer":
			reviewToFixer = true
		}
	}
	if !gateToReview {
		t.Error("missing edge: gate -> review on approve")
	}
	if gateToOutput {
		t.Error("gate should no longer route directly to __output__ when review is enabled")
	}
	if !reviewToOutput {
		t.Error("missing edge: review -> __output__ on approve")
	}
	if !reviewToFixer {
		t.Error("missing edge: review -> fixer on reject")
	}

	// The wired mission must build into a valid graph. The review→fixer→gate
	// cycle is bounded by the existing fixer→gate back-edge (max_cycles).
	g, err := BuildGraph(m, DefaultRegistry, func(string) config.LLMConfig {
		return config.LLMConfig{Provider: "anthropic", Model: "claude-opus-4-8"}
	})
	if err != nil {
		t.Fatalf("BuildGraph with review node failed: %v", err)
	}
	if !g.IsBackEdge("fixer", "gate") {
		t.Error("expected fixer -> gate to remain a back-edge bounding the review cycle")
	}
}

// TestBuildGateFix_GateOverrides verifies --no-format flow (skip set in params).
func TestBuildGateFix_GateOverrides(t *testing.T) {
	m := BuildGateFix(BuildGateFixOpts{
		Name:      "no-format",
		Input:     "test",
		Lang:      "go",
		SkipGates: map[string]bool{"format": true},
	})

	if m.Params == nil {
		t.Fatal("expected non-nil Params")
	}
	skip, ok := m.Params["skip_gates"].(map[string]bool)
	if !ok {
		t.Fatal("expected skip_gates in Params")
	}
	if !skip["format"] {
		t.Error("expected format to be in skip_gates")
	}
}
