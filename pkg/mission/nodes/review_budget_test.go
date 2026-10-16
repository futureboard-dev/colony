package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/futureboard-dev/colony/pkg/config"
	"github.com/futureboard-dev/colony/pkg/handoff"
	"github.com/futureboard-dev/colony/pkg/llm"
	"github.com/futureboard-dev/colony/pkg/mission/graph"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	return string(out)
}

func reviewFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init")
	git(t, dir, "commit", "--allow-empty", "-m", "init")
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte("required specification"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

// fakeReviewer records every prompt and answers batches with the exact unit
// labels it was given unless decide overrides the verdict.
type fakeReviewer struct {
	prompts []string
	decide  func(batch int, labels []string) batchVerdict
}

func (f *fakeReviewer) run(_ context.Context, _, prompt string) (llm.Result, error) {
	f.prompts = append(f.prompts, prompt)
	_, rest, isBatch := strings.Cut(prompt, "Units in this batch:\n")
	if !isBatch {
		return llm.Result{Text: `{"decision":"APPROVED","feedback":"whole change fulfils spec","output":""}`, LogPath: "/logs/review.log"}, nil
	}
	list, _, _ := strings.Cut(rest, "\n\n")
	var labels []string
	for _, line := range strings.Split(list, "\n") {
		labels = append(labels, strings.TrimPrefix(line, "- "))
	}
	v := batchVerdict{Decision: graph.APPROVED, Covered: labels}
	if f.decide != nil {
		v = f.decide(len(f.prompts), labels)
	}
	data, _ := json.Marshal(v)
	return llm.Result{Text: string(data), LogPath: "/logs/batch.log"}, nil
}

func newFakeReview(f *fakeReviewer) *ReviewNode {
	n := NewReviewNode("review", config.LLMConfig{Provider: "anthropic"})
	n.run = f.run
	return n
}

func runReview(t *testing.T, n *ReviewNode, dir string, params map[string]any) (graph.Output, error) {
	t.Helper()
	if params == nil {
		params = map[string]any{}
	}
	params["workdir"] = dir
	return n.Run(context.Background(), graph.Input{Params: params})
}

// largeChange writes files whose combined diff exceeds one review prompt,
// including one file that alone needs splitting into parts.
func largeChange(t *testing.T, dir string) {
	t.Helper()
	write(t, dir, "generated-client.ts", strings.Repeat("export const generated = 1;\n", handoff.ReviewLimit/20))
	for i := range 3 {
		write(t, dir, fmt.Sprintf("handwritten-%d.ts", i), fmt.Sprintf("marker-%d\n", i)+strings.Repeat("x\n", 40000))
	}
}

func TestReviewIncludesUntrackedExcludesDuplicateSpec(t *testing.T) {
	dir := reviewFixture(t)
	write(t, dir, "new.go", "required implementation")
	before := git(t, dir, "ls-files", "--stage")
	diff, err := reviewDiff(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(diff, "required specification") || !strings.Contains(diff, "required implementation") {
		t.Fatal(diff)
	}
	if git(t, dir, "ls-files", "--stage") != before {
		t.Fatal("review altered index")
	}
}

func TestReviewSmallDiffIsOneCall(t *testing.T) {
	dir := reviewFixture(t)
	write(t, dir, "new.go", "required implementation")
	f := &fakeReviewer{}
	out, err := runReview(t, newFakeReview(f), dir, nil)
	if err != nil || out.Envelope.Decision != graph.APPROVED || len(f.prompts) != 1 {
		t.Fatal(out, err, len(f.prompts))
	}
}

func TestReviewLargeDiffBatchesWithCompleteCoverage(t *testing.T) {
	dir := reviewFixture(t)
	largeChange(t, dir)
	diff, err := reviewDiff(dir)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeReviewer{}
	out, err := runReview(t, newFakeReview(f), dir, nil)
	if err != nil || out.Envelope.Decision != graph.APPROVED {
		t.Fatal(out, err)
	}
	if len(f.prompts) < 3 {
		t.Fatalf("want ≥2 batches plus synthesis, got %d calls", len(f.prompts))
	}
	var reviewed strings.Builder
	for i, p := range f.prompts {
		if len(p) > handoff.ReviewLimit {
			t.Fatalf("call %d is %d bytes, over budget", i, len(p))
		}
		if !strings.Contains(p, "required specification") {
			t.Fatalf("call %d lost the spec", i)
		}
		if _, content, ok := strings.Cut(p, "Units in this batch:\n"); ok {
			_, body, _ := strings.Cut(content, "\n\n")
			reviewed.WriteString(body)
		}
	}
	if reviewed.String() != diff {
		t.Fatal("batches did not cover the diff exactly once")
	}
	if !strings.Contains(f.prompts[0], "generated-client.ts (part 1/") {
		t.Fatal("oversized file was not split into labelled parts")
	}
}

func TestReviewBatchCoverageMismatchFailsClosed(t *testing.T) {
	dir := reviewFixture(t)
	largeChange(t, dir)
	f := &fakeReviewer{decide: func(_ int, labels []string) batchVerdict {
		return batchVerdict{Decision: graph.APPROVED, Covered: labels[1:]}
	}}
	out, err := runReview(t, newFakeReview(f), dir, nil)
	if err == nil || !strings.Contains(err.Error(), "coverage mismatch") || out.Envelope.Decision == graph.APPROVED {
		t.Fatal(out, err)
	}
}

func TestReviewBatchRejectionRejectsWithoutSynthesis(t *testing.T) {
	dir := reviewFixture(t)
	largeChange(t, dir)
	f := &fakeReviewer{decide: func(batch int, labels []string) batchVerdict {
		if batch == 2 {
			return batchVerdict{Decision: graph.REJECTED, Covered: labels, Feedback: "stub at handwritten-1.ts:1 " + strings.Repeat("detail ", 2000)}
		}
		return batchVerdict{Decision: graph.APPROVED, Covered: labels}
	}}
	out, err := runReview(t, newFakeReview(f), dir, nil)
	if err != nil || out.Envelope.Decision != graph.REJECTED {
		t.Fatal(out, err)
	}
	fb := out.Envelope.Feedback
	if !strings.Contains(fb, "stub at handwritten-1.ts:1") || len(fb) > handoff.FeedbackLimit || !strings.Contains(fb, "Full log:") {
		t.Fatalf("feedback not bounded/actionable (%d bytes): %s", len(fb), fb)
	}
	for _, p := range f.prompts {
		if !strings.Contains(p, "Units in this batch:") {
			t.Fatal("synthesis ran after a rejected batch")
		}
	}
}

func TestReviewOversizedSpecFailsClosed(t *testing.T) {
	dir := reviewFixture(t)
	write(t, dir, "SPEC.md", strings.Repeat("s", handoff.ReviewLimit))
	write(t, dir, "new.go", "required implementation")
	f := &fakeReviewer{}
	out, err := runReview(t, newFakeReview(f), dir, nil)
	if err == nil || !strings.Contains(err.Error(), "coverage blocked") || out.Envelope.Decision == graph.APPROVED || len(f.prompts) != 0 {
		t.Fatal(out, err)
	}
	logs, _ := filepath.Glob(filepath.Join(dir, ".colony/logs/review-input-*.log"))
	if len(logs) != 1 {
		t.Fatal("review input not archived", logs)
	}
}

func TestReviewBinaryFailsClosed(t *testing.T) {
	dir := reviewFixture(t)
	write(t, dir, "image.bin", "\x00\x01\x02binary")
	f := &fakeReviewer{}
	out, err := runReview(t, newFakeReview(f), dir, nil)
	if err == nil || !strings.Contains(err.Error(), "binary") || out.Envelope.Decision == graph.APPROVED || len(f.prompts) != 0 {
		t.Fatal(out, err)
	}
}

func TestReviewListsSeparatelyCommittedFiles(t *testing.T) {
	dir := reviewFixture(t)
	git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	write(t, dir, "client.gen.ts", "generated")
	git(t, dir, "add", "client.gen.ts")
	git(t, dir, "commit", "-m", "regenerate client")
	write(t, dir, "new.go", "required implementation")
	f := &fakeReviewer{}
	if _, err := runReview(t, newFakeReview(f), dir, map[string]any{"base": "main"}); err != nil {
		t.Fatal(err)
	}
	p := f.prompts[0]
	if !strings.Contains(p, "Already committed on this branch") || !strings.Contains(p, "client.gen.ts") || strings.Contains(p, "+generated") {
		t.Fatal(p)
	}
}

func TestSplitUnits(t *testing.T) {
	t.Run("reassembles exactly and respects capacity", func(t *testing.T) {
		diff := "diff --git a/a b/a\n+1\n" + "diff --git a/big b/big\n" + strings.Repeat("+line\n", 100) + "diff --git a/c b/c\n+3\n"
		units := splitUnits(diff, 64)
		var joined strings.Builder
		for _, u := range units {
			if len(u.text) > 64 {
				t.Fatalf("unit %q is %d bytes", u.label, len(u.text))
			}
			joined.WriteString(u.text)
		}
		if joined.String() != diff || units[0].label != "a" || units[len(units)-1].label != "c" || !strings.HasPrefix(units[1].label, "big (part 1/") {
			t.Fatal(units)
		}
	})
	t.Run("splits a single oversized line", func(t *testing.T) {
		diff := "diff --git a/min.js b/min.js\n+" + strings.Repeat("x", 300) + "\n"
		units := splitUnits(diff, 100)
		var joined strings.Builder
		for _, u := range units {
			joined.WriteString(u.text)
		}
		if joined.String() != diff || len(units) < 3 {
			t.Fatal(len(units))
		}
	})
}

func TestCoverageGap(t *testing.T) {
	missing, extra := coverageGap([]string{"a", "b"}, []string{"b", "c"})
	if len(missing) != 1 || missing[0] != "a" || len(extra) != 1 || extra[0] != "c" {
		t.Fatal(missing, extra)
	}
	if m, e := coverageGap([]string{"a"}, []string{"a"}); m != nil || e != nil {
		t.Fatal(m, e)
	}
}

func TestReviewVerifiesPriorFeedback(t *testing.T) {
	dir := reviewFixture(t)
	write(t, dir, "new.go", "required implementation")
	t.Run("prior checklist reaches the reviewer", func(t *testing.T) {
		f := &fakeReviewer{}
		if _, err := runReview(t, newFakeReview(f), dir, map[string]any{"prior_feedback": "1. ai-apply.service.ts: risks overwritten"}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(f.prompts[0], "## Previously reported issues") || !strings.Contains(f.prompts[0], "ai-apply.service.ts: risks overwritten") {
			t.Fatal(f.prompts[0])
		}
	})
	t.Run("no section without prior feedback", func(t *testing.T) {
		f := &fakeReviewer{}
		if _, err := runReview(t, newFakeReview(f), dir, map[string]any{"prior_feedback": "  "}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(f.prompts[0], "Previously reported issues") {
			t.Fatal(f.prompts[0])
		}
	})
}
