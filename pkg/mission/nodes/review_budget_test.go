package nodes

import (
	"context"
	"github.com/futureboard-dev/colony/pkg/config"
	"github.com/futureboard-dev/colony/pkg/handoff"
	"github.com/futureboard-dev/colony/pkg/mission/graph"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func reviewFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init"}, {"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte("required specification"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}
func TestReviewIncludesUntrackedExcludesDuplicateSpec(t *testing.T) {
	dir := reviewFixture(t)
	os.WriteFile(filepath.Join(dir, "new.go"), []byte("required implementation"), 0600)
	before := exec.Command("git", "ls-files", "--stage")
	before.Dir = dir
	b, _ := before.Output()
	diff, err := reviewDiff(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(diff, "required specification") || !strings.Contains(diff, "required implementation") {
		t.Fatal(diff)
	}
	after := exec.Command("git", "ls-files", "--stage")
	after.Dir = dir
	a, _ := after.Output()
	if string(a) != string(b) {
		t.Fatal("review altered index")
	}
}
func TestReviewLargeGeneratedArtifactFailsClosed(t *testing.T) {
	dir := reviewFixture(t)
	os.WriteFile(filepath.Join(dir, "generated-client.ts"), []byte(strings.Repeat("generated\n", handoff.ReviewLimit/8)), 0600)
	// No executable CLI is needed: coverage must fail before any invocation.
	out, err := NewReviewNode("review", config.LLMConfig{Provider: "anthropic"}).Run(context.Background(), graph.Input{Params: map[string]any{"workdir": dir}})
	if err == nil || !strings.Contains(err.Error(), "coverage blocked") || out.Envelope.Decision == graph.APPROVED {
		t.Fatal(out, err)
	}
	logs, _ := filepath.Glob(filepath.Join(dir, ".colony/logs/review-input-*.log"))
	if len(logs) != 1 {
		t.Fatal(logs)
	}
	data, _ := os.ReadFile(logs[0])
	if !strings.Contains(string(data), "generated-client.ts") || len(data) < handoff.ReviewLimit {
		t.Fatal("evidence lost")
	}
}
