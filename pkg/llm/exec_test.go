package llm

import (
	"slices"
	"strings"
	"testing"

	"github.com/futureboard-dev/colony/pkg/config"
)

func argsContain(args []string, want string) bool {
	return slices.Contains(args, want)
}

func TestAgentArgsClaude(t *testing.T) {
	e := New(config.LLMConfig{Provider: "anthropic", Model: "claude-sonnet-4-6"})
	args := e.agentArgs("do the thing")

	for _, want := range []string{"-p", "do the thing", "stream-json", "--verbose", "--allowedTools"} {
		if !argsContain(args, want) {
			t.Errorf("claude agentArgs missing %q: %v", want, args)
		}
	}
	if !argsContain(args, "--model") || !argsContain(args, "claude-sonnet-4-6") {
		t.Errorf("claude agentArgs missing model override: %v", args)
	}
}

func TestAgentArgsCrush(t *testing.T) {
	e := New(config.LLMConfig{Provider: "openai", Model: "gpt-4o"})
	args := e.agentArgs("do the thing")

	if args[0] != "run" {
		t.Errorf("crush agentArgs should start with \"run\", got %v", args)
	}
	if !argsContain(args, "--verbose") {
		t.Errorf("crush agentArgs missing --verbose: %v", args)
	}
	if !argsContain(args, "-m") || !argsContain(args, "gpt-4o") {
		t.Errorf("crush agentArgs missing model override: %v", args)
	}
	if args[len(args)-1] != "do the thing" {
		t.Errorf("crush agentArgs should end with the prompt, got %v", args)
	}
	if argsContain(args, "stream-json") {
		t.Errorf("crush must not use claude stream-json flags: %v", args)
	}
}

func TestAgentArgsClaudeNoModel(t *testing.T) {
	e := New(config.LLMConfig{Provider: "anthropic"})
	args := e.agentArgs("x")
	if argsContain(args, "--model") {
		t.Errorf("claude agentArgs should omit --model when unset: %v", strings.Join(args, " "))
	}
}

func TestCLISelection(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.LLMConfig
		want string
	}{
		{"anthropic → claude", config.LLMConfig{Provider: "anthropic"}, "claude"},
		{"other provider → crush", config.LLMConfig{Provider: "openrouter"}, "crush"},
		{"cli pins codex", config.LLMConfig{Provider: "openai", CLI: "codex"}, "codex"},
		{"cli overrides provider", config.LLMConfig{Provider: "anthropic", CLI: "crush"}, "crush"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := New(tc.cfg).CLI(); got != tc.want {
				t.Errorf("CLI() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestArgsCodex(t *testing.T) {
	e := New(config.LLMConfig{Provider: "openai", CLI: "codex", Model: "gpt-5"})

	t.Run("headless is read-only", func(t *testing.T) {
		args := e.headlessArgs("write a spec")
		if args[0] != "exec" || !argsContain(args, "--sandbox") || !argsContain(args, "read-only") {
			t.Errorf("codex headlessArgs should be exec with read-only sandbox: %v", args)
		}
		if argsContain(args, "--full-auto") {
			t.Errorf("codex headless must not allow writes: %v", args)
		}
		if args[len(args)-1] != "write a spec" {
			t.Errorf("codex headlessArgs should end with the prompt: %v", args)
		}
	})
	t.Run("agent can write", func(t *testing.T) {
		args := e.agentArgs("do the thing")
		if args[0] != "exec" || !argsContain(args, "--sandbox") || !argsContain(args, "workspace-write") {
			t.Errorf("codex agentArgs should use workspace-write sandbox: %v", args)
		}
		if argsContain(args, "--full-auto") || argsContain(args, "--dangerously-bypass-approvals-and-sandbox") {
			t.Errorf("codex agentArgs must not use removed flags or bypass the sandbox: %v", args)
		}
		if !argsContain(args, "-m") || !argsContain(args, "gpt-5") {
			t.Errorf("codex agentArgs missing model override: %v", args)
		}
		if args[len(args)-1] != "do the thing" {
			t.Errorf("codex agentArgs should end with the prompt: %v", args)
		}
	})
	t.Run("no model flag when unset", func(t *testing.T) {
		args := New(config.LLMConfig{Provider: "openai", CLI: "codex"}).agentArgs("x")
		if argsContain(args, "-m") {
			t.Errorf("codex agentArgs should omit -m when unset: %v", args)
		}
	})
}

func TestInteractiveArgsClaude(t *testing.T) {
	e := New(config.LLMConfig{Provider: "anthropic", Model: "claude-sonnet-4-6"})
	args := e.interactiveArgs("/work/dir", "fix the thing")

	for _, want := range []string{"--add-dir", "/work/dir", "fix the thing"} {
		if !argsContain(args, want) {
			t.Errorf("claude interactiveArgs missing %q: %v", want, args)
		}
	}
	if !argsContain(args, "--model") || !argsContain(args, "claude-sonnet-4-6") {
		t.Errorf("claude interactiveArgs missing model override: %v", args)
	}
	if args[0] != "fix the thing" {
		t.Errorf("claude interactiveArgs must lead with the prompt, or variadic --add-dir swallows it: %v", args)
	}
}

func TestInteractiveArgsClaudeNoModel(t *testing.T) {
	e := New(config.LLMConfig{Provider: "anthropic"})
	args := e.interactiveArgs("/work/dir", "fix the thing")
	if argsContain(args, "--model") {
		t.Errorf("claude interactiveArgs should omit --model when unset: %v", args)
	}
}

func TestInteractiveArgsCodex(t *testing.T) {
	e := New(config.LLMConfig{Provider: "openai", CLI: "codex", Model: "gpt-5"})
	args := e.interactiveArgs("/work/dir", "fix the thing")

	if !argsContain(args, "fix the thing") {
		t.Errorf("codex interactiveArgs missing prompt: %v", args)
	}
	if !argsContain(args, "-m") || !argsContain(args, "gpt-5") {
		t.Errorf("codex interactiveArgs missing model override: %v", args)
	}
	if argsContain(args, "--add-dir") {
		t.Errorf("codex interactiveArgs must not use claude flags: %v", args)
	}
}

func TestInteractiveArgsCrush(t *testing.T) {
	e := New(config.LLMConfig{CLI: "crush", Model: "deepseek/deepseek-chat"})
	args := e.interactiveArgs("/work/dir", "fix the thing")

	// Bare crush accepts no starting prompt or model flag, so the session
	// must go through `run`, which accepts both.
	if args[0] != "run" {
		t.Errorf("crush interactiveArgs should start with \"run\": %v", args)
	}
	if !argsContain(args, "fix the thing") {
		t.Errorf("crush interactiveArgs missing prompt: %v", args)
	}
	if !argsContain(args, "-m") || !argsContain(args, "deepseek/deepseek-chat") {
		t.Errorf("crush interactiveArgs missing model override: %v", args)
	}
}

func TestInteractiveArgsCrushNoModel(t *testing.T) {
	e := New(config.LLMConfig{CLI: "crush"})
	args := e.interactiveArgs("/work/dir", "x")
	if argsContain(args, "-m") {
		t.Errorf("crush interactiveArgs should omit -m when unset: %v", args)
	}
}

func TestPreflightSkipsCodex(t *testing.T) {
	// Empty PATH: if Preflight tried to run crush it would fail on lookup.
	t.Setenv("PATH", t.TempDir())
	if err := New(config.LLMConfig{Provider: "openai", CLI: "codex"}).Preflight(); err != nil {
		t.Errorf("preflight is crush-only, expected nil for codex: %v", err)
	}
}
