package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// FlowChoice is the tool/model pick remembered for one flow (e.g. "fix",
// "review", "craft"), so the shared picker can preselect it next time.
type FlowChoice struct {
	Tool  string `json:"tool"`
	Model string `json:"model"`
}

// State is Colony's local, per-machine state. Unlike Config (.colony/config.json,
// which the user edits by hand and may commit), State lives in its own file
// and holds data Colony manages for itself — currently just the remembered
// tool/model choice per flow (see "Open Questions" §2 in the blocked-task
// recovery spec: this belongs in local state, not project config).
type State struct {
	Flows map[string]FlowChoice `json:"flows,omitempty"`
}

// StatePath returns the path to the local state file for a project root.
func StatePath(projectRoot string) string {
	return filepath.Join(projectRoot, ".colony", "state.json")
}

// LoadState reads the local state file. A missing file is not an error — it
// returns the zero-value State, so a project with no saved choices yet
// behaves the same as one with an empty state file.
func LoadState(projectRoot string) (*State, error) {
	data, err := os.ReadFile(StatePath(projectRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return &State{}, nil
		}
		return nil, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	return &st, nil
}

// SaveState writes the local state file, creating .colony if needed.
func SaveState(projectRoot string, st *State) error {
	dir := filepath.Join(projectRoot, ".colony")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(StatePath(projectRoot), append(data, '\n'), 0644)
}

// ChoiceForFlow returns the remembered choice for a flow and whether one has
// been saved. An unsaved flow returns the zero FlowChoice and false, so the
// picker always shows (per Open Questions §1) with nothing preselected.
func (s *State) ChoiceForFlow(flow string) (FlowChoice, bool) {
	if s.Flows == nil {
		return FlowChoice{}, false
	}
	c, ok := s.Flows[flow]
	return c, ok
}

// SetChoiceForFlow remembers the tool/model choice for a flow.
func (s *State) SetChoiceForFlow(flow string, choice FlowChoice) {
	if s.Flows == nil {
		s.Flows = map[string]FlowChoice{}
	}
	s.Flows[flow] = choice
}
