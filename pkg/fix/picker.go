// Picker resolves which installed tool and model a fix flow uses (see
// "Choosing a Tool and Model" in SOURCE.md). Colony keeps no model list of
// its own: a model always comes from a one-off choice, a saved Colony
// setting, or whatever the tool itself already has configured.
package fix

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/futureboard-dev/colony/pkg/config"
	"github.com/futureboard-dev/colony/pkg/module"
)

func init() {
	SetResolver(picker{})
}

// picker is the default fix.Resolver: it detects installed tool CLIs and
// resolves a Request's Choice using the tool/model precedence rules.
type picker struct{}

// onPath reports whether a CLI binary is on PATH. Overridable in tests.
var onPath = func(cli string) bool {
	_, err := exec.LookPath(cli)
	return err == nil
}

// loadConfig loads the project's .colony/config.json. Overridable in tests
// so model-precedence table tests don't need a real project on disk.
var loadConfig = func() (*config.Config, error) {
	root, err := module.FindRoot()
	if err != nil {
		return nil, err
	}
	return config.Load(root)
}

// allTools is the fixed, ordered set of tools Colony can drive. Not exported:
// callers get the installed subset through InstalledTools.
var allTools = []Tool{ToolClaude, ToolCodex, ToolCrush}

// InstalledTools reports which of claude, codex and crush are on PATH, in
// that fixed order.
func (picker) InstalledTools(ctx context.Context) ([]Tool, error) {
	var installed []Tool
	for _, t := range allTools {
		if onPath(string(t)) {
			installed = append(installed, t)
		}
	}
	return installed, nil
}

// ResolveChoice resolves a Request's Choice to a concrete, usable tool and
// model. It never errors unless no tool at all is installed — an invalid or
// unavailable model must surface as the tool's own error and send the flow
// back to the picker, not as a new blocked task, so this function always
// returns a Choice the caller can retry with (see ResolveChoiceExcluding).
func (p picker) ResolveChoice(ctx context.Context, req Request) (Choice, error) {
	return p.resolve(ctx, req, "")
}

// ResolveChoiceExcluding behaves like ResolveChoice, but treats bad as
// unusable even if it would otherwise win on precedence. Call this after the
// tool has rejected bad as an invalid or unavailable model, so the picker
// falls through to the next tier (a different Colony setting, or the tool's
// own default) instead of repeating the same failure.
func ResolveChoiceExcluding(ctx context.Context, req Request, bad string) (Choice, error) {
	return picker{}.resolve(ctx, req, bad)
}

func (p picker) resolve(ctx context.Context, req Request, bad string) (Choice, error) {
	installed, err := p.InstalledTools(ctx)
	if err != nil {
		return Choice{}, err
	}
	if len(installed) == 0 {
		return Choice{}, fmt.Errorf("no agent CLI found — install one of:\n" +
			"  claude  https://claude.ai/code\n" +
			"  codex   npm install -g @openai/codex\n" +
			"  crush   brew install charmbracelet/tap/crush")
	}

	cfg, _ := loadConfig() // a missing/unreadable config just means no Colony setting

	tool := resolveTool(req, installed, cfg)
	model := resolveModel(req, tool, cfg, bad)
	return Choice{Tool: tool, Model: model}, nil
}

// resolveTool picks the tool to use. A preselected or one-off req.Choice.Tool
// wins if it's actually installed (the stuck tool, or the user's own
// switch). With exactly one tool installed, that's the only option — used
// without asking. With several and no preselection, the Colony-configured
// tool is preselected if installed, otherwise the first installed tool.
func resolveTool(req Request, installed []Tool, cfg *config.Config) Tool {
	if req.Choice.Tool != "" && containsTool(installed, req.Choice.Tool) {
		return req.Choice.Tool
	}
	if len(installed) == 1 {
		return installed[0]
	}
	if cfg != nil {
		if def := toolFromLLM(cfg.LLM); containsTool(installed, def) {
			return def
		}
	}
	return installed[0]
}

// resolveModel applies the model precedence: one-off change, then the Colony
// setting for this flow or tool, then the tool's own default (an empty
// string — the caller must not pass a --model flag at all, so the CLI uses
// whatever it already has configured). bad, when non-empty, is a model the
// tool just rejected and must be skipped at every tier.
func resolveModel(req Request, tool Tool, cfg *config.Config, bad string) string {
	if req.Choice.Model != "" && req.Choice.Model != bad {
		return req.Choice.Model
	}
	if cfg != nil {
		if m := cfg.Fix.Flow[req.Flow]; m != "" && m != bad {
			return m
		}
		if m := cfg.Fix.Models[string(tool)]; m != "" && m != bad {
			return m
		}
		if tool == toolFromLLM(cfg.LLM) && cfg.LLM.Model != "" && cfg.LLM.Model != bad {
			return cfg.LLM.Model
		}
	}
	return ""
}

// toolFromLLM derives the tool an LLMConfig points at, mirroring the
// precedence documented on LLMConfig.CLI: an explicit CLI wins, otherwise
// anthropic derives claude and everything else derives crush.
func toolFromLLM(llm config.LLMConfig) Tool {
	switch llm.CLI {
	case string(ToolClaude):
		return ToolClaude
	case string(ToolCodex):
		return ToolCodex
	case string(ToolCrush):
		return ToolCrush
	}
	if llm.Provider == "anthropic" {
		return ToolClaude
	}
	return ToolCrush
}

func containsTool(tools []Tool, t Tool) bool {
	return slices.Contains(tools, t)
}

// IsInvalidModel classifies a tool CLI's own error output as an invalid or
// unavailable model, as opposed to some other failure. Callers use this to
// decide whether a failure should show the tool's error and return to the
// picker (via ResolveChoiceExcluding) rather than creating a new blocked
// task.
func IsInvalidModel(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "model") {
		return false
	}
	for _, needle := range []string{"invalid", "unknown", "not found", "unavailable", "unsupported", "does not exist"} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}
