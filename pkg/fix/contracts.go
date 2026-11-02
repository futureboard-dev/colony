// Package fix holds the shared types and registration hooks for Colony's
// blocked-task recovery flow (fix, tell, shell, skip; picker; triage).
// Later parts register their implementations through Set* hooks in
// registry.go; this file only declares the surface they implement.
package fix

import "context"

// Tool identifies one of the agent tools Colony can drive.
type Tool string

const (
	ToolClaude Tool = "claude"
	ToolCodex  Tool = "codex"
	ToolCrush  Tool = "crush"
)

// Choice is the tool and model a flow will use.
type Choice struct {
	Tool  Tool
	Model string
}

// Request carries everything a fix flow needs to act on a blocked task
// without the user re-explaining it.
type Request struct {
	TaskID      string
	Description string
	Reason      string
	Worktree    string
	Logs        string
	Hint        string
	Flow        string
	Choice      Choice
}

// Explanation is a human-readable account of why a task blocked and what
// to do next, in place of a raw error (see Phase 3: Clearer block reasons).
type Explanation struct {
	Cause    string
	NextStep string
}

// Diagnosis is the result of a single automatic triage attempt on a
// blocked task (see Phase 4: Automatic triage).
type Diagnosis struct {
	Explanation Explanation
	Fixed       bool
	Change      string
}

// Resolver picks the tool and model for a flow: which tools are installed,
// and, given a request, which choice to use.
type Resolver interface {
	InstalledTools(ctx context.Context) ([]Tool, error)
	ResolveChoice(ctx context.Context, req Request) (Choice, error)
}

// Session opens a fresh agent session for a blocked task, already loaded
// with its error, logs, task and worktree.
type Session interface {
	OpenSession(ctx context.Context, req Request) error
}

// Explainer turns a block's raw reason into a likely cause and next step.
type Explainer interface {
	Explain(ctx context.Context, req Request) (Explanation, error)
}

// Retrier resumes a blocked task, keeping its block reason and anything
// the user added, in the same worktree.
type Retrier interface {
	Retry(ctx context.Context, req Request) error
}

// Triager gives a fresh agent one attempt to diagnose, and possibly fix,
// a blocked task before it reaches the user.
type Triager interface {
	Diagnose(ctx context.Context, req Request) (Diagnosis, error)
}
