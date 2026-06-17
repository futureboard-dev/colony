package storage

// Task represents a task in the loop queue with optional spec and gate overrides.
type Task struct {
	ID           string
	Description  string
	State        string // "open", "needs-fix", "done", "blocked"
	SpecPath     string // path to the spec file (--file)
	BaseBranch   string // base branch (--base / --from)
	GateOverrides string // JSON-encoded gate override flags, e.g. {"no-format":true}
	CycleCount   int
	Lang         string
}
