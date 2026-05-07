package mission

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
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

	// Inject client config (if provided) then input text.
	combined := promptText
	if len(in.Params) > 0 {
		b, _ := json.MarshalIndent(in.Params, "", "  ")
		combined = injectClientConfig(combined, string(b))
	}
	combined = injectInput(combined, in.Text)

	// Run LLM headless. Tee CLI output to stderr (with an agent prefix) so the
	// user sees live progress instead of staring at a frozen terminal while the
	// model generates. The buffer still receives the full output for parsing.
	exec := llm.New(n.cfg)
	var buf bytes.Buffer
	stream := io.MultiWriter(&buf, prefixedWriter(os.Stderr, "    "+n.agentID+" │ "))
	fmt.Fprintf(os.Stderr, "    %s │ <streaming…>\n", n.agentID)
	if err := exec.RunHeadless(ctx, ".", combined, stream); err != nil {
		raw := buf.String()
		// Recover if the buffer contains a valid envelope despite the non-zero exit
		// (e.g. claude CLI prints a warning to stderr and exits 1 after full output).
		var env Envelope
		if jsonErr := json.Unmarshal([]byte(extractJSON(raw)), &env); jsonErr == nil && env.Decision != "" {
			return Output{AgentID: n.agentID, Envelope: env, Raw: raw}, nil
		}
		return Output{AgentID: n.agentID, Raw: raw}, fmt.Errorf("agent %q: llm call failed: %w\n--- agent output ---\n%s\n---", n.agentID, err, raw)
	}

	raw := buf.String()

	// Parse JSON envelope.
	var env Envelope
	if err := json.Unmarshal([]byte(extractJSON(raw)), &env); err != nil {
		preview := raw
		if len(preview) > 500 {
			preview = preview[:500] + "...(truncated)"
		}
		return Output{AgentID: n.agentID, Raw: raw},
			fmt.Errorf("agent %q: invalid JSON envelope: %w\n--- raw output ---\n%s\n---", n.agentID, err, preview)
	}

	return Output{AgentID: n.agentID, Envelope: env, Raw: raw}, nil
}

// prefixedWriter returns an io.Writer that prepends prefix to each line written
// to w. Used to tag streamed LLM output with the agent ID so concurrent agents
// don't produce indistinguishable streams.
func prefixedWriter(w io.Writer, prefix string) io.Writer {
	return &linePrefixer{w: w, prefix: []byte(prefix), atLineStart: true}
}

type linePrefixer struct {
	w           io.Writer
	prefix      []byte
	atLineStart bool
}

func (p *linePrefixer) Write(b []byte) (int, error) {
	for _, c := range b {
		if p.atLineStart {
			if _, err := p.w.Write(p.prefix); err != nil {
				return 0, err
			}
			p.atLineStart = false
		}
		if _, err := p.w.Write([]byte{c}); err != nil {
			return 0, err
		}
		if c == '\n' {
			p.atLineStart = true
		}
	}
	return len(b), nil
}

// extractJSON finds the JSON envelope object in s by locating the "decision"
// key and tracing back to its enclosing {, then matching braces to find the
// closing }. This handles reasoning preamble, markdown fences, and thinking
// tokens that LLMs (especially chain-of-thought models) emit before the JSON.
func extractJSON(s string) string {
	decisionIdx := strings.Index(s, `"decision"`)
	if decisionIdx >= 0 {
		for i := decisionIdx - 1; i >= 0; i-- {
			if s[i] == '{' {
				if end := matchingBrace(s, i); end > i {
					return s[i : end+1]
				}
			}
		}
	}
	// Fallback: first { to last }
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return strings.TrimSpace(s)
}

// matchingBrace returns the index of the } that closes the { at pos,
// correctly handling nested braces and quoted strings.
func matchingBrace(s string, pos int) int {
	depth := 0
	inStr := false
	escaped := false
	for i := pos; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && inStr {
			escaped = true
			continue
		}
		if c == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// injectClientConfig inserts a ## Client Config section (JSON) just before the # INPUT marker.
func injectClientConfig(promptText, configJSON string) string {
	const marker = "\n# INPUT"
	section := "\n\n## Client Config\n\n```json\n" + configJSON + "\n```"
	idx := strings.Index(promptText, marker)
	if idx < 0 {
		return promptText + section
	}
	return promptText[:idx] + section + promptText[idx:]
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
