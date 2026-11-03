package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/futureboard-dev/colony/pkg/fix"
)

// flowPicker is the shared tool/model picker described in "Choosing a Tool
// and Model": every interactive flow shows the same component, with a
// preselected choice ready to accept on Enter (Open Questions §1 — the
// picker always shows, never only when nothing is saved). The blocked-task
// fix flow owns the first user of this type; later flows (e.g. 08's) build
// their own flowPicker from fix.InstalledTools/fix.ResolveChoice the same
// way and drive it with the same handleKey/view pair.
type flowPicker struct {
	tools   []fix.Tool
	index   int    // selected tool within tools
	model   string // "" means the tool's own default
	editing bool
	input   textinput.Model
	err     string // e.g. the tool's own rejection of the last model tried
}

// newFlowPicker builds a picker from the installed tools and a preselected
// choice (typically the result of fix.ResolveChoice).
func newFlowPicker(tools []fix.Tool, preselect fix.Choice) flowPicker {
	idx := 0
	for i, t := range tools {
		if t == preselect.Tool {
			idx = i
			break
		}
	}
	in := textinput.New()
	in.Placeholder = "model name"
	in.CharLimit = 100
	return flowPicker{tools: tools, index: idx, model: preselect.Model, input: in}
}

// choice reports the picker's current selection.
func (p flowPicker) choice() fix.Choice {
	if len(p.tools) == 0 {
		return fix.Choice{}
	}
	return fix.Choice{Tool: p.tools[p.index], Model: p.model}
}

// pickerResult reports what a keypress did to the picker.
type pickerResult int

const (
	pickerNone pickerResult = iota
	pickerConfirmed
	pickerCancelled
)

// handleKey routes one key to the picker. While editing the model field,
// every key goes to the text input until Enter commits it or Esc reverts
// without changing the model.
func (p *flowPicker) handleKey(key tea.KeyMsg) (pickerResult, tea.Cmd) {
	if p.editing {
		switch key.String() {
		case "enter":
			p.model = strings.TrimSpace(p.input.Value())
			p.editing = false
			p.input.Blur()
			return pickerNone, nil
		case "esc":
			p.editing = false
			p.input.Blur()
			return pickerNone, nil
		}
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(key)
		return pickerNone, cmd
	}

	switch key.String() {
	case "left", "h", "up", "k":
		if len(p.tools) > 0 {
			p.index = (p.index - 1 + len(p.tools)) % len(p.tools)
		}
		return pickerNone, nil
	case "right", "l", "down", "j", "tab":
		if len(p.tools) > 0 {
			p.index = (p.index + 1) % len(p.tools)
		}
		return pickerNone, nil
	case "m":
		if len(p.tools) == 0 {
			return pickerNone, nil
		}
		p.editing = true
		p.err = ""
		p.input.SetValue(p.model)
		p.input.CursorEnd()
		return pickerNone, p.input.Focus()
	case "enter":
		if len(p.tools) == 0 {
			return pickerNone, nil
		}
		return pickerConfirmed, nil
	case "esc":
		return pickerCancelled, nil
	}
	return pickerNone, nil
}

// view renders "Fix with: ● claude  ○ codex  ○ crush" and the model line.
func (p flowPicker) view(theme *Theme) string {
	var b strings.Builder
	if p.err != "" {
		b.WriteString(" " + theme.Error.Render(theme.icon(iconCrashed)+" "+p.err) + "\n\n")
	}
	if len(p.tools) == 0 {
		b.WriteString(" " + theme.Error.Render("No agent CLI found — install one of:") + "\n")
		b.WriteString(theme.Dim.Render("   claude  https://claude.ai/code") + "\n")
		b.WriteString(theme.Dim.Render("   codex   npm install -g @openai/codex") + "\n")
		b.WriteString(theme.Dim.Render("   crush   brew install charmbracelet/tap/crush") + "\n")
		return b.String()
	}

	b.WriteString(" " + theme.Dim.Render("Fix with:") + "  ")
	for i, t := range p.tools {
		mark := "○"
		style := theme.Dim
		if i == p.index {
			mark, style = "●", theme.Accent
		}
		b.WriteString(style.Render(theme.icon(mark)+" "+string(t)) + "   ")
	}
	b.WriteString("\n")

	model := p.model
	if model == "" {
		model = "default"
	}
	if p.editing {
		b.WriteString(" " + theme.Dim.Render("Model:") + " " + theme.FieldFocus.Render(" "+p.input.View()+" ") + "\n")
	} else {
		b.WriteString(" " + theme.Dim.Render("Model:") + " " + theme.Accent.Render(model) + "   " + theme.Dim.Render("[m] change") + "\n")
	}
	return b.String()
}

// label renders the always-visible "tool · model" summary line (e.g.
// "claude · default").
func (p flowPicker) label() string {
	c := p.choice()
	if c.Tool == "" {
		return ""
	}
	model := c.Model
	if model == "" {
		model = "default"
	}
	return string(c.Tool) + " · " + model
}
