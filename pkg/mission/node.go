package mission

import "context"

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
	Decision Decision `json:"decision"`
	Feedback string   `json:"feedback"`
	Output   string   `json:"output"`
}

// Input is passed to a Node when it is executed.
type Input struct {
	Text string
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
