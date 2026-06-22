package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jirateep/colony/pkg/config"
)

// testRegistry returns a registry with a fake "always-approve" role for testing.
func testRegistry(roles ...string) *Registry {
	reg := NewRegistry()
	for _, role := range roles {
		r := role
		reg.Register(r, func(agentID string, cfg config.LLMConfig) (Node, error) {
			return &fakeNode{decision: APPROVED, output: "ok"}, nil
		})
	}
	return reg
}

func writeMission(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.mission.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMissionBasic(t *testing.T) {
	path := writeMission(t, `
name: my-mission
input: "hello world"
max_cycles: 3
agents:
  - id: writer
    role: business-analyst
flow:
  - from: __input__
    to: writer
  - from: writer
    to: __output__
`)
	m, err := LoadMission(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "my-mission" {
		t.Errorf("expected name my-mission, got %s", m.Name)
	}
	if m.Input != "hello world" {
		t.Errorf("expected input hello world, got %s", m.Input)
	}
	if len(m.Agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(m.Agents))
	}
	if m.MaxCycles != 3 {
		t.Errorf("expected max_cycles 3, got %d", m.MaxCycles)
	}
}

func TestLoadMissionMissingName(t *testing.T) {
	path := writeMission(t, `
input: "hello"
agents: []
flow: []
`)
	_, err := LoadMission(path)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestLoadMissionFileNotFound(t *testing.T) {
	_, err := LoadMission("/nonexistent/path.mission.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestBuildGraphBasicLinear(t *testing.T) {
	m := &Mission{
		Name:  "linear",
		Input: "test",
		Agents: []Agent{
			{ID: "a", Role: "fake"},
			{ID: "b", Role: "fake"},
		},
		Flow: []Edge{
			{From: "__input__", To: "a"},
			{From: "a", To: "b"},
			{From: "b", To: "__output__"},
		},
	}
	reg := testRegistry("fake")
	g, err := BuildGraph(m, reg, func(role string) config.LLMConfig {
		return config.LLMConfig{Provider: "anthropic", Model: "test"}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(g.Nodes))
	}
}

func TestBuildGraphRejectsReservedInputID(t *testing.T) {
	m := &Mission{
		Name:   "bad",
		Input:  "test",
		Agents: []Agent{{ID: "__input__", Role: "fake"}},
		Flow:   []Edge{},
	}
	reg := testRegistry("fake")
	_, err := BuildGraph(m, reg, func(string) config.LLMConfig {
		return config.LLMConfig{}
	})
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("expected reserved id error, got %v", err)
	}
}

func TestBuildGraphRejectsReservedOutputID(t *testing.T) {
	m := &Mission{
		Name:   "bad",
		Input:  "test",
		Agents: []Agent{{ID: "__output__", Role: "fake"}},
		Flow:   []Edge{},
	}
	reg := testRegistry("fake")
	_, err := BuildGraph(m, reg, func(string) config.LLMConfig {
		return config.LLMConfig{}
	})
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("expected reserved id error, got %v", err)
	}
}

func TestBuildGraphRejectsUnknownRole(t *testing.T) {
	m := &Mission{
		Name:   "bad-role",
		Input:  "test",
		Agents: []Agent{{ID: "x", Role: "nonexistent-role"}},
		Flow:   []Edge{{From: "__input__", To: "x"}},
	}
	reg := NewRegistry() // empty registry
	_, err := BuildGraph(m, reg, func(string) config.LLMConfig {
		return config.LLMConfig{}
	})
	if err == nil {
		t.Fatal("expected error for unknown role")
	}
}

func TestBuildGraphFanOutFanIn(t *testing.T) {
	m := &Mission{
		Name:  "fan",
		Input: "test",
		Agents: []Agent{
			{ID: "a", Role: "fake"},
			{ID: "b", Role: "fake"},
			{ID: "c", Role: "fake"},
			{ID: "d", Role: "fake"},
		},
		Flow: []Edge{
			{From: "__input__", To: "a"},
			{From: "a", To: "b"},
			{From: "a", To: "c"},
			{From: "b", To: "d"},
			{From: "c", To: "d"},
			{From: "d", To: "__output__"},
		},
	}
	reg := testRegistry("fake")
	g, err := BuildGraph(m, reg, func(string) config.LLMConfig {
		return config.LLMConfig{}
	})
	if err != nil {
		t.Fatal(err)
	}
	// d should have static in-degree 2 (from b and c)
	if g.StaticInDegree["d"] != 2 {
		t.Errorf("expected in-degree 2 for d, got %d", g.StaticInDegree["d"])
	}
	// a should have static in-degree 1 (from __input__)
	if g.StaticInDegree["a"] != 1 {
		t.Errorf("expected in-degree 1 for a, got %d", g.StaticInDegree["a"])
	}
}

func TestBuildGraphBackEdgeDetection(t *testing.T) {
	m := &Mission{
		Name:  "cyclic",
		Input: "test",
		Agents: []Agent{
			{ID: "writer", Role: "fake"},
			{ID: "reviewer", Role: "fake"},
		},
		Flow: []Edge{
			{From: "__input__", To: "writer"},
			{From: "writer", OnApprove: "__output__", OnReject: "reviewer"},
			{From: "reviewer", To: "writer"},
		},
		MaxCycles: 3,
	}
	reg := testRegistry("fake")
	g, err := BuildGraph(m, reg, func(string) config.LLMConfig {
		return config.LLMConfig{}
	})
	if err != nil {
		t.Fatal(err)
	}
	// reviewer→writer should be a back-edge
	if !g.IsBackEdge("reviewer", "writer") {
		t.Error("expected reviewer→writer to be a back-edge")
	}
	// __input__→writer should NOT be a back-edge
	if g.IsBackEdge("__input__", "writer") {
		t.Error("__input__→writer should not be a back-edge")
	}
}
