package blueprint

import (
	"github.com/futureboard-dev/colony/pkg/mission/graph"
)

// BuildGateFixOpts configures the BuildGateFix mission template.
type BuildGateFixOpts struct {
	Name        string
	Input       string
	Lang        string
	Workdir     string          // directory the builder/fixer/gate operate in (worktree path); "" = current dir
	SkipGates   map[string]bool // gate names to skip (e.g. {"format": true})
	SkipBuilder bool            // start from gate instead of builder (use when worktree already has code)
	MaxCycles   int
	// EscalationRole, when non-nil, wires an escalation node that runs after
	// max-cycles with a red gate. The mission itself does not enforce the
	// escalation transition — the caller (loop steward) handles it.
	EscalationRole string
	// ReviewRole, when non-empty, inserts an LLM review node between a green gate
	// and __output__: gate(APPROVED) → review → (APPROVED → __output__) /
	// (REJECTED → fixer). It catches stubs/unimplemented spec items the
	// deterministic gate cannot, before a task is committed.
	ReviewRole string
}

// BuildGateFix returns a populated Mission configured for the build→gate→fix
// loop. The mission has:
//   - a builder node (reads spec → implements)
//   - a gate node (runs quality gates)
//   - a fixer node (reads gate failing output via REJECTED feedback)
//   - an escalation node when opts.EscalationRole is non-empty
//
// The graph is: __input__ → builder → gate → (APPROVED → __output__) /
// (REJECTED → fixer → gate → ...) bounded by max_cycles.
func BuildGateFix(opts BuildGateFixOpts) *graph.Mission {
	agents := []graph.Agent{
		{ID: "gate", Role: graph.RoleGate},
		{ID: "fixer", Role: graph.RoleFixer},
	}
	flow := []graph.Edge{
		{From: "gate", OnApprove: "__output__"},
		{From: "gate", OnReject: "fixer"},
		{From: "fixer", To: "gate"},
	}

	if !opts.SkipBuilder {
		agents = append([]graph.Agent{{ID: "builder", Role: graph.RoleBuilder}}, agents...)
		flow = append([]graph.Edge{
			{From: "__input__", To: "builder"},
			{From: "builder", To: "gate"},
		}, flow...)
	} else {
		flow = append([]graph.Edge{{From: "__input__", To: "gate"}}, flow...)
	}

	if opts.ReviewRole != "" {
		agents = append(agents, graph.Agent{ID: "review", Role: opts.ReviewRole})
		// Redirect the green gate to review instead of straight to output.
		for i := range flow {
			if flow[i].From == "gate" && flow[i].OnApprove == "__output__" {
				flow[i].OnApprove = "review"
			}
		}
		flow = append(flow,
			graph.Edge{From: "review", OnApprove: "__output__"},
			graph.Edge{From: "review", OnReject: "fixer"}, // back-edge: review → fixer
		)
	}

	if opts.EscalationRole != "" {
		agents = append(agents, graph.Agent{ID: "escalation", Role: opts.EscalationRole})
		flow = append(flow, graph.Edge{From: "gate", OnReject: "escalation"})
		flow = append(flow, graph.Edge{From: "escalation", To: "__output__"})
	}

	maxCycles := opts.MaxCycles
	if maxCycles <= 0 {
		maxCycles = 3 // default
	}

	return &graph.Mission{
		Name:      opts.Name,
		Input:     opts.Input,
		MaxCycles: maxCycles,
		Agents:    agents,
		Flow:      flow,
		Params: map[string]any{
			"lang":       opts.Lang,
			"skip_gates": opts.SkipGates,
			"workdir":    opts.Workdir,
		},
	}
}
