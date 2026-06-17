package mission

// BuildGateFixOpts configures the BuildGateFix mission template.
type BuildGateFixOpts struct {
	Name       string
	Input      string
	Lang       string
	SkipGates  map[string]bool // gate names to skip (e.g. {"format": true})
	MaxCycles  int
	// EscalationRole, when non-nil, wires an escalation node that runs after
	// max-cycles with a red gate. The mission itself does not enforce the
	// escalation transition — the caller (loop steward) handles it.
	EscalationRole string
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
func BuildGateFix(opts BuildGateFixOpts) *Mission {
	agents := []Agent{
		{ID: "builder", Role: RoleBuilder},
		{ID: "gate", Role: RoleGate},
		{ID: "fixer", Role: RoleFixer},
	}
	flow := []Edge{
		{From: "__input__", To: "builder"},
		{From: "builder", To: "gate"},
		{From: "gate", OnApprove: "__output__"},
		{From: "gate", OnReject: "fixer"},
		{From: "fixer", To: "gate"}, // back-edge: fixer → gate
	}

	if opts.EscalationRole != "" {
		agents = append(agents, Agent{ID: "escalation", Role: opts.EscalationRole})
		flow = append(flow, Edge{From: "gate", OnReject: "escalation"})
		flow = append(flow, Edge{From: "escalation", To: "__output__"})
	}

	maxCycles := opts.MaxCycles
	if maxCycles <= 0 {
		maxCycles = 5 // default
	}

	return &Mission{
		Name:      opts.Name,
		Input:     opts.Input,
		MaxCycles: maxCycles,
		Agents:    agents,
		Flow:      flow,
		Params: map[string]any{
			"lang":       opts.Lang,
			"skip_gates": opts.SkipGates,
		},
	}
}
