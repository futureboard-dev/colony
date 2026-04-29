package mission

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jirateep/colony/pkg/config"
	"github.com/jirateep/colony/pkg/llm"
	"github.com/jirateep/colony/pkg/prompt"
)

// LLMNode is a Node backed by an LLM via the colony executor.
// It loads the module prompt for the role, injects the input under # INPUT,
// calls ValidateKey() before dialing the LLM, and parses the JSON envelope.
type LLMNode struct {
	agentID string
	role    string
	cfg     config.LLMConfig
}

// NewLLMNode creates an LLMNode for the given agent.
func NewLLMNode(agentID, role string, cfg config.LLMConfig) *LLMNode {
	return &LLMNode{agentID: agentID, role: role, cfg: cfg}
}

func (n *LLMNode) Run(ctx context.Context, in Input) (Output, error) {
	// Validate API key before any LLM call.
	if err := n.cfg.ValidateKey(); err != nil {
		return Output{}, err
	}

	// Load module prompt for this role.
	promptText, err := prompt.LoadModulePrompt(n.role)
	if err != nil {
		return Output{}, fmt.Errorf("agent %q: load prompt for role %q: %w", n.agentID, n.role, err)
	}

	// Inject input text under the # INPUT section.
	combined := injectInput(promptText, in.Text)

	// Run LLM headless.
	exec := llm.New(n.cfg)
	var buf bytes.Buffer
	if err := exec.RunHeadless(ctx, ".", combined, &buf); err != nil {
		return Output{AgentID: n.agentID, Raw: buf.String()}, fmt.Errorf("agent %q: llm call failed: %w", n.agentID, err)
	}

	raw := buf.String()

	// Parse JSON envelope.
	var env Envelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return Output{AgentID: n.agentID, Raw: raw},
			fmt.Errorf("agent %q: invalid JSON envelope: %w", n.agentID, err)
	}

	return Output{AgentID: n.agentID, Envelope: env, Raw: raw}, nil
}

// injectInput replaces the content after "# INPUT" in the prompt with the actual input.
func injectInput(promptText, inputText string) string {
	const marker = "\n# INPUT"
	idx := strings.Index(promptText, marker)
	if idx < 0 {
		return promptText + "\n\n# INPUT\n\n" + inputText
	}
	return promptText[:idx] + marker + "\n\n" + inputText
}

// LLMNodeFactory returns a NodeFactory that creates LLMNode instances.
func LLMNodeFactory(role string) NodeFactory {
	return func(agentID string, cfg config.LLMConfig) (Node, error) {
		return NewLLMNode(agentID, role, cfg), nil
	}
}

func init() {
	// Register built-in roles backed by module prompts.
	for _, role := range []string{"business-analyst", "architect", "project-manager", "estimator"} {
		r := role // capture
		Register(r, func(agentID string, cfg config.LLMConfig) (Node, error) {
			return NewLLMNode(agentID, r, cfg), nil
		})
	}
}
