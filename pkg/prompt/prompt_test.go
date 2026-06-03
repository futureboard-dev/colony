package prompt

import (
	"strings"
	"testing"
)

func TestBuildContainsLang(t *testing.T) {
	p, err := Build("go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, "go") {
		t.Error("Build prompt should contain the language")
	}
	if !strings.Contains(p, "SPEC.md") {
		t.Error("Build prompt should reference SPEC.md")
	}
}

func TestBuildContinueContainsLang(t *testing.T) {
	p, err := BuildContinue("typescript")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, "typescript") {
		t.Error("BuildContinue prompt should contain the language")
	}
	if !strings.Contains(p, "interrupted") {
		t.Error("BuildContinue prompt should mention interruption")
	}
}

func TestFixContainsGateAndErrors(t *testing.T) {
	p, err := Fix("Type check", "cannot use string as int")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, "Type check") {
		t.Error("Fix prompt should contain gate name")
	}
	if !strings.Contains(p, "cannot use string as int") {
		t.Error("Fix prompt should contain error output")
	}
}

func TestCoordinatorContainsSpec(t *testing.T) {
	spec := "Add a promotional banner to the homepage"
	p, err := Coordinator(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, spec) {
		t.Error("Coordinator prompt should embed the spec")
	}
	if !strings.Contains(p, "SUBTASK") {
		t.Error("Coordinator prompt should instruct on SUBTASK format")
	}
}

func TestScoutContainsSpec(t *testing.T) {
	spec := "Implement search endpoint returning paginated results"
	p, err := Scout(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, spec) {
		t.Error("Scout prompt should embed the spec")
	}
}

func TestReviewContainsSpecAndDiff(t *testing.T) {
	spec := "Add user profile page"
	diff := "+func Profile() { ... }"
	p, err := Review(spec, diff)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, spec) {
		t.Error("Review prompt should embed the spec")
	}
	if !strings.Contains(p, diff) {
		t.Error("Review prompt should embed the diff")
	}
	if !strings.Contains(p, "APPROVED") {
		t.Error("Review prompt should mention APPROVED")
	}
	if !strings.Contains(p, "REJECTED") {
		t.Error("Review prompt should mention REJECTED")
	}
}

func TestSpecFeatureContainsInput(t *testing.T) {
	input := "users log in with email and password"
	p, err := SpecFeature(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, input) {
		t.Error("SpecFeature prompt should embed the requirements input")
	}
	if !strings.Contains(p, "Agent Task Spec") {
		t.Error("SpecFeature prompt should reference Agent Task Spec format")
	}
}

func TestSpecFeatureReviseContainsSpec(t *testing.T) {
	existing := "# Agent Task Spec\n\n## 1. Add login\n\n<!-- change to OAuth -->\n\nSome content."
	p, err := SpecFeatureRevise(existing)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, existing) {
		t.Error("SpecFeatureRevise prompt should embed the existing spec")
	}
	if !strings.Contains(p, "feedback comments") {
		t.Error("SpecFeatureRevise prompt should instruct on incorporating comments")
	}
}
