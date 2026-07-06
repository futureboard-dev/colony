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
