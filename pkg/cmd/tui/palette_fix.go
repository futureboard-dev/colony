package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/futureboard-dev/colony/pkg/config"
	"github.com/futureboard-dev/colony/pkg/fix"
)

// openFixPicker shows the shared tool/model picker (06's flowPicker) before a
// palette command with a non-empty Flow actually runs — Rollout Phase 2's
// "add the shared picker to the other flows". The form's own validation has
// already passed by the time this is called (see palette_keys.go), so the
// only thing left to decide is which tool/model the command uses.
//
// This only runs inside the TUI, which always has a terminal attached, so it
// never needs to special-case "no terminal": non-interactive callers (e.g.
// scheduled loop runs) call fix.ResolveChoice directly, without going
// through the palette at all.
func (m *Model) openFixPicker(flow string) tea.Cmd {
	ctx := context.Background()
	tools, err := fixInstalledTools(ctx)
	if err != nil || len(tools) == 0 {
		// Nothing to choose between: run with whatever the form/config
		// already resolves to, same as before this flow existed.
		return m.runPaletteCommand()
	}

	root := or(m.opts.Root, ".")
	var preselect fix.Choice
	if st, err := config.LoadState(root); err == nil {
		if c, ok := st.ChoiceForFlow(flow); ok {
			preselect = fix.Choice{Tool: fix.Tool(c.Tool), Model: c.Model}
		}
	}
	choice, err := fixResolveChoice(ctx, fix.Request{Flow: flow, Choice: preselect})
	if err != nil {
		return m.runPaletteCommand()
	}

	picker := newFlowPicker(tools, choice)
	m.palette.fixPicker = &picker
	m.palette.fixFlow = flow
	return nil
}

// handlePaletteFixPickerKey routes a key into the shared picker. Confirming
// remembers the choice for next time (one write per flow) and fills the
// command's own --model flag when it has one; cancelling drops any one-off
// edit and runs with the plain config-resolved choice instead ("esc falls
// back to config" — nothing is written to state).
func (m *Model) handlePaletteFixPickerKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := &m.palette
	result, cmd := p.fixPicker.handleKey(key)
	switch result {
	case pickerConfirmed:
		choice := p.fixPicker.choice()
		flow := p.fixFlow
		p.applyFixChoice(choice)
		p.fixPicker = nil
		m.saveFixChoice(flow, choice)
		return m, m.runPaletteCommand()
	case pickerCancelled:
		p.fixPicker = nil
		return m, m.runPaletteCommand()
	}
	return m, cmd
}

// applyFixChoice fills the selected command's own --model flag from the
// confirmed picker choice, when it declares one (e.g. craft) and the user
// hasn't already typed a value.
func (p *paletteState) applyFixChoice(choice fix.Choice) {
	if choice.Model == "" {
		return
	}
	spec := paletteCommands[p.spec]
	for i, f := range spec.Flags {
		if f.Name == "model" && p.values[i] == "" {
			p.values[i] = choice.Model
			return
		}
	}
}

// saveFixChoice remembers the confirmed tool/model for this flow in Colony's
// local state, so next time the picker opens with it preselected.
func (m *Model) saveFixChoice(flow string, choice fix.Choice) {
	root := or(m.opts.Root, ".")
	st, err := config.LoadState(root)
	if err != nil {
		st = &config.State{}
	}
	st.SetChoiceForFlow(flow, config.FlowChoice{Tool: string(choice.Tool), Model: choice.Model})
	_ = config.SaveState(root, st)
}
