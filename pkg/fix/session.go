package fix

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/futureboard-dev/colony/pkg/config"
	"github.com/futureboard-dev/colony/pkg/llm"
	"github.com/futureboard-dev/colony/pkg/module"
)

func init() {
	SetSession(&agentSession{})
}

// agentSession opens a fresh agent CLI session for a blocked task, already
// loaded with the error, logs, task and worktree, so the user never has to
// leave Colony or paste the problem in by hand.
type agentSession struct{}

// launchInteractive does the actual process launch. It is a package-level
// var so tests can replace it and observe what OpenSession would have run
// without shelling out to a real agent CLI.
var launchInteractive = func(ex *llm.Executor, workdir, prompt string) error {
	return ex.RunInteractive(workdir, prompt)
}

func (s *agentSession) OpenSession(ctx context.Context, req Request) error {
	worktree := resolveWorktree(req.Worktree)
	prompt := buildFixPrompt(req, worktree)
	cfg := llmConfigForChoice(req.Choice)

	fmt.Printf("Fix with: %s · %s\n\n", req.Choice.Tool, modelLabel(req.Choice.Model))

	return launchInteractive(llm.New(cfg), worktree, prompt)
}

// resolveWorktree falls back to the project root when the task's worktree
// doesn't exist yet (e.g. a block before SetupWorktree ran), and to the
// current directory if even that can't be found.
func resolveWorktree(worktree string) string {
	if worktree != "" {
		if info, err := os.Stat(worktree); err == nil && info.IsDir() {
			return worktree
		}
	}
	if root, err := module.FindRoot(); err == nil {
		return root
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return worktree
}

// llmConfigForChoice turns a picker Choice into the LLMConfig RunInteractive
// expects. CLI is set explicitly from Choice.Tool so the executor never has
// to infer it from a provider the picker never resolved.
func llmConfigForChoice(choice Choice) config.LLMConfig {
	cfg := config.LLMConfig{CLI: string(choice.Tool), Model: choice.Model}
	if choice.Tool == ToolClaude {
		// anthropic skips the API-key check: claude manages its own auth.
		cfg.Provider = "anthropic"
	}
	return cfg
}

func modelLabel(model string) string {
	if model == "" {
		return "default"
	}
	return model
}

// buildFixPrompt carries everything the user would otherwise have to paste
// in by hand: why the task blocked, its logs, its description, and where to
// work.
func buildFixPrompt(req Request, worktree string) string {
	var b strings.Builder

	b.WriteString("A Colony task is blocked")
	if req.TaskID != "" {
		fmt.Fprintf(&b, " (task %s)", req.TaskID)
	}
	b.WriteString(". Find and fix the problem, then stop.\n\n")

	if req.Description != "" {
		fmt.Fprintf(&b, "## Task\n%s\n\n", req.Description)
	}
	if req.Reason != "" {
		fmt.Fprintf(&b, "## Why it blocked\n%s\n\n", req.Reason)
	}
	if req.Hint != "" {
		fmt.Fprintf(&b, "## Hint\n%s\n\n", req.Hint)
	}
	if req.Logs != "" {
		fmt.Fprintf(&b, "## Logs\n%s\n\n", req.Logs)
	}
	fmt.Fprintf(&b, "## Worktree\n%s\n", worktree)

	return b.String()
}
