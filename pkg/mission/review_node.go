package mission

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/jirateep/colony/pkg/config"
	"github.com/jirateep/colony/pkg/llm"
	"github.com/jirateep/colony/pkg/prompt"
)

// ReviewNode is an LLM-backed semantic gate that runs after the deterministic
// gate passes, before a task is committed. It reads the task spec (SPEC.md) and
// the worktree's git diff, asks the model whether the implementation actually
// fulfils the spec (catching stubs/TODOs/unimplemented items the gate cannot),
// and returns the parsed APPROVED/REJECTED envelope.
type ReviewNode struct {
	agentID string
	cfg     config.LLMConfig
}

// NewReviewNode creates a ReviewNode.
func NewReviewNode(agentID string, cfg config.LLMConfig) *ReviewNode {
	return &ReviewNode{agentID: agentID, cfg: cfg}
}

func (n *ReviewNode) Run(ctx context.Context, in Input) (Output, error) {
	// Skip key validation for anthropic — the claude CLI manages its own auth
	// and does not need ANTHROPIC_API_KEY. If we required it, a valid-looking key
	// set in env would be forwarded to the subprocess and rejected by the CLI.
	if n.cfg.Provider != "anthropic" {
		if err := n.cfg.ValidateKey(); err != nil {
			return Output{}, err
		}
	}

	workdir := workdirFrom(in)

	spec := readSpec(workdir)
	diff := gitDiff(workdir)
	if diff == "" {
		// No diff means the agent produced no changes — reject so the task is
		// not approved on an empty implementation.
		return Output{
			AgentID: n.agentID,
			Envelope: Envelope{
				Decision: REJECTED,
				Feedback: "review: the implementation produced no changes (empty diff)",
				Output:   mustMarshal(""),
			},
		}, nil
	}

	combined := injectInput(prompt.ReviewLoop(),
		"## Specification\n\n"+spec+"\n\n## Git diff of implementation\n\n"+diff)

	runner := llm.New(n.cfg)
	var buf bytes.Buffer
	stream := io.MultiWriter(&buf, prefixedWriter(os.Stderr, "    "+n.agentID+" │ "))
	fmt.Fprintf(os.Stderr, "    %s │ <streaming…>\n", n.agentID)
	// Review runs headless (read-only judgement): it inspects the diff and emits
	// a decision envelope; it must not modify the worktree.
	if err := runner.RunHeadless(ctx, workdir, combined, stream); err != nil {
		raw := buf.String()
		var env Envelope
		if jsonErr := json.Unmarshal([]byte(extractJSON(raw)), &env); jsonErr == nil && env.Decision != "" {
			return Output{AgentID: n.agentID, Envelope: env, Raw: raw}, nil
		}
		return Output{AgentID: n.agentID, Raw: raw},
			fmt.Errorf("agent %q: review call failed: %w\n--- agent output ---\n%s\n---", n.agentID, err, raw)
	}

	raw := buf.String()
	var env Envelope
	if err := json.Unmarshal([]byte(extractJSON(raw)), &env); err != nil {
		preview := raw
		if len(preview) > 500 {
			preview = preview[:500] + "...(truncated)"
		}
		return Output{AgentID: n.agentID, Raw: raw},
			fmt.Errorf("agent %q: invalid review envelope: %w\n--- raw output ---\n%s\n---", n.agentID, err, preview)
	}

	return Output{AgentID: n.agentID, Envelope: env, Raw: raw}, nil
}

// readSpec returns the contents of SPEC.md in workdir, or "" if absent.
func readSpec(workdir string) string {
	data, err := os.ReadFile(filepath.Join(workdir, "SPEC.md"))
	if err != nil {
		return ""
	}
	return string(data)
}

// gitDiff returns the combined staged+unstaged diff of the worktree, including
// untracked files (via --no-index would be heavy, so we add intent-to-add).
// Returns "" when there are no changes.
func gitDiff(workdir string) string {
	// Stage intent so newly created files show up in the diff without committing.
	add := exec.Command("git", "add", "-AN")
	add.Dir = workdir
	_ = add.Run()

	cmd := exec.Command("git", "diff", "HEAD")
	cmd.Dir = workdir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// ReviewNodeFactory returns a NodeFactory for the review role.
func ReviewNodeFactory(agentID string, cfg config.LLMConfig) (Node, error) {
	return NewReviewNode(agentID, cfg), nil
}
