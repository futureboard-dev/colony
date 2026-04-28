package prompt

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"
)

//go:embed build.md
var buildTmpl string

//go:embed build_continue.md
var buildContinueTmpl string

//go:embed fix.md
var fixTmpl string

//go:embed coordinator.md
var coordinatorTmpl string

//go:embed scout.md
var scoutTmpl string

//go:embed review.md
var reviewTmpl string

//go:embed spec_feature.md
var specFeatureTmpl string

// ModulePrompts exposes the module-prompts directory as an embedded filesystem.
//
//go:embed module-prompts
var ModulePrompts embed.FS

// LoadModulePrompt reads a module prompt by role name from the embedded FS.
func LoadModulePrompt(role string) (string, error) {
	data, err := ModulePrompts.ReadFile("module-prompts/" + role + ".md")
	if err != nil {
		return "", fmt.Errorf("no module prompt for role %q", role)
	}
	return string(data), nil
}

func render(tmpl string, data any) (string, error) {
	t, err := template.New("").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func Build(lang string) (string, error) {
	return render(buildTmpl, map[string]any{"Lang": lang})
}

func BuildContinue(lang string) (string, error) {
	return render(buildContinueTmpl, map[string]any{"Lang": lang})
}

func Fix(gate, errors string) (string, error) {
	return render(fixTmpl, map[string]any{"Gate": gate, "Errors": errors})
}

func Coordinator(spec string) (string, error) {
	return render(coordinatorTmpl, map[string]any{"Spec": spec})
}

func Scout(spec string) (string, error) {
	return render(scoutTmpl, map[string]any{"Spec": spec})
}

func Review(spec, diff string) (string, error) {
	return render(reviewTmpl, map[string]any{"Spec": spec, "Diff": diff})
}

func SpecFeature(input string) (string, error) {
	t, err := template.New("").Delims("[[", "]]").Parse(specFeatureTmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, map[string]any{"Input": input}); err != nil {
		return "", err
	}
	return buf.String(), nil
}
