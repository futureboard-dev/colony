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

//go:embed review_loop.md
var reviewLoopTmpl string

//go:embed spec_feature.md
var specFeatureTmpl string

//go:embed spec_feature_revise.md
var specFeatureReviseTmpl string

//go:embed spec_split.md
var specSplitTmpl string

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

// RenderLens renders a specific review lens prompt with the provided diff.
func RenderLens(name, diff string) (string, error) {
	tmplStr, err := LoadModulePrompt("review-" + name)
	if err != nil {
		return "", err
	}
	return render(tmplStr, map[string]any{"Diff": diff})
}

// RenderSynthesize renders the synthesizer prompt with the collected reports.
func RenderSynthesize(reports string) (string, error) {
	tmplStr, err := LoadModulePrompt("review-synthesize")
	if err != nil {
		return "", err
	}
	return render(tmplStr, map[string]any{"Reports": reports})
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

// ReviewLoop returns the loop's envelope-format review prompt. The spec and diff
// are injected under the prompt's # INPUT marker by the caller, so this returns
// the static template unchanged.
func ReviewLoop() string {
	return reviewLoopTmpl
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

// SpecFeatureRevise builds a prompt that asks the LLM to revise an existing
// TASK.md, incorporating any inline feedback comments left by the human.
func SpecFeatureRevise(existingSpec string) (string, error) {
	t, err := template.New("").Delims("[[", "]]").Parse(specFeatureReviseTmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, map[string]any{"Spec": existingSpec}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// SpecSplit builds a prompt that asks the LLM to split a large spec into a
// plan plus short per-part briefs that reference the spec rather than restate it.
// files is the repo's file list (one path per line, may be empty); the split
// has no file tools, so this is how it names real paths and free numbers.
func SpecSplit(spec, files string) (string, error) {
	t, err := template.New("").Delims("[[", "]]").Parse(specSplitTmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, map[string]any{"Spec": spec, "Files": files}); err != nil {
		return "", err
	}
	return buf.String(), nil
}
