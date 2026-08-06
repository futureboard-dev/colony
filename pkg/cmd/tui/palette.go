package tui

import (
	"fmt"
	"strings"
)

// errRequiredFlag reports a required flag left empty in the palette form.
func errRequiredFlag(name string) error {
	return fmt.Errorf("--%s is required", name)
}

// flagKind distinguishes the two input shapes a flag can take.
type flagKind int

const (
	flagString flagKind = iota
	flagBool
)

// cmdFlag declares one flag of a runnable colony subcommand. Name is the flag
// name without dashes; Default seeds the field and doubles as the value that
// is omitted from argv when unchanged.
type cmdFlag struct {
	Name        string
	Kind        flagKind
	Default     string
	Placeholder string
	Required    bool
	Help        string
}

// cmdSpec declares a colony subcommand the palette can build and run.
type cmdSpec struct {
	Name       string   // display name and palette key
	Argv       []string // subcommand path, e.g. {"loop", "schedule", "start"}
	Summary    string
	Positional string // label for a leading positional arg, empty if none
	Flags      []cmdFlag
}

// paletteCommands mirrors the cobra definitions in pkg/cmd. Flag names and
// defaults must stay in sync with those files.
var paletteCommands = []cmdSpec{
	{
		Name:    "loop",
		Argv:    []string{"loop"},
		Summary: "Process the task queue",
		Flags: []cmdFlag{
			{Name: "once", Kind: flagBool, Help: "single pass then exit"},
			{Name: "watch", Kind: flagBool, Help: "long-lived daemon"},
			{Name: "lang", Kind: flagString, Default: "go", Placeholder: "go", Help: "language for gates"},
			{Name: "max-passes", Kind: flagString, Default: "0", Placeholder: "0", Help: "0 = unlimited"},
			{Name: "max-cycles", Kind: flagString, Default: "3", Placeholder: "3", Help: "inner fix loop cap"},
			{Name: "idle", Kind: flagString, Default: "10", Placeholder: "10", Help: "idle passes before stop"},
			{Name: "escalate-to", Kind: flagString, Placeholder: "model", Help: "escalation model"},
			{Name: "retry-blocked", Kind: flagBool, Help: "re-queue blocked tasks"},
			{Name: "review", Kind: flagBool, Help: "LLM review gate before done"},
		},
	},
	{
		Name:    "craft",
		Argv:    []string{"craft"},
		Summary: "Build one spec end to end",
		Flags: []cmdFlag{
			{Name: "spec", Kind: flagString, Placeholder: "path/to/TASK.md", Help: "spec markdown file"},
			{Name: "lang", Kind: flagString, Required: true, Placeholder: "typescript", Help: "typescript, python, go"},
			{Name: "base", Kind: flagString, Placeholder: "branch", Help: "must not be main/master"},
			{Name: "provider", Kind: flagString, Default: "deepseek", Placeholder: "deepseek", Help: "deepseek or anthropic"},
			{Name: "model", Kind: flagString, Placeholder: "model", Help: "override config model"},
			{Name: "resume", Kind: flagString, Placeholder: "worktree path", Help: "re-run gates only"},
			{Name: "continue", Kind: flagString, Placeholder: "worktree path", Help: "continue codegen then gates"},
			{Name: "headless", Kind: flagBool, Help: "run in background"},
			{Name: "no-format", Kind: flagBool, Help: "skip the format gate"},
			{Name: "interactive", Kind: flagBool, Help: "live agent session"},
			{Name: "no-pr", Kind: flagBool, Help: "skip push and PR creation"},
		},
	},
	{
		Name:    "swarm",
		Argv:    []string{"swarm"},
		Summary: "Fan a spec out across subtask agents",
		Flags: []cmdFlag{
			{Name: "spec", Kind: flagString, Placeholder: "path/to/TASK.md", Help: "spec markdown file"},
			{Name: "lang", Kind: flagString, Required: true, Placeholder: "typescript", Help: "typescript, python, go"},
			{Name: "mode", Kind: flagString, Default: "standard", Placeholder: "standard", Help: "quick, standard, full"},
			{Name: "review-depth", Kind: flagString, Default: "deep", Placeholder: "deep", Help: "fast or deep"},
			{Name: "no-format", Kind: flagBool, Help: "skip the format gate"},
			{Name: "no-pr", Kind: flagBool, Help: "skip push and PR creation"},
		},
	},
	{
		Name:       "spec-feature",
		Argv:       []string{"spec-feature"},
		Summary:    "Generate a TASK.md spec",
		Positional: "feature-name",
		Flags: []cmdFlag{
			{Name: "file", Kind: flagString, Placeholder: "requirements.md", Help: "read requirements from file"},
			{Name: "provider", Kind: flagString, Default: "deepseek", Placeholder: "deepseek", Help: "deepseek or anthropic"},
			{Name: "interactive", Kind: flagBool, Help: "collaborate in a live session"},
			{Name: "continue", Kind: flagBool, Help: "revise existing TASK.md"},
		},
	},
	{
		Name:    "loop schedule start",
		Argv:    []string{"loop", "schedule", "start"},
		Summary: "Install a cron/launchd schedule",
		Flags: []cmdFlag{
			{Name: "every", Kind: flagString, Default: "10m", Placeholder: "10m", Help: "interval, minimum 1m"},
		},
	},
	{
		Name:    "loop schedule stop",
		Argv:    []string{"loop", "schedule", "stop"},
		Summary: "Remove the installed schedule",
	},
	{
		Name:    "loop clear",
		Argv:    []string{"loop", "clear"},
		Summary: "Remove tasks from the queue",
		Flags: []cmdFlag{
			{Name: "state", Kind: flagString, Placeholder: "open", Help: "remove all tasks in a state"},
			{Name: "all", Kind: flagBool, Help: "remove every task"},
			{Name: "yes", Kind: flagBool, Default: "true", Help: "skip the confirmation prompt"},
		},
	},
}

// paletteState holds the palette's selection and in-progress form.
type paletteState struct {
	// picking is true while choosing a command, false while filling its form.
	picking  bool
	cursor   int
	spec     int    // index into paletteCommands once picking is done
	posValue string // positional argument value
	values   []string
	bools    []bool
	focus    int
	scroll   int
	err      string
}

// openPalette resets the palette to the command chooser.
func (m *Model) openPalette() {
	m.modal = ModalPalette
	m.palette = paletteState{picking: true}
}

// selectPaletteCommand moves from the chooser into the selected command's form.
func (m *Model) selectPaletteCommand(idx int) {
	if idx < 0 || idx >= len(paletteCommands) {
		return
	}
	spec := paletteCommands[idx]
	p := paletteState{picking: false, spec: idx}
	p.values = make([]string, len(spec.Flags))
	p.bools = make([]bool, len(spec.Flags))
	for i, f := range spec.Flags {
		if f.Kind == flagBool {
			p.bools[i] = f.Default == "true"
			continue
		}
		p.values[i] = f.Default
	}
	m.palette = p
}

// paletteFieldCount counts the focusable rows in the form: the optional
// positional argument plus every flag.
func (p *paletteState) fieldCount(spec cmdSpec) int {
	n := len(spec.Flags)
	if spec.Positional != "" {
		n++
	}
	return n
}

// flagIndex maps a focus row to a flag index, reporting false for the
// positional row.
func (p *paletteState) flagIndex(spec cmdSpec, focus int) (int, bool) {
	if spec.Positional == "" {
		return focus, focus < len(spec.Flags)
	}
	if focus == 0 {
		return 0, false
	}
	return focus - 1, focus-1 < len(spec.Flags)
}

// buildArgv assembles the command line, omitting flags left at their default.
// It returns an error when a required flag is empty.
func (p *paletteState) buildArgv(spec cmdSpec) ([]string, error) {
	argv := append([]string{}, spec.Argv...)
	if spec.Positional != "" {
		if v := strings.TrimSpace(p.posValue); v != "" {
			argv = append(argv, v)
		}
	}
	for i, f := range spec.Flags {
		switch f.Kind {
		case flagBool:
			if p.bools[i] != (f.Default == "true") {
				if p.bools[i] {
					argv = append(argv, "--"+f.Name)
				} else {
					argv = append(argv, "--"+f.Name+"=false")
				}
			}
		default:
			v := strings.TrimSpace(p.values[i])
			if v == "" && f.Required {
				return nil, errRequiredFlag(f.Name)
			}
			if v != "" && v != f.Default {
				argv = append(argv, "--"+f.Name, v)
			}
		}
	}
	return argv, nil
}

// commandLine renders the argv preview shown under the form.
func (p *paletteState) commandLine(spec cmdSpec) string {
	argv, err := p.buildArgv(spec)
	if err != nil {
		// Preview the command as typed; the error surfaces on submit.
		argv = append([]string{}, spec.Argv...)
	}
	return "colony " + strings.Join(argv, " ")
}
