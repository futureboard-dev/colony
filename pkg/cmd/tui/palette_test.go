package tui

import (
	"strings"
	"testing"
)

// specByName finds a palette command for tests.
func specByName(t *testing.T, name string) (int, cmdSpec) {
	t.Helper()
	for i, spec := range paletteCommands {
		if spec.Name == name {
			return i, spec
		}
	}
	t.Fatalf("palette command %q not found", name)
	return 0, cmdSpec{}
}

func TestPaletteOmitsDefaultsFromArgv(t *testing.T) {
	idx, spec := specByName(t, "loop")
	m := newTestModel(t, ViewDashboard, sampleStore())
	m.openPalette()
	m.selectPaletteCommand(idx)

	argv, err := m.palette.buildArgv(spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(argv, " "); got != "loop" {
		t.Errorf("untouched form should produce bare argv, got %q", got)
	}
}

func TestPaletteBuildsFlagsThatDifferFromDefaults(t *testing.T) {
	idx, spec := specByName(t, "loop")
	m := newTestModel(t, ViewDashboard, sampleStore())
	m.openPalette()
	m.selectPaletteCommand(idx)

	for i, f := range spec.Flags {
		switch f.Name {
		case "once", "review":
			m.palette.bools[i] = true
		case "lang":
			m.palette.values[i] = "typescript"
		case "max-cycles":
			m.palette.values[i] = "5"
		}
	}

	argv, err := m.palette.buildArgv(spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := strings.Join(argv, " ")
	for _, want := range []string{"loop", "--once", "--review", "--lang typescript", "--max-cycles 5"} {
		if !strings.Contains(got, want) {
			t.Errorf("argv %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "--idle") || strings.Contains(got, "--max-passes") {
		t.Errorf("untouched defaults leaked into argv: %q", got)
	}
}

func TestPaletteRequiredFlagBlocksSubmit(t *testing.T) {
	idx, spec := specByName(t, "craft")
	m := newTestModel(t, ViewDashboard, sampleStore())
	m.openPalette()
	m.selectPaletteCommand(idx)

	if _, err := m.palette.buildArgv(spec); err == nil {
		t.Fatal("expected craft to require --lang")
	}

	m.modal = ModalPalette
	m.Update(key("enter"))
	if m.palette.err == "" {
		t.Error("expected an inline error after submitting without --lang")
	}
	if m.runner.Running() {
		t.Error("no process should start when validation fails")
	}
}

func TestPaletteBoolDisabledExplicitlyWhenDefaultTrue(t *testing.T) {
	idx, spec := specByName(t, "loop clear")
	m := newTestModel(t, ViewDashboard, sampleStore())
	m.openPalette()
	m.selectPaletteCommand(idx)

	for i, f := range spec.Flags {
		if f.Name == "yes" {
			if !m.palette.bools[i] {
				t.Fatal("--yes should default to on")
			}
			m.palette.bools[i] = false
		}
	}

	argv, err := m.palette.buildArgv(spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(argv, " "); !strings.Contains(got, "--yes=false") {
		t.Errorf("turning off a default-true flag must be explicit, got %q", got)
	}
}

func TestPalettePositionalArgument(t *testing.T) {
	idx, spec := specByName(t, "spec-feature")
	m := newTestModel(t, ViewDashboard, sampleStore())
	m.openPalette()
	m.selectPaletteCommand(idx)
	m.palette.posValue = "initial-notes-sanitization"

	argv, err := m.palette.buildArgv(spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(argv, " "); got != "spec-feature initial-notes-sanitization" {
		t.Errorf("positional not placed before flags: %q", got)
	}
}

func TestPalettePrefillsPositionalFromQueueSelection(t *testing.T) {
	cases := []struct {
		command string
		want    string
	}{
		{"loop run", "t-1"},
		{"loop retry-review", "t-1"},
		{"loop retry-gate", "t-1"},
		{"task done", "colony/t-1"}, // the worktree branch, not the task ID
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			idx, _ := specByName(t, tc.command)
			m := newTestModel(t, ViewQueue, sampleStore())
			// The queue is sorted, so point the cursor at t-1 by ID.
			for i, task := range Apply(m.tasks, m.queue) {
				if task.ID == "t-1" {
					m.cursor = i
				}
			}
			m.openPalette()
			m.selectPaletteCommand(idx)

			if m.palette.posValue != tc.want {
				t.Errorf("prefilled positional = %q, want %q", m.palette.posValue, tc.want)
			}
		})
	}
}

func TestPaletteRequiredPositionalBlocksSubmit(t *testing.T) {
	idx, spec := specByName(t, "loop run")
	m := newTestModel(t, ViewQueue, &stubStore{}) // no tasks, so nothing to prefill
	m.openPalette()
	m.selectPaletteCommand(idx)
	if m.palette.posValue != "" {
		t.Fatalf("expected an empty positional with no queue selection, got %q", m.palette.posValue)
	}

	if _, err := m.palette.buildArgv(spec); err == nil {
		t.Fatal("expected loop run to require <task-id>")
	}

	m.modal = ModalPalette
	m.Update(key("enter"))
	if m.palette.err == "" {
		t.Error("expected an inline error after submitting without <task-id>")
	}
	if m.runner.Running() {
		t.Error("no process should start when validation fails")
	}
}

func TestPaletteFormNavigationAndToggle(t *testing.T) {
	idx, spec := specByName(t, "loop")
	m := newTestModel(t, ViewDashboard, sampleStore())
	m.modal = ModalPalette
	m.openPalette()
	m.selectPaletteCommand(idx)
	m.modal = ModalPalette

	// The first loop flag is --once, a boolean.
	if spec.Flags[0].Kind != flagBool {
		t.Fatalf("expected --%s to be a boolean", spec.Flags[0].Name)
	}
	m.Update(key(" "))
	if !m.palette.bools[0] {
		t.Error("space should toggle the focused boolean flag")
	}

	// Tab past every field must wrap without going out of range.
	for range m.palette.fieldCount(spec) + 2 {
		m.Update(key("tab"))
	}
	if m.palette.focus < 0 || m.palette.focus >= m.palette.fieldCount(spec) {
		t.Errorf("focus %d out of range", m.palette.focus)
	}
}

func TestPaletteTextEntry(t *testing.T) {
	idx, spec := specByName(t, "craft")
	m := newTestModel(t, ViewDashboard, sampleStore())
	m.openPalette()
	m.selectPaletteCommand(idx)
	m.modal = ModalPalette

	langIdx := -1
	for i, f := range spec.Flags {
		if f.Name == "lang" {
			langIdx = i
		}
	}
	m.palette.focus = langIdx

	for _, r := range "go" {
		m.Update(key(string(r)))
	}
	if m.palette.values[langIdx] != "go" {
		t.Errorf("typed value = %q, want \"go\"", m.palette.values[langIdx])
	}
}

func TestPaletteEscReturnsToPicker(t *testing.T) {
	idx, _ := specByName(t, "swarm")
	m := newTestModel(t, ViewDashboard, sampleStore())
	m.openPalette()
	m.selectPaletteCommand(idx)
	m.modal = ModalPalette

	m.Update(key("esc"))
	if !m.palette.picking {
		t.Error("esc in the form should return to the command picker")
	}
	if m.modal != ModalPalette {
		t.Error("esc in the form should not close the palette")
	}

	m.Update(key("esc"))
	if m.modal != ModalNone {
		t.Error("esc in the picker should close the palette")
	}
}

func TestPaletteFlagsMatchCobraDefinitions(t *testing.T) {
	// Guards against the palette drifting from pkg/cmd. Each entry is the flag
	// set declared by the corresponding cobra command's init().
	want := map[string][]string{
		"loop":              {"once", "watch", "lang", "max-passes", "max-cycles", "idle", "escalate-to", "retry-blocked", "review"},
		"swarm":             {"spec", "lang", "mode", "review-depth", "no-format", "no-pr"},
		"spec-feature":      {"file", "provider", "interactive", "continue"},
		"loop status":       {"state", "json"},
		"loop run":          {"lang"},
		"loop retry-review": {},
		"loop retry-gate":   {},
		"gate":              {"lang", "no-format"},
		"task list":         {},
		"task done":         {"worktree-only"},
		"mission run":       {"mission", "input", "output"},
		"mission audit":     {"session", "decision", "status", "purge", "show-output"},
		"log":               {"all", "live", "session"},
		"init":              {},
		"install":           {},
	}
	for name, flags := range want {
		_, spec := specByName(t, name)
		if len(spec.Flags) != len(flags) {
			t.Errorf("%s: %d flags declared, want %d", name, len(spec.Flags), len(flags))
			continue
		}
		for i, f := range spec.Flags {
			if f.Name != flags[i] {
				t.Errorf("%s flag %d = %q, want %q", name, i, f.Name, flags[i])
			}
		}
	}
}

func TestPaletteModalFitsMinimumTerminal(t *testing.T) {
	m := newTestModel(t, ViewDashboard, sampleStore())
	m.openPalette()

	if got := strings.Count(m.renderPaletteModal(minWidth, minHeight), "\n") + 1; got > minHeight {
		t.Errorf("palette picker is %d rows, exceeds %d", got, minHeight)
	}

	// The picker scrolls, so the last command must stay reachable and on screen.
	last := len(paletteCommands) - 1
	m.palette.cursor = last
	out := m.renderPaletteModal(minWidth, minHeight)
	if got := strings.Count(out, "\n") + 1; got > minHeight {
		t.Errorf("palette picker scrolled to the end is %d rows, exceeds %d", got, minHeight)
	}
	if !strings.Contains(out, paletteCommands[last].Name) {
		t.Errorf("last command %q not rendered when selected", paletteCommands[last].Name)
	}

	// craft has the most flags, so it is the worst case for the form.
	idx, _ := specByName(t, "craft")
	m.selectPaletteCommand(idx)
	if got := strings.Count(m.renderPaletteModal(minWidth, minHeight), "\n") + 1; got > minHeight {
		t.Errorf("craft form is %d rows, exceeds %d", got, minHeight)
	}
}
