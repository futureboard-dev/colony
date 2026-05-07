package mission

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

	fmt.Fprintf(r.logWriter, "Mission %q [%s]\n", m.Name, sessionID)

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

		role := g.Agents[nodeID].Role
		if m.MaxCycles > 0 {
			fmt.Fprintf(r.logWriter, "  ▶ %s (%s) step %d run %d/%d\n", nodeID, role, thisStep, thisRun, m.MaxCycles)
		} else {
			fmt.Fprintf(r.logWriter, "  ▶ %s (%s) step %d\n", nodeID, role, thisStep)
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

	totalSteps := stepNum
	totalDuration := time.Since(startedAt)
	fmt.Fprintf(r.logWriter, "Mission %q completed: %d steps, %s\n", m.Name, totalSteps, fmtDuration(totalDuration))

	return finalOutput, nil
}

func (r *defaultRunner) executeNode(
	ctx context.Context,
	_ *Mission,
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

	out, execErr = node.Run(ctx, Input{Text: currentInput})

	// Clarification loop: only fires for interactive agents that return CLARIFICATION.
	for execErr == nil && out.Envelope.Decision == CLARIFICATION && agent.Interactive {
		answer, err := r.clarify(nodeID, out.Envelope.Feedback)
		if err != nil {
			execErr = fmt.Errorf("agent %q: clarify: %w", nodeID, err)
			break
		}
		currentInput = currentInput + "\n\n## User Clarification\n\n" + answer
		out, execErr = node.Run(ctx, Input{Text: currentInput})
	}

	// Non-interactive agent returning CLARIFICATION is treated as REJECTED.
	if execErr == nil && out.Envelope.Decision == CLARIFICATION && !agent.Interactive {
		out.Envelope.Decision = REJECTED
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
	mark := "✓"
	if execErr != nil {
		mark = "✗"
	} else {
		switch out.Envelope.Decision {
		case REJECTED, REPROCESS:
			mark = "✗"
		case CLARIFICATION:
			mark = "?"
		}
	}
	fmt.Fprintf(r.logWriter, "  %s %s (%s) [%s] (%s)\n", mark, nodeID, agent.Role, decision, fmtDuration(duration))

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
