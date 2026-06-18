package mission

import (
	"context"
	"encoding/json"
)

// Role names used for registry registration of built-in agent roles.
const (
	RoleGate       = "gate"
	RoleBuilder    = "builder"
	RoleFixer      = "fixer"
	RoleEscalation = "escalation"
	RoleReview     = "review"
)

// Decision is the routing decision returned by a node.
type Decision string

const (
	APPROVED      Decision = "APPROVED"
	REJECTED      Decision = "REJECTED"
	REPROCESS     Decision = "REPROCESS"
	CLARIFICATION Decision = "CLARIFICATION"
)

// Envelope is the fixed JSON schema every agent must return.
type Envelope struct {
	Decision Decision        `json:"decision"`
	Feedback string          `json:"feedback"`
	Output   json.RawMessage `json:"output"`
}

// OutputText returns the output as a plain string.
// If the output is a JSON string value, it is unquoted; otherwise the raw JSON is returned.
func (e Envelope) OutputText() string {
	if len(e.Output) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(e.Output, &s); err == nil {
		return s
	}
	return string(e.Output)
}

// Input is passed to a Node when it is executed.
type Input struct {
	Text   string
	Params map[string]any
}

// Output holds the result of a Node execution.
type Output struct {
	AgentID  string
	Envelope Envelope
	Raw      string // raw text before JSON parse; set when parsing fails
}

// Node is the unit of execution in a mission graph.
type Node interface {
	Run(ctx context.Context, in Input) (Output, error)
}
