package tui

import (
	"context"
	"testing"

	"github.com/futureboard-dev/colony/pkg/config"
	"github.com/futureboard-dev/colony/pkg/fix"
)

// stubFixPicker swaps the package-level fix.* vars (shared with block_flow.go)
// for deterministic tools/resolution, the same pattern block_flow_test.go
// uses to avoid depending on the test machine's PATH.
func stubFixPicker(t *testing.T, tools []fix.Tool) {
	t.Helper()
	origTools, origResolve := fixInstalledTools, fixResolveChoice
	fixInstalledTools = func(ctx context.Context) ([]fix.Tool, error) { return tools, nil }
	fixResolveChoice = func(ctx context.Context, req fix.Request) (fix.Choice, error) {
		if req.Choice.Tool != "" {
			return req.Choice, nil
		}
		if len(tools) == 0 {
			return fix.Choice{}, fix.ErrNotConfigured
		}
		return fix.Choice{Tool: tools[0]}, nil
	}
	t.Cleanup(func() {
		fixInstalledTools, fixResolveChoice = origTools, origResolve
	})
}

// fillRequired fills in whatever a command needs to pass buildArgv, so
// pressing enter reaches the fix picker instead of an inline form error.
func fillRequired(m *Model, spec cmdSpec) {
	for i, f := range spec.Flags {
		if f.Name == "lang" {
			m.palette.values[i] = "go"
		}
	}
	if spec.Positional != "" && m.palette.posValue == "" {
		m.palette.posValue = "t-1"
	}
}

func TestPaletteFixPickerOpensWithSavedChoicePreselected(t *testing.T) {
	stubFixPicker(t, []fix.Tool{fix.ToolClaude, fix.ToolCodex})

	for _, tc := range []struct{ command, flow string }{
		{"craft", "craft"},
		{"loop retry-review", "review"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			root := t.TempDir()
			if err := config.SaveState(root, &config.State{Flows: map[string]config.FlowChoice{
				tc.flow: {Tool: "codex", Model: "my-model"},
			}}); err != nil {
				t.Fatal(err)
			}

			idx, spec := specByName(t, tc.command)
			m := newTestModel(t, ViewDashboard, sampleStore())
			m.opts.Root = root
			m.openPalette()
			m.selectPaletteCommand(idx)
			fillRequired(m, spec)
			m.modal = ModalPalette

			m.Update(key("enter"))

			if m.palette.fixPicker == nil {
				t.Fatal("expected the picker to open")
			}
			got := m.palette.fixPicker.choice()
			if got.Tool != fix.ToolCodex || got.Model != "my-model" {
				t.Errorf("expected the saved choice preselected, got %+v", got)
			}
		})
	}
}

func TestPaletteFixPickerEscFallsBackToConfig(t *testing.T) {
	stubFixPicker(t, []fix.Tool{fix.ToolClaude, fix.ToolCodex})
	root := t.TempDir()

	idx, spec := specByName(t, "craft")
	m := newTestModel(t, ViewDashboard, sampleStore())
	m.opts.Root = root
	m.openPalette()
	m.selectPaletteCommand(idx)
	fillRequired(m, spec)
	m.modal = ModalPalette

	m.Update(key("enter"))
	if m.palette.fixPicker == nil {
		t.Fatal("expected the picker to open")
	}
	// Switch the tool before cancelling, to prove the edit is discarded.
	m.palette.fixPicker.handleKey(key("right"))

	m.Update(key("esc"))

	if m.palette.fixPicker != nil {
		t.Error("esc should close the picker")
	}
	st, err := config.LoadState(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.ChoiceForFlow("craft"); ok {
		t.Error("esc must not persist a choice — it should fall back to config")
	}
}

func TestPaletteFixPickerConfirmWritesOneChoicePerFlow(t *testing.T) {
	stubFixPicker(t, []fix.Tool{fix.ToolClaude, fix.ToolCodex})
	root := t.TempDir()

	idx, spec := specByName(t, "craft")
	m := newTestModel(t, ViewDashboard, sampleStore())
	m.opts.Root = root
	m.openPalette()
	m.selectPaletteCommand(idx)
	fillRequired(m, spec)
	m.modal = ModalPalette

	m.Update(key("enter"))
	if m.palette.fixPicker == nil {
		t.Fatal("expected the picker to open")
	}
	m.palette.fixPicker.handleKey(key("right")) // move selection to codex

	m.Update(key("enter"))

	if m.palette.fixPicker != nil {
		t.Error("confirming should close the picker")
	}
	st, err := config.LoadState(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Flows) != 1 {
		t.Fatalf("expected exactly one flow recorded, got %d: %+v", len(st.Flows), st.Flows)
	}
	c, ok := st.ChoiceForFlow("craft")
	if !ok || c.Tool != string(fix.ToolCodex) {
		t.Errorf("expected craft's choice saved as codex, got %+v ok=%v", c, ok)
	}

	// Confirming again for the same flow must still leave exactly one entry.
	m.palette.fixPicker = nil
	m.openPalette()
	m.selectPaletteCommand(idx)
	fillRequired(m, spec)
	m.modal = ModalPalette
	m.Update(key("enter"))
	m.Update(key("enter"))

	st2, err := config.LoadState(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(st2.Flows) != 1 {
		t.Errorf("expected one choice per flow, got %d entries: %+v", len(st2.Flows), st2.Flows)
	}
}

func TestPaletteFixPickerSkipsWhenNoToolsInstalled(t *testing.T) {
	stubFixPicker(t, nil)
	root := t.TempDir()

	idx, spec := specByName(t, "craft")
	m := newTestModel(t, ViewDashboard, sampleStore())
	m.opts.Root = root
	m.openPalette()
	m.selectPaletteCommand(idx)
	fillRequired(m, spec)
	m.modal = ModalPalette

	m.Update(key("enter"))

	if m.palette.fixPicker != nil {
		t.Error("the picker should not open when no tool is installed")
	}
}

func TestPaletteUnaffectedFlowSkipsFixPicker(t *testing.T) {
	// "loop" declares no Flow, so enter must behave exactly as before.
	stubFixPicker(t, []fix.Tool{fix.ToolClaude})
	idx, _ := specByName(t, "loop")
	m := newTestModel(t, ViewDashboard, sampleStore())
	m.openPalette()
	m.selectPaletteCommand(idx)
	m.modal = ModalPalette

	m.Update(key("enter"))

	if m.palette.fixPicker != nil {
		t.Error("a command with no Flow should never show the fix picker")
	}
}
