package llm

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestStreamClaude(t *testing.T) {
	input := strings.Join([]string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Let me start."}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Write","input":{"file_path":"src/foo.ts"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"pnpm   test\nwith newline"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"x"}]}}`,
		`{"type":"result","is_error":true}`,
		`not json here`,
	}, "\n")

	var buf bytes.Buffer
	if err := streamClaude(strings.NewReader(input), &buf); err != nil {
		t.Fatalf("streamClaude: %v", err)
	}

	want := strings.Join([]string{
		"Let me start.",
		"  → Write src/foo.ts",
		"  → Bash pnpm test with newline",
		"  ⚠ agent run ended with an error",
		"not json here",
		"",
	}, "\n")

	if got := buf.String(); got != want {
		t.Errorf("streamClaude output mismatch:\ngot:\n%q\nwant:\n%q", got, want)
	}
}

func TestStreamClaudeEmptyAndWhitespace(t *testing.T) {
	var buf bytes.Buffer
	if err := streamClaude(strings.NewReader("\n   \n"), &buf); err != nil {
		t.Fatalf("streamClaude: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output for blank lines, got %q", buf.String())
	}
}

func TestFormatTool(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"Write", `{"file_path":"a/b.go"}`, "→ Write a/b.go"},
		{"Edit", `{"file_path":"a/b.go"}`, "✎ Edit a/b.go"},
		{"MultiEdit", `{"file_path":"a/b.go"}`, "✎ Edit a/b.go"},
		{"NotebookEdit", `{"notebook_path":"nb.ipynb"}`, "✎ Edit nb.ipynb"},
		{"Bash", `{"command":"go  build ./..."}`, "→ Bash go build ./..."},
		{"Read", `{"file_path":"a/b.go"}`, "· Read a/b.go"},
		{"Glob", `{"pattern":"**/*.go"}`, "· Glob **/*.go"},
		{"Grep", `{"pattern":"TODO"}`, "· Grep TODO"},
		{"UnknownTool", `{}`, "→ UnknownTool"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatTool(c.name, json.RawMessage(c.input))
			if got != c.want {
				t.Errorf("formatTool(%s) = %q, want %q", c.name, got, c.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 80); got != "short" {
		t.Errorf("truncate kept-short = %q", got)
	}
	if got := truncate("abcdef", 3); got != "abc…" {
		t.Errorf("truncate long = %q, want %q", got, "abc…")
	}
}
