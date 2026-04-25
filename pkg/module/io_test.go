package module

import (
	"testing"
)

func TestSlugify(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"fix login bug", "fix-login-bug"},
		{"Add banner to xyz.site", "add-banner-to-xyz-site"},
		{"  spaces  around  ", "spaces-around"},
		{"UPPER CASE WORDS", "upper-case-words"},
		{"special!@#chars$%^", "special-chars"},
		{"double--dashes", "double-dashes"},
		// truncates at 40 chars
		{"this is a very long task description that exceeds the limit", "this-is-a-very-long-task-description-tha"},
		// caps at 8 words
		{"one two three four five six seven eight nine ten", "one-two-three-four-five-six-seven-eight"},
		{"", ""},
	}

	for _, tc := range cases {
		got := Slugify(tc.input)
		if got != tc.want {
			t.Errorf("Slugify(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestExtractTaskDescFromH1(t *testing.T) {
	content := "# Fix the login bug\n\nSome description here."
	got := ExtractTaskDesc(content, "my-spec.md")
	if got != "Fix the login bug" {
		t.Errorf("got %q", got)
	}
}

func TestExtractTaskDescStripsPlaceholder(t *testing.T) {
	content := "# Plan: Add banner component\n\nDetails."
	got := ExtractTaskDesc(content, "spec.md")
	if got != "Add banner component" {
		t.Errorf("got %q", got)
	}
}

func TestExtractTaskDescFromSubheading(t *testing.T) {
	content := "## 1. Implement search endpoint\n\nDetails."
	got := ExtractTaskDesc(content, "spec.md")
	if got != "Implement search endpoint" {
		t.Errorf("got %q", got)
	}
}

func TestExtractTaskDescFallsBackToFilename(t *testing.T) {
	content := "No heading here, just plain text."
	got := ExtractTaskDesc(content, "add-user-profile.md")
	if got != "add user profile" {
		t.Errorf("got %q", got)
	}
}

func TestExtractTaskDescEmptyContent(t *testing.T) {
	got := ExtractTaskDesc("", "my-task.md")
	if got != "my task" {
		t.Errorf("got %q", got)
	}
}
