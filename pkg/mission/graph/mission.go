package graph

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Mission is the top-level structure of a *.mission.yaml file.
type Mission struct {
	Name      string         `yaml:"name"`
	Input     string         `yaml:"input"`
	MaxCycles int            `yaml:"max_cycles"`
	Params    map[string]any `yaml:"params,omitempty"`
	Agents    []Agent        `yaml:"agents"`
	Flow      []Edge         `yaml:"flow"`
}

// Agent declares a node in the mission graph.
type Agent struct {
	ID          string `yaml:"id"`
	Role        string `yaml:"role"`
	Interactive bool   `yaml:"interactive,omitempty"`
}

// Edge declares a directed connection between nodes.
// Use To for unconditional edges; OnApprove/OnReject for decision-driven routing.
type Edge struct {
	From      string `yaml:"from"`
	To        string `yaml:"to,omitempty"`
	OnApprove string `yaml:"on_approve,omitempty"`
	OnReject  string `yaml:"on_reject,omitempty"`
}

// LoadMission reads and parses a mission YAML file.
func LoadMission(path string) (*Mission, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read mission: %w", err)
	}
	var m Mission
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse mission yaml: %w", err)
	}
	if m.Name == "" {
		return nil, fmt.Errorf("mission name is required")
	}
	return &m, nil
}
