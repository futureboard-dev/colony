package mission

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jirateep/colony/pkg/storage"
)

const (
	inputNode  = "__input__"
	outputNode = "__output__"
)

// ErrMaxCycles is returned when a cyclic node exceeds the mission's max_cycles limit.
type ErrMaxCycles struct {
	NodeID string
}

func (e *ErrMaxCycles) Error() string {
	return fmt.Sprintf("max_cycles exceeded for node %q: mission failed", e.NodeID)
}

// Runner executes a mission graph.
type Runner interface {
	Run(ctx context.Context, m *Mission, g *Graph, sessionID string, store storage.Store) (*Output, error)
}

// NewRunner returns the default in-process runner.
func NewRunner() Runner {
	return &defaultRunner{}
}

type defaultRunner struct{}

func (r *defaultRunner) Run(ctx context.Context, m *Mission, g *Graph, sessionID string, store storage.Store) (*Output, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type nodeResult struct {
		nodeID string
		output Output
		err    error
	}

	resultCh := make(chan nodeResult, len(g.Nodes)+10)

	runCount := make(map[string]int)
	pendingCount := make(map[string]int)
	collected := make(map[string][]Output)
	stepNum := 0
	active := 0
	var finalOutput *Output
	var runErr error

	// Copy static in-degrees.
	for id, deg := range g.StaticInDegree {
		pendingCount[id] = deg
	}

	dispatchNode := func(nodeID string, inputs []Output) {
		runCount[nodeID]++
		thisRun := runCount[nodeID]
		stepNum++
		thisStep := stepNum
		active++
		go func() {
			out, err := r.executeNode(ctx, m, g, nodeID, inputs, thisRun, thisStep, store, sessionID)
			resultCh <- nodeResult{nodeID: nodeID, output: out, err: err}
		}()
	}

	// Seed from __input__.
	inputOut := Output{
		AgentID:  inputNode,
		Envelope: Envelope{Decision: APPROVED, Output: m.Input},
		Raw:      m.Input,
	}
	for _, e := range g.OutEdges[inputNode] {
		if !MatchesDecision(APPROVED, e) {
			continue
		}
		if e.To == outputNode {
			finalOutput = &inputOut
			continue
		}
		if g.IsBackEdge(inputNode, e.To) {
			continue
		}
		collected[e.To] = append(collected[e.To], inputOut)
		pendingCount[e.To]--
		if pendingCount[e.To] <= 0 {
			inputs := collected[e.To]
			delete(collected, e.To)
			dispatchNode(e.To, inputs)
		}
	}

	// Process results.
	for active > 0 {
		select {
		case <-ctx.Done():
			for active > 0 {
				<-resultCh
				active--
			}
			if runErr != nil {
				return nil, runErr
			}
			return nil, ctx.Err()
		case nr := <-resultCh:
			active--
			if nr.err != nil {
				runErr = nr.err
				cancel()
				for active > 0 {
					<-resultCh
					active--
				}
				return nil, runErr
			}

			decision := nr.output.Envelope.Decision

			for _, e := range g.OutEdges[nr.nodeID] {
				if !MatchesDecision(decision, e) {
					continue
				}

				nextID := e.To

				if nextID == outputNode {
					finalOutput = &nr.output
					continue
				}

				if g.IsBackEdge(nr.nodeID, nextID) {
					// Cycle: check max_cycles before re-dispatching.
					if m.MaxCycles > 0 && runCount[nextID] >= m.MaxCycles {
						cycErr := &ErrMaxCycles{NodeID: nextID}
						cancel()
						for active > 0 {
							<-resultCh
							active--
						}
						return nil, cycErr
					}
					dispatchNode(nextID, []Output{nr.output})
				} else {
					collected[nextID] = append(collected[nextID], nr.output)
					pendingCount[nextID]--
					if pendingCount[nextID] <= 0 {
						inputs := collected[nextID]
						delete(collected, nextID)
						dispatchNode(nextID, inputs)
					}
				}
			}
		}
	}

	if runErr != nil {
		return nil, runErr
	}
	return finalOutput, nil
}

func (r *defaultRunner) executeNode(
	ctx context.Context,
	m *Mission,
	g *Graph,
	nodeID string,
	inputs []Output,
	runNum int,
	stepNum int,
	store storage.Store,
	sessionID string,
) (Output, error) {
	node := g.Nodes[nodeID]
	agent := g.Agents[nodeID]

	inputText := combineInputs(inputs)
	startedAt := time.Now()

	out, execErr := node.Run(ctx, Input{Text: inputText})

	finishedAt := time.Now()
	durationMS := finishedAt.Sub(startedAt).Milliseconds()

	// Determine output JSON to persist.
	outputJSON := out.Raw
	if outputJSON == "" && execErr == nil {
		if data, err := json.Marshal(out.Envelope); err == nil {
			outputJSON = string(data)
		}
	}

	decision := ""
	if execErr == nil {
		decision = string(out.Envelope.Decision)
	}

	step := storage.Step{
		SessionID:  sessionID,
		StepNum:    stepNum,
		SubStep:    subStepFor(runNum),
		AgentID:    nodeID,
		Role:       agent.Role,
		InputText:  inputText,
		OutputJSON: outputJSON,
		Decision:   decision,
		DurationMS: durationMS,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
	}
	if err := store.InsertStep(step); err != nil {
		slog.Warn("failed to persist step", "step_num", stepNum, "agent", nodeID, "err", err)
	}

	if execErr != nil {
		return out, execErr
	}

	if out.Envelope.Decision == "" {
		return out, fmt.Errorf("agent %q returned empty decision", nodeID)
	}

	return out, nil
}

// combineInputs merges multiple upstream outputs into a single input text.
func combineInputs(inputs []Output) string {
	if len(inputs) == 1 {
		return inputs[0].Envelope.Output
	}
	parts := make([]string, 0, len(inputs))
	for _, inp := range inputs {
		if inp.Envelope.Output != "" {
			parts = append(parts, inp.Envelope.Output)
		} else if inp.Raw != "" {
			parts = append(parts, inp.Raw)
		}
	}
	return strings.Join(parts, "\n\n---\n\n")
}

// subStepFor converts a run count to a sub_step label.
// Run 1 → "", run 2 → "b", run 3 → "c", etc.
func subStepFor(runNum int) string {
	const letters = "bcdefghijklmnopqrstuvwxyz"
	if runNum <= 1 {
		return ""
	}
	idx := runNum - 2
	if idx < len(letters) {
		return string(letters[idx])
	}
	return fmt.Sprintf("z%d", idx-len(letters)+1)
}
