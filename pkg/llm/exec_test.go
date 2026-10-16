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

func TestPreflightSkipsCodex(t *testing.T) {
	// Empty PATH: if Preflight tried to run crush it would fail on lookup.
	t.Setenv("PATH", t.TempDir())
	if err := New(config.LLMConfig{Provider: "openai", CLI: "codex"}).Preflight(); err != nil {
		t.Errorf("preflight is crush-only, expected nil for codex: %v", err)
	}
}
