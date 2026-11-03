package fix

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/futureboard-dev/colony/pkg/llm"
	"github.com/futureboard-dev/colony/pkg/storage"
)

// storeTriager implements Triager by giving a fresh headless agent one attempt
// to fix a blocked task before the user is asked (see Phase 4: Automatic
// triage). It never takes over the terminal: the user chooses whether to open
// an interactive session only after this attempt. It uses 04's block_attempts
// counter to hard-cap itself at one attempt per block, and tells a fix apart
// from a real decision by checking whether the agent changed the task's
// worktree.
type storeTriager struct {
	open func() (storage.Store, error)
}

func init() {
	SetTriager(storeTriager{open: defaultOpenStore})
}

// triageTimeout bounds the unattended attempt so a hung agent can't hold a
// task, or the blocked prompt waiting on it, forever.
const triageTimeout = 10 * time.Minute

// triageInstructions tells the unattended agent how its result is judged: only
// uncommitted worktree changes count as a fix.
const triageInstructions = "\nThis is an unattended attempt and nobody can answer questions. " +
	"If the fix is simple, make it and leave it uncommitted. " +
	"If it needs a decision from the user, change nothing.\n"

// runTriageAgent does the actual headless run. It is a package-level var so
// tests can observe the attempt without shelling out to a real agent CLI.
var runTriageAgent = func(ctx context.Context, ex *llm.Executor, workdir, prompt string, out io.Writer) error {
	return ex.RunAgent(ctx, workdir, prompt, out)
}

// Diagnose implements Triager.
func (t storeTriager) Diagnose(ctx context.Context, req Request) (Diagnosis, error) {
	store, err := t.open()
	if err != nil {
		return Diagnosis{}, fmt.Errorf("triage: open store: %w", err)
	}
	defer func() { _ = store.Close() }()

	tasks, err := store.QueryTasks(storage.TaskFilter{ID: req.TaskID})
	if err != nil {
		return Diagnosis{}, fmt.Errorf("triage: query task %s: %w", req.TaskID, err)
	}
	if len(tasks) == 0 {
		return Diagnosis{}, fmt.Errorf("triage: task %s not found", req.TaskID)
	}
	task := tasks[0]

	// Already had its one attempt for this block: go straight to the user
	// with an explanation, never open a second session.
	if task.BlockAttempts > 0 {
		return explainOnly(ctx, req)
	}

	// An unattended agent may edit files, so it only ever runs in the task's own
	// worktree, never in the project root an interactive session falls back to.
	worktree := req.Worktree
	if info, err := os.Stat(worktree); worktree == "" || err != nil || !info.IsDir() {
		return explainOnly(ctx, req)
	}

	choice, err := picker{}.ResolveChoice(ctx, req)
	if err != nil {
		return Diagnosis{}, fmt.Errorf("triage: resolve tool: %w", err)
	}

	if _, err := store.IncrementBlockAttempts(req.TaskID); err != nil {
		return Diagnosis{}, fmt.Errorf("triage: increment attempts: %w", err)
	}

	out := req.Out
	if out == nil {
		out = io.Discard
	}
	fmt.Fprintf(out, "fix: one unattended attempt with %s · %s\n", choice.Tool, modelLabel(choice.Model))

	before := worktreeSnapshot(worktree)

	ctx, cancel := context.WithTimeout(ctx, triageTimeout)
	defer cancel()
	prompt := buildFixPrompt(req, worktree) + triageInstructions
	if err := runTriageAgent(ctx, llm.New(llmConfigForChoice(choice)), worktree, prompt, out); err != nil {
		return Diagnosis{}, fmt.Errorf("triage: run agent: %w", err)
	}

	if worktreeSnapshot(worktree) != before {
		change := summarizeChange(worktreeStatus(worktree))
		if change == "" {
			change = "changes made in " + worktree
		}
		return Diagnosis{Fixed: true, Change: change}, nil
	}

	// No changes left behind: this is a real decision, not a simple fix.
	// Return the diagnosis without retrying.
	return explainOnly(ctx, req)
}

// worktreeSnapshot captures status plus tracked content. A blocked task's
// worktree is usually already dirty, so status alone misses a fix to a file
// that was already modified.
func worktreeSnapshot(worktree string) string {
	cmd := exec.Command("git", "diff", "HEAD")
	cmd.Dir = worktree
	diff, _ := cmd.Output() // no HEAD yet just leaves the status to compare
	return worktreeStatus(worktree) + string(diff)
}

// explainOnly builds a Diagnosis carrying only an Explanation (Fixed left
// false), for blocks the fresh session didn't change anything for. It uses
// the registered Explainer (03) when one is available, but 05 doesn't
// depend on 03 being wired up: if none is configured, it falls back to the
// raw block reason as the cause so a diagnosis is always attached.
func explainOnly(ctx context.Context, req Request) (Diagnosis, error) {
	explanation, err := Explain(ctx, req)
	if err != nil {
		if !errors.Is(err, ErrNotConfigured) {
			return Diagnosis{}, fmt.Errorf("triage: explain: %w", err)
		}
		explanation = Explanation{Cause: req.Reason}
	}
	return Diagnosis{Explanation: explanation}, nil
}

// worktreeStatus returns `git status --porcelain` for worktree, or "" if it
// can't be read (e.g. not a git repo yet) so a missing worktree never looks
// like a fix.
func worktreeStatus(worktree string) string {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = worktree
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// summarizeChange turns `git status --porcelain` output into a short,
// human-readable list of what changed, for Diagnosis.Change.
func summarizeChange(status string) string {
	var parts []string
	for line := range strings.SplitSeq(strings.TrimRight(status, "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		code, path := fields[0], strings.Join(fields[1:], " ")
		switch {
		case strings.Contains(code, "D"):
			parts = append(parts, "deleted "+path)
		case strings.Contains(code, "A") || code == "??":
			parts = append(parts, "added "+path)
		default:
			parts = append(parts, "modified "+path)
		}
	}
	return strings.Join(parts, ", ")
}
