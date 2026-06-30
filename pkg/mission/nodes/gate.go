package nodes

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jirateep/colony/pkg/config"
	"github.com/jirateep/colony/pkg/mission/graph"
	"github.com/jirateep/colony/pkg/module"
)

// GateNode is a Node that runs quality gates via RunGateCaptureAll and returns
// APPROVED on success (exit 0) or REJECTED + captured output on failure.
// It does NOT use an LLM — it shells out directly to the gate commands.
type GateNode struct {
	agentID string
	lang    string
	skip    map[string]bool
}

// NewGateNode creates a GateNode that runs quality gates for the given language.
// skip optionally contains gate names to omit (e.g. "format" for --no-format).
func NewGateNode(agentID, lang string, skip map[string]bool) *GateNode {
	return &GateNode{agentID: agentID, lang: lang, skip: skip}
}

func (n *GateNode) Run(ctx context.Context, in graph.Input) (graph.Output, error) {
	workdir := "."
	if wd, ok := in.Params["workdir"].(string); ok && wd != "" {
		workdir = wd
	}
	// lang and skip gates are configured via Mission Params at runtime, falling
	// back to the factory-time values. Without this the gate would always run
	// the registration default, gating e.g. a TypeScript task with Go commands.
	lang := n.lang
	if l, ok := in.Params["lang"].(string); ok && l != "" {
		lang = l
	}
	skip := n.skip
	if s, ok := in.Params["skip_gates"].(map[string]bool); ok && s != nil {
		skip = s
	}
	output, err := module.RunGateCaptureAll(lang, workdir, skip)
	if err == nil {
		return graph.Output{
			AgentID: n.agentID,
			Envelope: graph.Envelope{
				Decision: graph.APPROVED,
				Feedback: "",
				Output:   mustMarshal("all gates passed"),
			},
		}, nil
	}
	// Gate failed — return REJECTED with the captured output as feedback.
	return graph.Output{
		AgentID: n.agentID,
		Envelope: graph.Envelope{
			Decision: graph.REJECTED,
			Feedback: fmt.Sprintf("Gate %q failed.\n\n%s", lang, output),
			Output:   mustMarshal(output),
		},
	}, nil
}

// GateNodeFactory returns a NodeFactory that creates GateNode instances.
// The lang and skip are captured at registration time.
func GateNodeFactory(lang string, skip map[string]bool) graph.NodeFactory {
	return func(agentID string, cfg config.LLMConfig) (graph.Node, error) {
		return NewGateNode(agentID, lang, skip), nil
	}
}

// mustMarshal marshals v to JSON, panicking on failure.
func mustMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return json.RawMessage(data)
}
