package mission

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jirateep/colony/pkg/config"
	"github.com/jirateep/colony/pkg/storage"
)

// fakeNode is a test Node that returns a fixed decision and output, optionally with a delay.
type fakeNode struct {
	decision Decision
	output   string
	delay    time.Duration
}

func (n *fakeNode) Run(ctx context.Context, in Input) (Output, error) {
	if n.delay > 0 {
		select {
		case <-time.After(n.delay):
		case <-ctx.Done():
			return Output{}, ctx.Err()
		}
	}
	rawOut, _ := json.Marshal(n.output)
	return Output{
		Envelope: Envelope{Decision: n.decision, Output: json.RawMessage(rawOut)},
		Raw:      fmt.Sprintf(`{"decision":%q,"feedback":"","output":%q}`, n.decision, n.output),
	}, nil
}

// badJSONNode returns malformed JSON.
type badJSONNode struct{}

func (n *badJSONNode) Run(ctx context.Context, in Input) (Output, error) {
	raw := "this is not json"
	return Output{Raw: raw}, fmt.Errorf("invalid JSON envelope: failed to decode")
}

// openTestStore opens a temporary SQLite store for testing.
func openTestStore(t *testing.T) storage.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func buildTestMission(name string, agents []Agent, flow []Edge, maxCycles int) *Mission {
	return &Mission{Name: name, Input: "test input", MaxCycles: maxCycles, Agents: agents, Flow: flow}
}

func buildTestGraph(t *testing.T, m *Mission, nodes map[string]Node) *Graph {
	t.Helper()
	reg := NewRegistry()
	for id, node := range nodes {
		n := node
		reg.Register(id, func(agentID string, cfg config.LLMConfig) (Node, error) {
			return n, nil
		})
	}
	// Give each agent the role matching its ID so registry lookup works.
	for i := range m.Agents {
		m.Agents[i].Role = m.Agents[i].ID
	}
	g, err := BuildGraph(m, reg, func(role string) config.LLMConfig {
		return config.LLMConfig{Provider: "anthropic"}
	})
	if err != nil {
		t.Fatalf("BuildGraph: %v", err)
	}
	return g
}

func seedSession(t *testing.T, store storage.Store, sessID, missionName string) {
	t.Helper()
	if err := store.InsertSession(storage.Session{
		ID: sessID, MissionName: missionName,
		StartedAt: time.Now(), Status: "running",
	}); err != nil {
		t.Fatalf("InsertSession: %v", err)
	}
}

// TestLinearFlow: A → B → C, all APPROVED. 3 steps recorded.
func TestLinearFlow(t *testing.T) {
	m := buildTestMission("linear", []Agent{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	}, []Edge{
		{From: "__input__", To: "a"},
		{From: "a", To: "b"},
		{From: "b", To: "c"},
		{From: "c", To: "__output__"},
	}, 0)

	nodes := map[string]Node{
		"a": &fakeNode{decision: APPROVED, output: "out-a"},
		"b": &fakeNode{decision: APPROVED, output: "out-b"},
		"c": &fakeNode{decision: APPROVED, output: "out-c"},
	}
	g := buildTestGraph(t, m, nodes)

	store := openTestStore(t)
	sessID := "linear-test"
	seedSession(t, store, sessID, m.Name)

	runner := NewRunner()
	out, err := runner.Run(context.Background(), m, g, sessID, store)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out == nil || out.Envelope.OutputText() != "out-c" {
		t.Errorf("expected final output out-c, got %+v", out)
	}

	steps, err := store.QuerySteps(storage.StepFilter{SessionID: sessID})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 {
		t.Errorf("expected 3 steps, got %d", len(steps))
	}
}

// TestFanOut: A → [B, C] run concurrently, asserted by timing.
func TestFanOut(t *testing.T) {
	delay := 150 * time.Millisecond
	m := buildTestMission("fanout", []Agent{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	}, []Edge{
		{From: "__input__", To: "a"},
		{From: "a", To: "b"},
		{From: "a", To: "c"},
		{From: "b", To: "__output__"},
		{From: "c", To: "__output__"},
	}, 0)

	nodes := map[string]Node{
		"a": &fakeNode{decision: APPROVED, output: "out-a"},
		"b": &fakeNode{decision: APPROVED, output: "out-b", delay: delay},
		"c": &fakeNode{decision: APPROVED, output: "out-c", delay: delay},
	}
	g := buildTestGraph(t, m, nodes)

	store := openTestStore(t)
	sessID := "fanout-test"
	seedSession(t, store, sessID, m.Name)

	start := time.Now()
	runner := NewRunner()
	_, err := runner.Run(context.Background(), m, g, sessID, store)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// If concurrent, total time ≈ delay (not 2*delay)
	if elapsed > 2*delay {
		t.Errorf("fan-out did not run concurrently: elapsed %v > 2*delay %v", elapsed, 2*delay)
	}

	steps, err := store.QuerySteps(storage.StepFilter{SessionID: sessID})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 {
		t.Errorf("expected 3 steps (a, b, c), got %d", len(steps))
	}
}

// TestFanIn: [B, C] → D, D must not start until both B and C return.
func TestFanIn(t *testing.T) {
	var dStarted int64 // atomic flag set when d.Run() is called

	delay := 100 * time.Millisecond
	m := buildTestMission("fanin", []Agent{
		{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"},
	}, []Edge{
		{From: "__input__", To: "a"},
		{From: "a", To: "b"},
		{From: "a", To: "c"},
		{From: "b", To: "d"},
		{From: "c", To: "d"},
		{From: "d", To: "__output__"},
	}, 0)

	nodes := map[string]Node{
		"a": &fakeNode{decision: APPROVED, output: "out-a"},
		"b": &fakeNode{decision: APPROVED, output: "out-b", delay: delay},
		"c": &fakeNode{decision: APPROVED, output: "out-c"},
		"d": &barrierNode{
			started: &dStarted,
			inner:   &fakeNode{decision: APPROVED, output: "out-d"},
		},
	}
	g := buildTestGraph(t, m, nodes)

	store := openTestStore(t)
	sessID := "fanin-test"
	seedSession(t, store, sessID, m.Name)

	runner := NewRunner()
	out, err := runner.Run(context.Background(), m, g, sessID, store)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out == nil || out.Envelope.OutputText() != "out-d" {
		t.Errorf("expected out-d as final output, got %+v", out)
	}
	if atomic.LoadInt64(&dStarted) == 0 {
		t.Error("d was never started")
	}
}

// barrierNode wraps a node and sets an atomic flag when Run is called.
type barrierNode struct {
	started *int64
	inner   Node
}

func (n *barrierNode) Run(ctx context.Context, in Input) (Output, error) {
	atomic.StoreInt64(n.started, 1)
	return n.inner.Run(ctx, in)
}

// TestCyclicMaxCycles: always-REJECT cycle terminates after exactly max_cycles total runs.
func TestCyclicMaxCycles(t *testing.T) {
	const maxCycles = 3
	var runCount int64

	m := buildTestMission("cyclic", []Agent{
		{ID: "writer"}, {ID: "reviewer"},
	}, []Edge{
		{From: "__input__", To: "writer"},
		{From: "writer", OnApprove: "__output__", OnReject: "reviewer"},
		{From: "reviewer", To: "writer"},
	}, maxCycles)

	nodes := map[string]Node{
		"writer": &countingNode{
			count: &runCount,
			inner: &fakeNode{decision: REJECTED, output: "draft"},
		},
		"reviewer": &fakeNode{decision: REJECTED, output: "rejected"},
	}
	g := buildTestGraph(t, m, nodes)

	store := openTestStore(t)
	sessID := "cyclic-test"
	seedSession(t, store, sessID, m.Name)

	runner := NewRunner()
	_, err := runner.Run(context.Background(), m, g, sessID, store)
	if err == nil {
		t.Fatal("expected error from max_cycles exceeded")
	}

	runs := atomic.LoadInt64(&runCount)
	if runs != maxCycles {
		t.Errorf("expected writer to run exactly %d times, ran %d", maxCycles, runs)
	}

	// Verify sub_steps in DB
	steps, err := store.QuerySteps(storage.StepFilter{SessionID: sessID})
	if err != nil {
		t.Fatal(err)
	}
	var writerSteps []storage.Step
	for _, s := range steps {
		if s.AgentID == "writer" {
			writerSteps = append(writerSteps, s)
		}
	}
	if len(writerSteps) != maxCycles {
		t.Errorf("expected %d writer steps in DB, got %d", maxCycles, len(writerSteps))
	}
	// Check sub_steps: first = "", second = "b", third = "c"
	expected := []string{"", "b", "c"}
	for i, s := range writerSteps {
		if s.SubStep != expected[i] {
			t.Errorf("step %d: expected sub_step %q, got %q", i+1, expected[i], s.SubStep)
		}
	}
}

// countingNode increments a counter each time Run is called.
type countingNode struct {
	count *int64
	inner Node
}

func (n *countingNode) Run(ctx context.Context, in Input) (Output, error) {
	atomic.AddInt64(n.count, 1)
	return n.inner.Run(ctx, in)
}

// TestCyclicApproveSkipsBackEdge: APPROVED exits the cycle.
func TestCyclicApproveSkipsBackEdge(t *testing.T) {
	m := buildTestMission("approve-cycle", []Agent{
		{ID: "writer"}, {ID: "reviewer"},
	}, []Edge{
		{From: "__input__", To: "writer"},
		{From: "writer", OnApprove: "__output__", OnReject: "reviewer"},
		{From: "reviewer", To: "writer"},
	}, 5)

	nodes := map[string]Node{
		"writer":   &fakeNode{decision: APPROVED, output: "final draft"},
		"reviewer": &fakeNode{decision: REJECTED, output: "nope"},
	}
	g := buildTestGraph(t, m, nodes)

	store := openTestStore(t)
	sessID := "approve-cycle-test"
	seedSession(t, store, sessID, m.Name)

	runner := NewRunner()
	out, err := runner.Run(context.Background(), m, g, sessID, store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == nil || out.Envelope.OutputText() != "final draft" {
		t.Errorf("expected approved output, got %+v", out)
	}
}

// recordingNode captures every input.Text it receives, then returns a scripted
// sequence of decisions/outputs (one per call). If calls exceed the script,
// the last entry repeats.
type recordingNode struct {
	calls  []string
	script []struct {
		dec Decision
		out string
	}
}

func (n *recordingNode) Run(_ context.Context, in Input) (Output, error) {
	n.calls = append(n.calls, in.Text)
	idx := len(n.calls) - 1
	if idx >= len(n.script) {
		idx = len(n.script) - 1
	}
	step := n.script[idx]
	rawOut, _ := json.Marshal(step.out)
	return Output{
		Envelope: Envelope{Decision: step.dec, Output: json.RawMessage(rawOut)},
		Raw:      fmt.Sprintf(`{"decision":%q,"feedback":"","output":%q}`, step.dec, step.out),
	}, nil
}

// TestBackEdgeMergesPriorContext: when a reviewer rejects back to an earlier
// node, that node should see its prior input merged with the reviewer's
// feedback — not just the feedback alone. This prevents context loss in
// reject loops (e.g. estimator losing the original spec when reviewer rejects).
func TestBackEdgeMergesPriorContext(t *testing.T) {
	estimator := &recordingNode{script: []struct {
		dec Decision
		out string
	}{
		{dec: APPROVED, out: "estimate-v1"}, // first run
		{dec: APPROVED, out: "estimate-v2"}, // second run after reject
	}}
	reviewer := &recordingNode{script: []struct {
		dec Decision
		out string
	}{
		{dec: REJECTED, out: "needs more detail"}, // first review
		{dec: APPROVED, out: "looks good"},        // second review approves
	}}

	m := buildTestMission("back-edge-merge", []Agent{
		{ID: "estimator"}, {ID: "reviewer"},
	}, []Edge{
		{From: "__input__", To: "estimator"},
		{From: "estimator", To: "reviewer"},
		{From: "reviewer", OnApprove: "__output__", OnReject: "estimator"},
	}, 5)
	m.Input = "original spec"

	g := buildTestGraph(t, m, map[string]Node{
		"estimator": estimator,
		"reviewer":  reviewer,
	})

	store := openTestStore(t)
	sessID := "back-edge-merge-test"
	seedSession(t, store, sessID, m.Name)

	runner := NewRunner()
	out, err := runner.Run(context.Background(), m, g, sessID, store)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out == nil || out.Envelope.OutputText() != "looks good" {
		t.Fatalf("expected final output 'looks good', got %+v", out)
	}

	if len(estimator.calls) != 2 {
		t.Fatalf("expected estimator called twice, got %d", len(estimator.calls))
	}
	// First call: just the original input.
	if !strings.Contains(estimator.calls[0], "original spec") {
		t.Errorf("first estimator call should contain original spec, got: %s", estimator.calls[0])
	}
	// Second call: original spec PLUS reviewer's rejection output.
	if !strings.Contains(estimator.calls[1], "original spec") {
		t.Errorf("second estimator call should still contain original spec, got: %s", estimator.calls[1])
	}
	if !strings.Contains(estimator.calls[1], "needs more detail") {
		t.Errorf("second estimator call should contain reviewer feedback, got: %s", estimator.calls[1])
	}
}

// TestMalformedEnvelope: bad JSON from node causes run to fail, step persisted with raw text.
func TestMalformedEnvelope(t *testing.T) {
	m := buildTestMission("bad-json", []Agent{
		{ID: "broken"},
	}, []Edge{
		{From: "__input__", To: "broken"},
		{From: "broken", To: "__output__"},
	}, 0)

	nodes := map[string]Node{
		"broken": &badJSONNode{},
	}
	g := buildTestGraph(t, m, nodes)

	store := openTestStore(t)
	sessID := "bad-json-test"
	seedSession(t, store, sessID, m.Name)

	runner := NewRunner()
	_, err := runner.Run(context.Background(), m, g, sessID, store)
	if err == nil {
		t.Fatal("expected error from malformed envelope")
	}

	// Step should be persisted with raw text
	steps, err := store.QuerySteps(storage.StepFilter{SessionID: sessID})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 {
		t.Errorf("expected 1 step persisted, got %d", len(steps))
	}
	if steps[0].OutputJSON != "this is not json" {
		t.Errorf("expected raw text in output_json, got %q", steps[0].OutputJSON)
	}
}

// clarifyNode returns CLARIFICATION on the first call, then APPROVED on the second.
// It records what input text it received on each call.
type clarifyNode struct {
	calls  []string
	answer string // set after first call to simulate enriched input
}

func (n *clarifyNode) Run(_ context.Context, in Input) (Output, error) {
	n.calls = append(n.calls, in.Text)
	if len(n.calls) == 1 {
		return Output{
			Envelope: Envelope{Decision: CLARIFICATION, Feedback: "What is the budget?"},
			Raw:      `{"decision":"CLARIFICATION","feedback":"What is the budget?","output":""}`,
		}, nil
	}
	rawOut, _ := json.Marshal("final stories")
	return Output{
		Envelope: Envelope{Decision: APPROVED, Output: json.RawMessage(rawOut)},
		Raw:      `{"decision":"APPROVED","feedback":"","output":"final stories"}`,
	}, nil
}

// TestClarificationInteractiveAgent: interactive agent asks a question, gets an answer,
// then produces final output. Only one step should be persisted (the final result).
func TestClarificationInteractiveAgent(t *testing.T) {
	node := &clarifyNode{}
	m := buildTestMission("clarify-interactive", []Agent{
		{ID: "analyst", Interactive: true},
	}, []Edge{
		{From: "__input__", To: "analyst"},
		{From: "analyst", To: "__output__"},
	}, 0)

	g := buildTestGraph(t, m, map[string]Node{"analyst": node})

	store := openTestStore(t)
	sessID := "clarify-interactive-test"
	seedSession(t, store, sessID, m.Name)

	clarifyFn := func(agentID, questions string) (string, error) {
		if agentID != "analyst" {
			t.Errorf("unexpected agentID in clarify: %s", agentID)
		}
		if questions != "What is the budget?" {
			t.Errorf("unexpected questions: %s", questions)
		}
		return "$100k", nil
	}

	runner := newRunnerWithClarify(clarifyFn)
	out, err := runner.Run(context.Background(), m, g, sessID, store)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out == nil || out.Envelope.OutputText() != "final stories" {
		t.Errorf("expected final output 'final stories', got %+v", out)
	}

	// Node should have been called twice.
	if len(node.calls) != 2 {
		t.Fatalf("expected 2 node calls (clarify + final), got %d", len(node.calls))
	}
	// Second call must include the user's answer.
	if !strings.Contains(node.calls[1], "$100k") {
		t.Errorf("second call input should contain user answer, got: %s", node.calls[1])
	}
	// Only one step persisted (the final result).
	steps, err := store.QuerySteps(storage.StepFilter{SessionID: sessID})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 {
		t.Errorf("expected 1 persisted step, got %d", len(steps))
	}
	if steps[0].Decision != string(APPROVED) {
		t.Errorf("persisted step should have APPROVED decision, got %s", steps[0].Decision)
	}
}

// TestClarificationAllAgents: any agent returning CLARIFICATION (regardless of the
// interactive field) prompts the user and loops until a terminal decision is reached.
func TestClarificationAllAgents(t *testing.T) {
	node := &clarifyNode{}
	m := buildTestMission("clarify-non-interactive", []Agent{
		{ID: "analyst"}, // interactive: false (default) — clarification still fires
	}, []Edge{
		{From: "__input__", To: "analyst"},
		{From: "analyst", OnApprove: "__output__"},
	}, 0)

	g := buildTestGraph(t, m, map[string]Node{
		"analyst": node,
	})

	store := openTestStore(t)
	sessID := "clarify-non-interactive-test"
	seedSession(t, store, sessID, m.Name)

	called := 0
	clarifyFn := func(_, _ string) (string, error) {
		called++
		return "user answer", nil
	}

	runner := newRunnerWithClarify(clarifyFn)
	out, err := runner.Run(context.Background(), m, g, sessID, store)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// CLARIFICATION triggered the clarify loop; analyst ran twice (clarify + approved).
	if out == nil || out.Envelope.OutputText() != "final stories" {
		t.Errorf("expected final stories, got %+v", out)
	}
	if called != 1 {
		t.Errorf("expected clarify called once, got %d", called)
	}
	if len(node.calls) != 2 {
		t.Errorf("expected 2 analyst calls, got %d", len(node.calls))
	}
}
