package mission

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/jirateep/colony/pkg/config"
	"github.com/jirateep/colony/pkg/llm"
	"github.com/jirateep/colony/pkg/prompt"
)

// BuilderNode is a Node that uses the build.md prompt to implement a spec.
// It wraps LLM execution with the builder role's model config.
type BuilderNode struct {
	agentID string
	cfg     config.LLMConfig
}

// NewBuilderNode creates a BuilderNode.
func NewBuilderNode(agentID string, cfg config.LLMConfig) *BuilderNode {
	return &BuilderNode{agentID: agentID, cfg: cfg}
}

func (n *BuilderNode) Run(ctx context.Context, in Input) (Output, error) {
	// Key validation is handled by the executor (RunAgent), which skips it for
	// anthropic — the claude CLI manages its own auth.

	// Determine language from params or default.
	lang := "go"
	if l, ok := in.Params["lang"].(string); ok && l != "" {
		lang = l
	}

	promptText, err := prompt.Build(lang)
	if err != nil {
		return Output{}, fmt.Errorf("agent %q: build prompt: %w", n.agentID, err)
	}

	// Inject client config and input.
	combined := promptText
	if len(in.Params) > 0 {
		b, _ := json.MarshalIndent(in.Params, "", "  ")
		combined = injectClientConfig(combined, string(b))
	}
	combined = injectInput(combined, in.Text)

	return runLLMAndParse(ctx, n.agentID, n.cfg, workdirFrom(in), combined)
}

// FixerNode is a Node that uses the fix.md prompt to fix gate failures.
type FixerNode struct {
	agentID string
	cfg     config.LLMConfig
}

// NewFixerNode creates a FixerNode.
func NewFixerNode(agentID string, cfg config.LLMConfig) *FixerNode {
	return &FixerNode{agentID: agentID, cfg: cfg}
}

func (n *FixerNode) Run(ctx context.Context, in Input) (Output, error) {
	// Key validation is handled by the executor (RunAgent), which skips it for
	// anthropic — the claude CLI manages its own auth.

	// Extract gate name and error details from the input.
	// The input contains the upstream's output which has the gate failure info.
	gateName := "gate"
	if l, ok := in.Params["lang"].(string); ok && l != "" {
		gateName = l
	}

	// Build the fix prompt by extracting error text from the incoming text.
	// The incoming text is the REJECTED output from the gate node.
	errText := in.Text
	promptText, err := prompt.Fix(gateName, errText)
	if err != nil {
		return Output{}, fmt.Errorf("agent %q: fix prompt: %w", n.agentID, err)
	}

	combined := promptText
	if len(in.Params) > 0 {
		b, _ := json.MarshalIndent(in.Params, "", "  ")
		combined = injectClientConfig(combined, string(b))
	}

	return runLLMAndParse(ctx, n.agentID, n.cfg, workdirFrom(in), combined)
}

// workdirFrom returns the workdir param, defaulting to the current directory.
func workdirFrom(in Input) string {
	if wd, ok := in.Params["workdir"].(string); ok && wd != "" {
		return wd
	}
	return "."
}

// runLLMAndParse is a shared helper that calls the LLM and parses the envelope.
func runLLMAndParse(ctx context.Context, agentID string, cfg config.LLMConfig, workdir, promptText string) (Output, error) {
	exec := llm.New(cfg)
	var buf bytes.Buffer
	stream := io.MultiWriter(&buf, prefixedWriter(os.Stderr, "    "+agentID+" │ "))
	fmt.Fprintf(os.Stderr, "    %s │ <streaming…>\n", agentID)
	if err := exec.RunAgent(ctx, workdir, promptText, stream); err != nil {
		raw := buf.String()
		var env Envelope
		if jsonErr := json.Unmarshal([]byte(extractJSON(raw)), &env); jsonErr == nil && env.Decision != "" {
			return Output{AgentID: agentID, Envelope: env, Raw: raw}, nil
		}
		return Output{AgentID: agentID, Raw: raw}, fmt.Errorf("agent %q: llm call failed: %w\n--- agent output ---\n%s---", agentID, err, raw)
	}

	raw := buf.String()
	var env Envelope
	if err := json.Unmarshal([]byte(extractJSON(raw)), &env); err != nil {
		preview := raw
		if len(preview) > 500 {
			preview = preview[:500] + "...(truncated)"
		}
		return Output{AgentID: agentID, Raw: raw},
			fmt.Errorf("agent %q: invalid JSON envelope: %w\n--- raw output ---\n%s---", agentID, err, preview)
	}

	return Output{AgentID: agentID, Envelope: env, Raw: raw}, nil
}

// BuilderNodeFactory returns a NodeFactory for builder roles.
func BuilderNodeFactory(agentID string, cfg config.LLMConfig) (Node, error) {
	return NewBuilderNode(agentID, cfg), nil
}

// FixerNodeFactory returns a NodeFactory for fixer roles.
func FixerNodeFactory(agentID string, cfg config.LLMConfig) (Node, error) {
	return NewFixerNode(agentID, cfg), nil
}
