package fix

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/futureboard-dev/colony/pkg/llm"
)

// initTestRepo creates a temp directory with a real git repo and returns its
// path, so project-root fallback can be tested for real instead of mocked.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "--initial-branch=main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, string(out))
	}
	return dir
}

func TestBuildFixPromptCarriesContext(t *testing.T) {
	req := Request{
		TaskID:      "12",
		Description: "add the frobnicator",
		Reason:      "cannot find config/schema.yaml",
		Logs:        "panic: open config/schema.yaml: no such file",
	}
	prompt := buildFixPrompt(req, "/work/task-12")

	for _, want := range []string{
		"task 12",
		req.Description,
		req.Reason,
		req.Logs,
		"/work/task-12",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildFixPromptCarriesHint(t *testing.T) {
	req := Request{Hint: "it's in configs/, not config/"}
	prompt := buildFixPrompt(req, "/work")
	if !strings.Contains(prompt, req.Hint) {
		t.Errorf("prompt missing hint:\n%s", prompt)
	}
}

func TestBuildFixPromptOmitsEmptySections(t *testing.T) {
	prompt := buildFixPrompt(Request{}, "/work")
	for _, unwanted := range []string{"## Task", "## Why it blocked", "## Hint", "## Logs"} {
		if strings.Contains(prompt, unwanted) {
			t.Errorf("prompt should omit empty section %q:\n%s", unwanted, prompt)
		}
	}
	if !strings.Contains(prompt, "## Worktree\n/work") {
		t.Errorf("prompt missing worktree section:\n%s", prompt)
	}
}

func TestResolveWorktreeKeepsExisting(t *testing.T) {
	dir := t.TempDir()
	if got := resolveWorktree(dir); got != dir {
		t.Errorf("resolveWorktree(%q) = %q, want unchanged", dir, got)
	}
}

func TestResolveWorktreeFallsBackToProjectRoot(t *testing.T) {
	root := initTestRepo(t)
	wantRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origWd)

	missing := filepath.Join(root, "nonexistent-worktree")
	got, err := filepath.EvalSymlinks(resolveWorktree(missing))
	if err != nil {
		t.Fatal(err)
	}
	if got != wantRoot {
		t.Errorf("resolveWorktree(%q) = %q, want project root %q", missing, got, wantRoot)
	}
}

func TestLLMConfigForChoice(t *testing.T) {
	cases := []struct {
		tool         Tool
		wantCLI      string
		wantProvider string
	}{
		{ToolClaude, "claude", "anthropic"},
		{ToolCodex, "codex", ""},
		{ToolCrush, "crush", ""},
	}
	for _, tc := range cases {
		cfg := llmConfigForChoice(Choice{Tool: tc.tool, Model: "some-model"})
		if cfg.CLI != tc.wantCLI {
			t.Errorf("%s: CLI = %q, want %q", tc.tool, cfg.CLI, tc.wantCLI)
		}
		if cfg.Provider != tc.wantProvider {
			t.Errorf("%s: Provider = %q, want %q", tc.tool, cfg.Provider, tc.wantProvider)
		}
		if cfg.Model != "some-model" {
			t.Errorf("%s: Model = %q, want %q", tc.tool, cfg.Model, "some-model")
		}
	}
}

func TestModelLabel(t *testing.T) {
	if got := modelLabel(""); got != "default" {
		t.Errorf(`modelLabel("") = %q, want "default"`, got)
	}
	if got := modelLabel("gpt-5"); got != "gpt-5" {
		t.Errorf("modelLabel(%q) = %q, want unchanged", "gpt-5", got)
	}
}

// TestOpenSessionRegisteredFromInit proves fix.Session is wired up by this
// package's init() (OpenSession doesn't return ErrNotConfigured), and that
// the request's reason/logs/description/worktree and the chosen model all
// reach the launch step.
func TestOpenSessionRegisteredFromInit(t *testing.T) {
	// init() registers &agentSession{} once at package load; sibling test
	// files in this package reset the shared registry between their own
	// tests, so re-assert it here rather than depend on run order.
	SetSession(&agentSession{})

	var gotWorkdir, gotPrompt, gotCLI, gotModel string
	orig := launchInteractive
	launchInteractive = func(ex *llm.Executor, workdir, prompt string) error {
		gotWorkdir = workdir
		gotPrompt = prompt
		gotCLI = ex.CLI()
		gotModel = ex.Model()
		return nil
	}
	defer func() { launchInteractive = orig }()

	req := Request{
		TaskID:      "7",
		Description: "fix the thing",
		Reason:      "it broke",
		Logs:        "stack trace here",
		Worktree:    t.TempDir(),
		Choice:      Choice{Tool: ToolClaude, Model: "claude-opus-4-8"},
	}

	if err := OpenSession(context.Background(), req); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if gotWorkdir != req.Worktree {
		t.Errorf("worktree = %q, want %q", gotWorkdir, req.Worktree)
	}
	for _, want := range []string{req.Reason, req.Logs, req.Description} {
		if !strings.Contains(gotPrompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, gotPrompt)
		}
	}
	if gotCLI != "claude" {
		t.Errorf("CLI = %q, want claude", gotCLI)
	}
	if gotModel != "claude-opus-4-8" {
		t.Errorf("model = %q, want claude-opus-4-8", gotModel)
	}
}

func TestOpenSessionCodexAndCrushReachLauncher(t *testing.T) {
	SetSession(&agentSession{})

	orig := launchInteractive
	defer func() { launchInteractive = orig }()

	for _, tc := range []struct {
		tool  Tool
		model string
	}{
		{ToolCodex, "gpt-5"},
		{ToolCrush, "deepseek/deepseek-chat"},
	} {
		var gotCLI, gotModel string
		launchInteractive = func(ex *llm.Executor, workdir, prompt string) error {
			gotCLI = ex.CLI()
			gotModel = ex.Model()
			return nil
		}
		req := Request{
			Worktree: t.TempDir(),
			Choice:   Choice{Tool: tc.tool, Model: tc.model},
		}
		if err := OpenSession(context.Background(), req); err != nil {
			t.Fatalf("OpenSession(%s): %v", tc.tool, err)
		}
		if gotCLI != string(tc.tool) {
			t.Errorf("%s: CLI = %q, want %q", tc.tool, gotCLI, tc.tool)
		}
		if gotModel != tc.model {
			t.Errorf("%s: model = %q, want %q", tc.tool, gotModel, tc.model)
		}
	}
}
