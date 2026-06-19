package mission

import (
	"strings"
	"testing"

	"github.com/jirateep/colony/pkg/prompt"
)

// TestReviewLoopPrompt_HasRejectionInstructions verifies the review prompt
// contains instructions to reject redeclarations, duplicate schema, and stubs.
func TestReviewLoopPrompt_HasRejectionInstructions(t *testing.T) {
	p := prompt.ReviewLoop()

	if !strings.Contains(p, "Redeclarations") {
		t.Error("review prompt missing 'Redeclarations' section")
	}
	if !strings.Contains(p, "Duplicate schema objects") {
		t.Error("review prompt missing 'Duplicate schema objects' section")
	}
	if !strings.Contains(p, "Stubs and TODO placeholders") {
		t.Error("review prompt missing 'Stubs and TODO placeholders' section")
	}
	if !strings.Contains(p, "REJECT") {
		t.Error("review prompt missing REJECT instruction")
	}
	if !strings.Contains(p, "redeclares") {
		t.Error("review prompt missing 'redeclares' keyword")
	}
	if !strings.Contains(p, "TODO") {
		t.Error("review prompt missing 'TODO' reference")
	}
	if !strings.Contains(p, "REJECTED") {
		t.Error("review prompt missing REJECTED decision option")
	}
	if !strings.Contains(p, "APPROVED") {
		t.Error("review prompt missing APPROVED decision option")
	}
}
