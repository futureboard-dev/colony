package mission

import (
	"context"
	"os"
	"testing"

	"github.com/jirateep/colony/pkg/config"
)

// TestGateNode_Approved: mocked capture returning 0 → APPROVED.
func TestGateNode_Approved(t *testing.T) {
	// Use a temp dir with a valid Go module so gates pass.
	// Format is skipped because gofmt -w ./... does not support the ./... glob.
	dir := t.TempDir()
	writeGoModule(t, dir, `package test

func F() {}
`)

	node := NewGateNode("test-gate", "go", map[string]bool{"format": true})
	out, err := node.Run(context.Background(), Input{
		Params: map[string]any{"workdir": dir},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Envelope.Decision != APPROVED {
		t.Errorf("expected APPROVED, got %s", out.Envelope.Decision)
	}
}

// TestGateNode_Rejected: capture returning 1 → REJECTED + output.
func TestGateNode_Rejected(t *testing.T) {
	dir := t.TempDir()
	node := NewGateNode("test-gate", "go", nil)

	out, err := node.Run(context.Background(), Input{
		Params: map[string]any{"workdir": dir},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Envelope.Decision != REJECTED {
		t.Errorf("expected REJECTED, got %s", out.Envelope.Decision)
	}
	if out.Envelope.Feedback == "" {
		t.Error("expected non-empty feedback on rejection")
	}
}

// TestGateNode_RegisterRole: node has RoleGate via registry.
func TestGateNode_RegisterRole(t *testing.T) {
	// DefaultRegistry is populated by init() in llm_node.go.
	node, err := DefaultRegistry.Create(RoleGate, "test-agent", config.LLMConfig{})
	if err != nil {
		t.Fatalf("Create(RoleGate): %v", err)
	}
	if _, ok := node.(*GateNode); !ok {
		t.Errorf("expected *GateNode, got %T", node)
	}
}

func TestGateNode_SkipFormat(t *testing.T) {
	dir := t.TempDir()
	writeGoModule(t, dir, `package test

func F() {}
`)

	skip := map[string]bool{"format": true}
	node := NewGateNode("test-gate", "go", skip)

	out, err := node.Run(context.Background(), Input{
		Params: map[string]any{"workdir": dir},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Envelope.Decision != APPROVED {
		t.Errorf("expected APPROVED, got %s", out.Envelope.Decision)
	}
}

// writeGoModule creates a minimal go.mod and .go file in dir.
func writeGoModule(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(dir+"/go.mod", []byte("module test\n\ngo 1.25\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/test.go", []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
