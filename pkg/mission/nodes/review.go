package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/futureboard-dev/colony/pkg/config"
	"github.com/futureboard-dev/colony/pkg/handoff"
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
	// run is the headless model call; tests substitute it to exercise
	// batching and coverage checks without a model CLI.
	run func(ctx context.Context, workdir, prompt string) (llm.Result, error)
}

// NewReviewNode creates a ReviewNode.
func NewReviewNode(agentID string, cfg config.LLMConfig) *ReviewNode {
	return &ReviewNode{agentID: agentID, cfg: cfg, run: llm.New(cfg).RunHeadlessResult}
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
	base, _ := in.Params["base"].(string)
	reviewContext := "## Specification\n\n" + spec + committedOnBranch(workdir, base)
	if prior, _ := in.Params["prior_feedback"].(string); strings.TrimSpace(prior) != "" {
		reviewContext += "\n\n## Previously reported issues\n\nAn earlier attempt was rejected for the issues below; some may since have been fixed. Verify each against the current diff: any that is still unresolved is a reason to reject.\n\n" + handoff.Bound(prior, handoff.FeedbackLimit)
	}
	evidencePath, err := handoff.Save(workdir, "review-input", reviewContext+"\n\n## Diff\n\n"+diff)
	if err != nil {
		return graph.Output{}, err
	}
	// A model cannot review binary content, so it can never be approved.
	if strings.Contains(diff, "GIT binary patch") {
		return graph.Output{}, fmt.Errorf("review coverage blocked: binary artifacts present; no content was reviewed or approved; full input: %s", evidencePath)
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

	combined := injectInput(prompt.ReviewLoop(), reviewContext+"\n\n## Git diff of implementation\n\n"+diff)
	if len(combined) > handoff.ReviewLimit {
		// Never truncate: review every byte in batches with verified coverage.
		return n.runBatches(ctx, workdir, reviewContext, diff, evidencePath)
	}

	result, err := n.run(ctx, workdir, combined)
	if err != nil {
		return graph.Output{AgentID: n.agentID}, fmt.Errorf("agent %q: review call failed: %w", n.agentID, err)
	}
	env, err := parseDecision(n.agentID, result)
	if err != nil {
		return graph.Output{AgentID: n.agentID, Raw: result.Text}, err
	}
	env.Feedback = handoff.Summary(env.Feedback, result.LogPath, handoff.FeedbackLimit)
	return graph.Output{AgentID: n.agentID, Envelope: env, Raw: result.Text}, nil
}

// parseDecision extracts a review envelope that must decide APPROVED or REJECTED.
func parseDecision(agentID string, result llm.Result) (graph.Envelope, error) {
	var env graph.Envelope
	if err := json.Unmarshal([]byte(extractJSON(result.Text)), &env); err != nil {
		return env, fmt.Errorf("agent %q: invalid review envelope: %w\n--- raw output ---\n%s\n---", agentID, err, handoff.Bound(result.Text, 500))
	}
	if env.Decision != graph.APPROVED && env.Decision != graph.REJECTED {
		return env, fmt.Errorf("invalid review decision %q; full log: %s", env.Decision, result.LogPath)
	}
	return env, nil
}

// committedOnBranch lists files already committed on this branch since base.
// They are absent from the working diff (e.g. regenerated API clients committed
// separately), so the reviewer must not reject the task for missing them.
func committedOnBranch(workdir, base string) string {
	if base == "" {
		return ""
	}
	cmd := exec.Command("git", "log", "--stat", "--format=%h %s", "origin/"+base+"..HEAD")
	cmd.Dir = workdir
	out, err := cmd.Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return ""
	}
	return "\n\n## Already committed on this branch (not in the diff below)\n\n" + handoff.Bound(string(out), handoff.FeedbackLimit)
}

// PartNotesFile is where a group part's agent writes its handoff notes in its
// worktree. It is git-excluded and moved to the spec folder when the part lands.
const PartNotesFile = "PART_NOTES.md"

// reviewDiff returns the combined staged+unstaged diff of the worktree against
// HEAD plus every untracked file and the part's handoff notes, without
// altering the index.
// Returns "" when there are no changes.
func reviewDiff(workdir string) (string, error) {
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
	files := strings.Split(string(names), "\x00")
	// A group part's handoff notes are git-excluded so they never get
	// committed, but they explain the part's choices and the reviewer needs them.
	if _, err := os.Stat(filepath.Join(workdir, PartNotesFile)); err == nil {
		ignored := exec.Command("git", "check-ignore", "-q", PartNotesFile)
		ignored.Dir = workdir
		if ignored.Run() == nil {
			files = append(files, PartNotesFile)
		}
	}
	var diff strings.Builder
	diff.Write(out)
	for _, name := range files {
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
