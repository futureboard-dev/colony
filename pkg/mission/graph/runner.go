package graph

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/futureboard-dev/colony/pkg/storage"
)

const (
	inputNode  = "__input__"
	outputNode = "__output__"
)

// ANSI colors for the runner's per-step status marks.
const (
	clrReset = "\033[0m"
	clrGreen = "\033[32m"
	clrRed   = "\033[31m"
	clrBlue  = "\033[34m"
)

// ErrMaxCycles is returned when a cyclic node exceeds the mission's max_cycles limit.
type ErrMaxCycles struct {
	NodeID     string
	LastOutput *Output
}

func (e *ErrMaxCycles) Error() string {
	return fmt.Sprintf("max_cycles exceeded for node %q: mission failed", e.NodeID)
}

// ErrStuck is returned when a fix cycle reproduces the identical gate failure —
// the fixer is making no progress, so the loop bails early instead of burning
// further cycles. Carries the repeated output for feedback.
type ErrStuck struct {
	NodeID     string
	LastOutput *Output
}

func (e *ErrStuck) Error() string {
	return fmt.Sprintf("stuck at node %q: identical failure repeated, bailing", e.NodeID)
}

// Runner executes a mission graph.
type Runner interface {
	Run(ctx context.Context, m *Mission, g *Graph, sessionID string, store storage.Store) (*Output, error)
}

// ClarifyFn is called when an interactive agent returns CLARIFICATION.
// It receives the agent ID and the questions from the feedback field,
// and returns the user's answer to be appended to the agent's input.
type ClarifyFn func(agentID, questions string) (string, error)

// NewRunner returns the default in-process runner that reads clarifications from stdin.
func NewRunner() Runner {
	return &defaultRunner{
		clarify:   stdinClarify,
		logWriter: os.Stderr,
	}
}

// newRunnerWithClarify creates a runner with an injectable clarify function (used in tests).
func newRunnerWithClarify(fn ClarifyFn) Runner {
	return &defaultRunner{
		clarify:   fn,
		logWriter: os.Stderr,
	}
}

type defaultRunner struct {
	clarify   ClarifyFn
	logWriter io.Writer
}

// stdinClarify is the production ClarifyFn: prints questions and reads the user's answer.
func stdinClarify(agentID, questions string) (string, error) {
	fmt.Printf("\n--- %s needs clarification ---\n", agentID)
	fmt.Println(strings.TrimSpace(questions))
	fmt.Print("\nYour response (press Enter twice when done): ")

	scanner := bufio.NewScanner(os.Stdin)
	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" && len(lines) > 0 {
			break
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}

func (r *defaultRunner) Run(ctx context.Context, m *Mission, g *Graph, sessionID string, store storage.Store) (*Output, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	startedAt := time.Now()

	fmt.Fprintf(r.logWriter, "%sMission %q [%s]%s\n", clrBlue, m.Name, sessionID, clrReset)

	type nodeResult struct {
		nodeID string
		output Output
		err    error
	}

	resultCh := make(chan nodeResult, len(g.Nodes)+10)

	runCount := make(map[string]int)
	pendingCount := make(map[string]int)
	collected := make(map[string][]Output)
	// lastInputs caches the most recent input set each node was dispatched with.
	// Used on back-edges so a rejecting upstream's feedback is merged with the
	// node's prior context, instead of replacing it.
	lastInputs := make(map[string][]Output)
	// prevReject tracks the last REJECTED feedback that drove each back-edge, so
	// we can bail early when a fix cycle produces the identical failure (the
	// fixer is stuck and burning tokens to no effect).
	prevReject := make(map[string]string)
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
		lastInputs[nodeID] = inputs

		role := g.Agents[nodeID].Role
		if m.MaxCycles > 0 {
			fmt.Fprintf(r.logWriter, "  %s▶ %s (%s) step %d run %d/%d%s\n", clrBlue, nodeID, role, thisStep, thisRun, m.MaxCycles, clrReset)
		} else {
			fmt.Fprintf(r.logWriter, "  %s▶ %s (%s) step %d%s\n", clrBlue, nodeID, role, thisStep, clrReset)
		}

		go func() {
			out, err := r.executeNode(ctx, m, g, nodeID, inputs, thisRun, thisStep, store, sessionID)
			resultCh <- nodeResult{nodeID: nodeID, output: out, err: err}
		}()
	}

	// Seed from __input__.
	rawInput, _ := json.Marshal(m.Input)
	inputOut := Output{
		AgentID:  inputNode,
		Envelope: Envelope{Decision: APPROVED, Output: json.RawMessage(rawInput)},
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
				return &nr.output, runErr
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
						cycErr := &ErrMaxCycles{NodeID: nextID, LastOutput: &nr.output}
						cancel()
						for active > 0 {
							<-resultCh
							active--
						}
						return nil, cycErr
					}
					// Stuck-detection: if this back-edge fired before with the
					// identical rejecting feedback, the fixer is making no
					// progress — bail rather than burn another full cycle.
					fb := nr.output.Envelope.Feedback
					if prev, seen := prevReject[nextID]; seen && fb != "" && fb == prev {
						stuckErr := &ErrStuck{NodeID: nextID, LastOutput: &nr.output}
						cancel()
						for active > 0 {
							<-resultCh
							active--
						}
						return nil, stuckErr
					}
					prevReject[nextID] = fb
					// Merge the rejecting upstream's output with whatever inputs
					// the target last ran with, so the target sees its prior
					// context (e.g. the original spec) plus the new feedback.
					prior := lastInputs[nextID]
					merged := make([]Output, 0, len(prior)+1)
					merged = append(merged, prior...)
					merged = append(merged, nr.output)
					dispatchNode(nextID, merged)
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

	totalSteps := stepNum
	totalDuration := time.Since(startedAt)
	fmt.Fprintf(r.logWriter, "%sMission %q completed: %d steps, %s%s\n", clrGreen, m.Name, totalSteps, fmtDuration(totalDuration), clrReset)

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

	currentInput := combineInputs(inputs)
	startedAt := time.Now()

	var out Output
	var execErr error

	out, execErr = node.Run(ctx, Input{Text: currentInput, Params: m.Params})

	// Clarification loop: any agent returning CLARIFICATION prompts the user.
	for execErr == nil && out.Envelope.Decision == CLARIFICATION {
		answer, err := r.clarify(nodeID, out.Envelope.Feedback)
		if err != nil {
			execErr = fmt.Errorf("agent %q: clarify: %w", nodeID, err)
			break
		}
		currentInput = currentInput + "\n\n## User Clarification\n\n" + answer
		out, execErr = node.Run(ctx, Input{Text: currentInput, Params: m.Params})
	}

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

	// Log completion.
	duration := finishedAt.Sub(startedAt)
	mark, color := "✓", clrGreen
	if execErr != nil {
		mark, color = "✗", clrRed
	} else {
		switch out.Envelope.Decision {
		case REJECTED, REPROCESS:
			mark, color = "✗", clrRed
		case CLARIFICATION:
			mark, color = "?", clrBlue
		}
	}
	fmt.Fprintf(r.logWriter, "  %s%s %s (%s) [%s] (%s)%s\n", color, mark, nodeID, agent.Role, decision, fmtDuration(duration), clrReset)

	step := storage.Step{
		SessionID:  sessionID,
		StepNum:    stepNum,
		SubStep:    subStepFor(runNum),
		AgentID:    nodeID,
		Role:       agent.Role,
		InputText:  currentInput,
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
		return inputs[0].Envelope.OutputText()
	}
	parts := make([]string, 0, len(inputs))
	for _, inp := range inputs {
		if text := inp.Envelope.OutputText(); text != "" {
			parts = append(parts, text)
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

// fmtDuration formats a duration for human-readable logging.
func fmtDuration(d time.Duration) string {
	d = d.Round(100 * time.Millisecond)
	s := d.Seconds()
	if s < 60 {
		return fmt.Sprintf("%.1fs", s)
	}
	m := int(d.Minutes())
	sec := int(d.Seconds()) % 60
	if m < 60 {
		return fmt.Sprintf("%dm %ds", m, sec)
	}
	h := m / 60
	m = m % 60
	return fmt.Sprintf("%dh %dm", h, m)
}
