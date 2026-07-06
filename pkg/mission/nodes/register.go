package nodes

import (
	"github.com/futureboard-dev/colony/pkg/config"
	"github.com/futureboard-dev/colony/pkg/mission/graph"
)

// Register registers all built-in node factories with the given registry.
// This replaces the previous init()-based self-registration pattern.
func Register(reg *graph.Registry) {
	// Register built-in roles backed by module prompts.
	for _, role := range []string{"business-analyst", "architect", "project-manager", "estimator"} {
		r := role // capture
		reg.Register(r, func(agentID string, cfg config.LLMConfig) (graph.Node, error) {
			return NewLLMNode(agentID, r, cfg), nil
		})
	}
	// Register the gate role — no LLM, runs quality gates via RunGateCaptureAll.
	// The lang and skip gates are configured via Mission Params at runtime.
	reg.Register(graph.RoleGate, func(agentID string, cfg config.LLMConfig) (graph.Node, error) {
		return NewGateNode(agentID, "go", nil), nil
	})
	// Register the builder role — uses build.md prompt via BuilderNode.
	reg.Register(graph.RoleBuilder, BuilderNodeFactory)
	// Register the fixer role — uses fix.md prompt via FixerNode.
	reg.Register(graph.RoleFixer, FixerNodeFactory)
	// Register the review role — LLM semantic gate via ReviewNode.
	reg.Register(graph.RoleReview, ReviewNodeFactory)
}
