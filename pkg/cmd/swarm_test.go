package cmd

import (
	"strings"
	"testing"
)

func TestParseSubtasksBasic(t *testing.T) {
	input := `## SUBTASK 1
**Title:** Add banner component
Create a React component for the banner.

## SUBTASK 2
**Title:** Wire up to sales page
Update routing to include the new banner.
`
	got := parseSubtasks(input)
	if len(got) != 2 {
		t.Fatalf("expected 2 subtasks, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "Add banner component") {
		t.Errorf("subtask 1 missing title: %q", got[0])
	}
	if !strings.Contains(got[1], "Wire up to sales page") {
		t.Errorf("subtask 2 missing title: %q", got[1])
	}
}

func TestParseSubtasksSingle(t *testing.T) {
	input := `## SUBTASK 1
**Title:** Fix login bug
Fix the null pointer in the auth handler.
`
	got := parseSubtasks(input)
	if len(got) != 1 {
		t.Fatalf("expected 1 subtask, got %d", len(got))
	}
}

func TestParseSubtasksEmptyOutput(t *testing.T) {
	got := parseSubtasks("No subtask markers here at all.")
	if len(got) != 0 {
		t.Errorf("expected 0 subtasks for output without markers, got %d", len(got))
	}
}

func TestParseSubtasksPreambleIgnored(t *testing.T) {
	input := `Here is my decomposition plan:

## SUBTASK 1
**Title:** Only task
Do the thing.
`
	got := parseSubtasks(input)
	if len(got) != 1 {
		t.Fatalf("expected 1 subtask (preamble should be ignored), got %d", len(got))
	}
}

func TestParseDecisionApproved(t *testing.T) {
	output := `The implementation looks correct and covers all requirements.

DECISION: APPROVED
Reason: All spec requirements met with good test coverage.`
	if got := parseDecision(output); got != "APPROVED" {
		t.Errorf("expected APPROVED, got %q", got)
	}
}

func TestParseDecisionRejected(t *testing.T) {
	output := `Several issues found in the implementation.

DECISION: REJECTED
Missing error handling in the auth middleware.`
	if got := parseDecision(output); got != "REJECTED" {
		t.Errorf("expected REJECTED, got %q", got)
	}
}

func TestParseDecisionUnknown(t *testing.T) {
	output := "Great work overall but no explicit decision made."
	if got := parseDecision(output); got != "UNKNOWN" {
		t.Errorf("expected UNKNOWN, got %q", got)
	}
}

func TestSubtaskID(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/path/to/subtask-1.md", "1"},
		{"/path/to/subtask-3.md", "3"},
		{"/path/to/subtask-2-scouted.md", "2"},
	}
	for _, tc := range cases {
		got := subtaskID(tc.path)
		if got != tc.want {
			t.Errorf("subtaskID(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

