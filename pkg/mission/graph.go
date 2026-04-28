package mission

import (
	"fmt"

	"github.com/jirateep/colony/pkg/config"
)

// EdgeCondition controls when an edge fires.
type EdgeCondition int

const (
	EdgeAll     EdgeCondition = iota // fires on any decision (unconditional "to" edge)
	EdgeApprove                      // fires on APPROVED
	EdgeReject                       // fires on REJECTED or REPROCESS
)

// GraphEdge is a resolved outgoing edge in the executable graph.
type GraphEdge struct {
	To        string
	Condition EdgeCondition
}

// Graph is the executable representation of a Mission.
type Graph struct {
	Nodes          map[string]Node
	Agents         map[string]*Agent
	OutEdges       map[string][]GraphEdge
	StaticInDegree map[string]int // in-degree excluding back-edges
	backEdges      map[string]bool // "from:to" → true
}

// IsBackEdge returns true if the from→to edge is a cycle back-edge.
func (g *Graph) IsBackEdge(from, to string) bool {
	return g.backEdges[from+":"+to]
}

// BuildGraph validates the mission and constructs the executable Graph.
// reg is used to instantiate nodes for each agent role.
// cfg is the base LLM config; role-specific overrides are looked up from Colony config.
func BuildGraph(m *Mission, reg *Registry, roleCfg func(role string) config.LLMConfig) (*Graph, error) {
	const inputID = "__input__"
	const outputID = "__output__"

	// Validate reserved IDs are not used by agents.
	for _, a := range m.Agents {
		if a.ID == inputID || a.ID == outputID {
			return nil, fmt.Errorf("agent id %q is reserved and cannot be used", a.ID)
		}
	}

	// Build agent map and instantiate nodes.
	agents := make(map[string]*Agent, len(m.Agents))
	nodes := make(map[string]Node, len(m.Agents))

	for i := range m.Agents {
		a := &m.Agents[i]
		if _, dup := agents[a.ID]; dup {
			return nil, fmt.Errorf("duplicate agent id %q", a.ID)
		}
		agents[a.ID] = a

		cfg := roleCfg(a.Role)
		node, err := reg.Create(a.Role, a.ID, cfg)
		if err != nil {
			return nil, fmt.Errorf("agent %q: %w", a.ID, err)
		}
		nodes[a.ID] = node
	}

	// Collect all valid node IDs (including sentinels).
	validIDs := make(map[string]bool, len(agents)+2)
	validIDs[inputID] = true
	validIDs[outputID] = true
	for id := range agents {
		validIDs[id] = true
	}

	// Build outgoing edge lists.
	outEdges := make(map[string][]GraphEdge)
	for _, e := range m.Flow {
		if !validIDs[e.From] {
			return nil, fmt.Errorf("edge from unknown node %q", e.From)
		}
		if e.To != "" {
			if !validIDs[e.To] {
				return nil, fmt.Errorf("edge to unknown node %q", e.To)
			}
			outEdges[e.From] = append(outEdges[e.From], GraphEdge{To: e.To, Condition: EdgeAll})
		}
		if e.OnApprove != "" {
			if !validIDs[e.OnApprove] {
				return nil, fmt.Errorf("on_approve to unknown node %q", e.OnApprove)
			}
			outEdges[e.From] = append(outEdges[e.From], GraphEdge{To: e.OnApprove, Condition: EdgeApprove})
		}
		if e.OnReject != "" {
			if !validIDs[e.OnReject] {
				return nil, fmt.Errorf("on_reject to unknown node %q", e.OnReject)
			}
			outEdges[e.From] = append(outEdges[e.From], GraphEdge{To: e.OnReject, Condition: EdgeReject})
		}
	}

	// Detect back-edges via DFS from __input__.
	backEdges := detectBackEdges(outEdges, inputID)

	// Compute static in-degree (excluding back-edges) for fan-in tracking.
	staticInDegree := make(map[string]int, len(agents))
	for id := range agents {
		staticInDegree[id] = 0
	}
	for from, edges := range outEdges {
		for _, e := range edges {
			key := from + ":" + e.To
			if !backEdges[key] && e.To != outputID && e.To != inputID {
				staticInDegree[e.To]++
			}
		}
	}

	return &Graph{
		Nodes:          nodes,
		Agents:         agents,
		OutEdges:       outEdges,
		StaticInDegree: staticInDegree,
		backEdges:      backEdges,
	}, nil
}

// detectBackEdges performs DFS from startID and returns a set of back-edge keys ("from:to").
func detectBackEdges(outEdges map[string][]GraphEdge, startID string) map[string]bool {
	backEdges := make(map[string]bool)
	color := make(map[string]int) // 0=white,1=gray,2=black

	var dfs func(id string)
	dfs = func(id string) {
		color[id] = 1
		for _, e := range outEdges[id] {
			switch color[e.To] {
			case 1:
				backEdges[id+":"+e.To] = true
			case 0:
				dfs(e.To)
			}
		}
		color[id] = 2
	}
	dfs(startID)
	return backEdges
}

// MatchesDecision returns true if the edge should fire for the given decision.
func MatchesDecision(d Decision, e GraphEdge) bool {
	switch e.Condition {
	case EdgeAll:
		return true
	case EdgeApprove:
		return d == APPROVED
	case EdgeReject:
		return d == REJECTED || d == REPROCESS
	default:
		return false
	}
}
