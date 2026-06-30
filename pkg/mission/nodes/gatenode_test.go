package nodes

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jirateep/colony/pkg/mission/graph"
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
	out, err := node.Run(context.Background(), graph.Input{
		Params: map[string]any{"workdir": dir},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Envelope.Decision != graph.APPROVED {
		t.Errorf("expected APPROVED, got %s", out.Envelope.Decision)
	}
}

// TestGateNode_Rejected: capture returning 1 → REJECTED + output.
func TestGateNode_Rejected(t *testing.T) {
	dir := t.TempDir()
	node := NewGateNode("test-gate", "go", nil)

	out, err := node.Run(context.Background(), graph.Input{
		Params: map[string]any{"workdir": dir},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Envelope.Decision != graph.REJECTED {
		t.Errorf("expected REJECTED, got %s", out.Envelope.Decision)
	}
	if out.Envelope.Feedback == "" {
		t.Error("expected non-empty feedback on rejection")
	}
}

func TestGateNode_SkipFormat(t *testing.T) {
	dir := t.TempDir()
	writeGoModule(t, dir, `package test

func F() {}
`)

	skip := map[string]bool{"format": true}
	node := NewGateNode("test-gate", "go", skip)

	out, err := node.Run(context.Background(), graph.Input{
		Params: map[string]any{"workdir": dir},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Envelope.Decision != graph.APPROVED {
		t.Errorf("expected APPROVED, got %s", out.Envelope.Decision)
	}
}

// TestGateNode_LangParamOverridesFactory verifies the lang mission param takes
// precedence over the factory-time lang. A node built with "go" but run with a
// "typescript" param must gate as TypeScript, not run go commands in a dir with
// no go.mod. Regression test for the loop running Go gates on TS tasks.
func TestGateNode_LangParamOverridesFactory(t *testing.T) {
	dir := t.TempDir()
	// Factory default is "go", but the mission param says typescript.
	node := NewGateNode("test-gate", "go", nil)

	out, err := node.Run(context.Background(), graph.Input{
		Params: map[string]any{"workdir": dir, "lang": "typescript"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// With no package.json/tsc, the TS gate fails — but the feedback must name
	// the TypeScript gate, proving the param (not the "go" factory) was used.
	if out.Envelope.Decision != graph.REJECTED {
		t.Errorf("expected REJECTED, got %s", out.Envelope.Decision)
	}
	if !strings.Contains(out.Envelope.Feedback, `Gate "typescript"`) {
		t.Errorf("expected feedback to reference the typescript gate, got: %s", out.Envelope.Feedback)
	}
	if strings.Contains(out.Envelope.Feedback, "main module") {
		t.Errorf("feedback contains a Go toolchain error — gate ran go instead of typescript: %s", out.Envelope.Feedback)
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
