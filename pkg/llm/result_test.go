package llm

import (
	"context"
	"github.com/futureboard-dev/colony/pkg/config"
	"github.com/futureboard-dev/colony/pkg/handoff"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFinalResponseSeparateFromTranscript(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
while [ "$#" -gt 0 ]; do
 if [ "$1" = "--output-last-message" ]; then shift; printf 'final answer' > "$1"; fi
 shift
done
printf 'tool transcript\n'
printf 'stderr details\n' >&2
`
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	result, err := New(config.LLMConfig{CLI: "codex", Provider: "anthropic"}).RunAgentResult(context.Background(), dir, "prompt", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "final answer" {
		t.Fatalf("response: %q", result.Text)
	}
	log, err := os.ReadFile(result.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tool transcript", "stderr details", "final answer"} {
		if !strings.Contains(string(log), want) {
			t.Fatalf("log missing %q", want)
		}
	}
}
func TestHeadlessOversizeCannotApprove(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s' '" + strings.Repeat("x", handoff.ResponseLimit+1) + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	result, err := New(config.LLMConfig{Provider: "anthropic"}).RunHeadlessResult(context.Background(), dir, "prompt")
	if err == nil || result.Text != "" {
		t.Fatal("oversized response accepted")
	}
	log, readErr := os.ReadFile(result.LogPath)
	if readErr != nil || len(log) < handoff.ResponseLimit {
		t.Fatal("full log lost", readErr)
	}
}
func TestClaudeFinalOnly(t *testing.T) {
	var final, progress strings.Builder
	err := streamClaudeResult(strings.NewReader("{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"intermediate\"}]}}\n{\"type\":\"result\",\"result\":\"done\"}\n"), &progress, &final)
	if err != nil || final.String() != "done" || !strings.Contains(progress.String(), "intermediate") {
		t.Fatal(final.String(), err)
	}
}
