package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/futureboard-dev/colony/pkg/handoff"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/futureboard-dev/colony/pkg/config"
	"github.com/futureboard-dev/colony/pkg/llm"
	"github.com/futureboard-dev/colony/pkg/mission/graph"
	"github.com/futureboard-dev/colony/pkg/prompt"
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

func (n *ReviewNode) Run(ctx context.Context, in graph.Input) (graph.Output, error) {
	if err := n.cfg.ValidateKey(); err != nil {
		return graph.Output{}, err
	}

	workdir := workdirFrom(in)

	specBytes, specErr := os.ReadFile(filepath.Join(workdir, "SPEC.md"))
	if specErr != nil || len(specBytes) == 0 {
		return graph.Output{}, fmt.Errorf("review coverage blocked: missing or unreadable SPEC.md: %v", specErr)
	}
	spec := string(specBytes)
	diff, diffErr := reviewDiff(workdir)
	if diffErr != nil {
		return graph.Output{}, fmt.Errorf("review coverage blocked: %w", diffErr)
	}
	evidencePath, err := handoff.Save(workdir, "review-input", "## Specification\n\n"+spec+"\n\n## Diff\n\n"+diff)
	if err != nil {
		return graph.Output{}, err
	}
	// Never approve a partial diff. Generated clients, schemas and lockfiles are
	// required evidence too: retain them and block until review is partitioned.
	if len(spec)+len(diff) > handoff.ReviewLimit || strings.Contains(diff, "GIT binary patch") {
		return graph.Output{}, fmt.Errorf("review coverage blocked: %d bytes (budget %d), or binary artifacts present; partition the review with explicit coverage for generated clients, schemas and other artifacts; no content was reviewed or approved; full input: %s", len(spec)+len(diff), handoff.ReviewLimit, evidencePath)
	}
	if diff == "" {
		// No diff means the agent produced no changes — reject so the task is
		// not approved on an empty implementation.
		return graph.Output{
			AgentID: n.agentID,
			Envelope: graph.Envelope{
				Decision: graph.REJECTED,
				Feedback: "review: the implementation produced no changes (empty diff)",
				Output:   mustMarshal(""),
			},
		}, nil
	}

	combined := injectInput(prompt.ReviewLoop(),
		"## Specification\n\n"+spec+"\n\n## Git diff of implementation\n\n"+diff)

	runner := llm.New(n.cfg)
	result, err := runner.RunHeadlessResult(ctx, workdir, combined)
	if err != nil {
		return graph.Output{AgentID: n.agentID}, fmt.Errorf("agent %q: review call failed: %w", n.agentID, err)
	}
	raw := result.Text

	var env graph.Envelope
	if err := json.Unmarshal([]byte(extractJSON(raw)), &env); err != nil {
		preview := raw
		if len(preview) > 500 {
			preview = preview[:500] + "...(truncated)"
		}
		return graph.Output{AgentID: n.agentID, Raw: raw},
			fmt.Errorf("agent %q: invalid review envelope: %w\n--- raw output ---\n%s\n---", n.agentID, err, preview)
	}

	if env.Decision != graph.APPROVED && env.Decision != graph.REJECTED {
		return graph.Output{}, fmt.Errorf("invalid review decision %q; full log: %s", env.Decision, result.LogPath)
	}
	env.Feedback = handoff.Summary(env.Feedback, result.LogPath, handoff.FeedbackLimit)
	return graph.Output{AgentID: n.agentID, Envelope: env, Raw: raw}, nil
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
func gitDiff(workdir string) string { diff, _ := reviewDiff(workdir); return diff }

func reviewDiff(workdir string) (string, error) {
	// Read without altering the real index or staging new files.
	cmd := exec.Command("git", "diff", "--binary", "HEAD", "--", ".", ":(exclude)SPEC.md", ":(exclude).colony")
	cmd.Dir = workdir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	list := exec.Command("git", "ls-files", "--others", "--exclude-standard", "-z")
	list.Dir = workdir
	names, err := list.Output()
	if err != nil {
		return "", err
	}
	var diff strings.Builder
	diff.Write(out)
	for _, name := range strings.Split(string(names), "\x00") {
		if name == "" || name == "SPEC.md" || strings.HasPrefix(name, ".colony/") {
			continue
		}
		add := exec.Command("git", "diff", "--no-index", "--binary", "--", "/dev/null", name)
		add.Dir = workdir
		data, err := add.Output()
		if err != nil {
			if status, ok := err.(*exec.ExitError); !ok || status.ExitCode() != 1 {
				return "", fmt.Errorf("diff untracked %s: %w", name, err)
			}
		}
		diff.Write(data)
	}
	return diff.String(), nil
}

// ReviewNodeFactory returns a NodeFactory for the review role.
func ReviewNodeFactory(agentID string, cfg config.LLMConfig) (graph.Node, error) {
	return NewReviewNode(agentID, cfg), nil
}
