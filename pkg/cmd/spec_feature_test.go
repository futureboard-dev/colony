package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/futureboard-dev/colony/pkg/llm"
	"github.com/spf13/cobra"
)

func TestSpecHash(t *testing.T) {
	t.Run("same content produces same hash", func(t *testing.T) {
		data := []byte("# Agent Task Spec\n\nsome content")
		h1 := specHash(data)
		h2 := specHash([]byte("# Agent Task Spec\n\nsome content"))
		if h1 != h2 {
			t.Error("hash should be deterministic")
		}
	})

	t.Run("different content produces different hash", func(t *testing.T) {
		a := specHash([]byte("content A"))
		b := specHash([]byte("content B"))
		if a == b {
			t.Error("different content should produce different hashes")
		}
	})

	t.Run("hash is hex string", func(t *testing.T) {
		h := specHash([]byte("test"))
		if len(h) != 64 {
			t.Errorf("expected 64-char hex SHA-256, got %d chars", len(h))
		}
		for _, c := range h {
			if !strings.ContainsRune("0123456789abcdef", c) {
				t.Errorf("hash contains non-hex character: %q", c)
			}
		}
	})
}

func TestSpecFeatureArgsAccepted(t *testing.T) {
	t.Run("one arg (feature name only) is valid", func(t *testing.T) {
		if err := specFeatureCmd.Args(specFeatureCmd, []string{"fix-upload-error"}); err != nil {
			t.Errorf("expected 1 arg to be accepted, got error: %v", err)
		}
	})

	t.Run("two args (feature name + inline requirements) is valid", func(t *testing.T) {
		if err := specFeatureCmd.Args(specFeatureCmd, []string{"fix-upload-error", "fix ECONNRESET on upload"}); err != nil {
			t.Errorf("expected 2 args to be accepted, got error: %v", err)
		}
	})

	t.Run("three args is rejected", func(t *testing.T) {
		if err := specFeatureCmd.Args(specFeatureCmd, []string{"a", "b", "c"}); err == nil {
			t.Error("expected 3 args to be rejected")
		}
	})
}

func fourPartFixture() string {
	return `## PART 01: models
Depends-On: none
# Agent Task Spec

## 1. Models

Define the shared types.

## PART 02: api
Depends-On: 01
# Agent Task Spec

## 1. API

Build the HTTP handlers.

## PART 03: ui
Depends-On: 01
# Agent Task Spec

## 1. UI

Build the frontend.

## PART 04: docs
Depends-On: 02, 03
# Agent Task Spec

## 1. Docs

Write the README section.
`
}

func TestParseParts(t *testing.T) {
	t.Run("valid 4-part fixture", func(t *testing.T) {
		parts, err := parseParts(fourPartFixture())
		if err != nil {
			t.Fatalf("parseParts: %v", err)
		}
		if len(parts) != 4 {
			t.Fatalf("expected 4 parts, got %d", len(parts))
		}
		wantIDs := []string{"01-models", "02-api", "03-ui", "04-docs"}
		for i, want := range wantIDs {
			if parts[i].ID != want {
				t.Errorf("part %d: expected ID %q, got %q", i, want, parts[i].ID)
			}
		}
		if len(parts[0].DependsOn) != 0 {
			t.Errorf("expected part 01 to have no deps, got %v", parts[0].DependsOn)
		}
		if len(parts[1].DependsOn) != 1 || parts[1].DependsOn[0] != "01-models" {
			t.Errorf("expected part 02 to depend on 01-models, got %v", parts[1].DependsOn)
		}
		if len(parts[3].DependsOn) != 2 || parts[3].DependsOn[0] != "02-api" || parts[3].DependsOn[1] != "03-ui" {
			t.Errorf("expected part 04 to depend on 02-api and 03-ui, got %v", parts[3].DependsOn)
		}
		for _, p := range parts {
			if !strings.HasPrefix(p.Body, "# Agent Task Spec") {
				t.Errorf("part %s body should start with '# Agent Task Spec', got %q", p.ID, p.Body[:minInt(len(p.Body), 30)])
			}
		}
	})

	t.Run("single part", func(t *testing.T) {
		fixture := "## PART 01: everything\nDepends-On: none\n# Agent Task Spec\n\nBody text.\n"
		parts, err := parseParts(fixture)
		if err != nil {
			t.Fatalf("parseParts: %v", err)
		}
		if len(parts) != 1 || parts[0].ID != "01-everything" {
			t.Fatalf("expected single part 01-everything, got %+v", parts)
		}
	})

	t.Run("Depends-On none", func(t *testing.T) {
		fixture := "## PART 01: foo\nDepends-On: none\n# Agent Task Spec\n\nBody.\n"
		parts, err := parseParts(fixture)
		if err != nil {
			t.Fatalf("parseParts: %v", err)
		}
		if len(parts[0].DependsOn) != 0 {
			t.Errorf("expected no deps, got %v", parts[0].DependsOn)
		}
	})

	t.Run("owns, reserves and plan", func(t *testing.T) {
		fixture := "## PLAN\n- Reserved: Script0040 → 01\n\n## PART 00: contracts\nDepends-On: none\nOwns: src/Program.cs, src/Ports/\nReserves: none\nDefine the ports.\n\n## PART 01: signin\nDepends-On: 00\nOwns: src/Auth/\nReserves: Script0040\nImplement §5.3.\n"
		parts, err := parseParts(fixture)
		if err != nil {
			t.Fatalf("parseParts: %v", err)
		}
		if len(parts) != 2 || parts[0].ID != "00-contracts" || parts[1].DependsOn[0] != "00-contracts" {
			t.Fatalf("unexpected parts: %+v", parts)
		}
		if strings.Join(parts[0].Owns, "|") != "src/Program.cs|src/Ports/" || len(parts[0].Reserves) != 0 {
			t.Errorf("part 00 owns=%v reserves=%v", parts[0].Owns, parts[0].Reserves)
		}
		if len(parts[1].Reserves) != 1 || parts[1].Reserves[0] != "Script0040" {
			t.Errorf("part 01 reserves=%v", parts[1].Reserves)
		}
		if parts[1].Body != "Implement §5.3." {
			t.Errorf("body should exclude header lines, got %q", parts[1].Body)
		}
		if plan := parsePlan(fixture); plan != "- Reserved: Script0040 → 01" {
			t.Errorf("parsePlan = %q", plan)
		}
	})

	t.Run("no plan section", func(t *testing.T) {
		if plan := parsePlan(fourPartFixture()); plan != "" {
			t.Errorf("expected empty plan, got %q", plan)
		}
	})

	t.Run("malformed header returns error", func(t *testing.T) {
		fixture := "## PART one: foo\nDepends-On: none\nBody.\n"
		if _, err := parseParts(fixture); err == nil {
			t.Fatal("expected an error for a malformed part header")
		}
	})

	t.Run("no parts returns error", func(t *testing.T) {
		if _, err := parseParts("just some text, no parts here"); err == nil {
			t.Fatal("expected an error when no parts are found")
		}
	})
}

func TestValidateParts(t *testing.T) {
	t.Run("duplicate id", func(t *testing.T) {
		parts := []specPart{
			{ID: "01-models", Body: "x"},
			{ID: "01-models", Body: "y"},
		}
		if _, err := validateParts(parts); err == nil {
			t.Fatal("expected an error for duplicate part ids")
		}
	})

	t.Run("missing dependency", func(t *testing.T) {
		parts := []specPart{
			{ID: "01-models", Body: "x"},
			{ID: "02-api", DependsOn: []string{"99-missing"}, Body: "y"},
		}
		if _, err := validateParts(parts); err == nil {
			t.Fatal("expected an error for a dependency on an unknown part")
		}
	})

	t.Run("cycle", func(t *testing.T) {
		parts := []specPart{
			{ID: "01-a", DependsOn: []string{"02-b"}, Body: "x"},
			{ID: "02-b", DependsOn: []string{"01-a"}, Body: "y"},
		}
		if _, err := validateParts(parts); err == nil {
			t.Fatal("expected an error for a dependency cycle")
		}
	})

	t.Run("two owners of a file warns but passes", func(t *testing.T) {
		parts := []specPart{
			{ID: "01-a", Owns: []string{"pkg/shared.go"}, Body: "x"},
			{ID: "02-b", Owns: []string{"pkg/shared.go"}, Body: "y"},
		}
		warnings, err := validateParts(parts)
		if err != nil {
			t.Fatalf("expected overlap to be a warning, not an error: %v", err)
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], "pkg/shared.go") {
			t.Errorf("expected one warning naming the file, got %v", warnings)
		}
	})

	t.Run("two parts reserving a number warns", func(t *testing.T) {
		parts := []specPart{
			{ID: "01-a", Reserves: []string{"Script0040"}, Body: "x"},
			{ID: "02-b", Reserves: []string{"Script0040"}, Body: "y"},
		}
		warnings, err := validateParts(parts)
		if err != nil {
			t.Fatal(err)
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], "Script0040") {
			t.Errorf("expected one warning naming the reservation, got %v", warnings)
		}
	})

	t.Run("valid graph topo order", func(t *testing.T) {
		parts := []specPart{
			{ID: "01-models", Body: "x"},
			{ID: "02-api", DependsOn: []string{"01-models"}, Body: "y"},
			{ID: "03-ui", DependsOn: []string{"01-models"}, Body: "z"},
			{ID: "04-docs", DependsOn: []string{"02-api", "03-ui"}, Body: "w"},
		}
		warnings, err := validateParts(parts)
		if err != nil {
			t.Fatalf("expected a valid graph, got error: %v", err)
		}
		if len(warnings) != 0 {
			t.Errorf("expected no warnings, got %v", warnings)
		}
		order, err := topoSortParts(parts)
		if err != nil {
			t.Fatalf("topoSortParts: %v", err)
		}
		pos := make(map[string]int, len(order))
		for i, id := range order {
			pos[id] = i
		}
		if pos["01-models"] >= pos["02-api"] || pos["01-models"] >= pos["03-ui"] {
			t.Errorf("expected 01-models before its dependents, order=%v", order)
		}
		if pos["02-api"] >= pos["04-docs"] || pos["03-ui"] >= pos["04-docs"] {
			t.Errorf("expected 04-docs after its dependencies, order=%v", order)
		}
	})
}

func TestPartWaves(t *testing.T) {
	parts, err := parseParts(fourPartFixture())
	if err != nil {
		t.Fatal(err)
	}

	t.Run("groups parts by earliest start", func(t *testing.T) {
		var got []string
		for _, w := range partWaves(parts) {
			ids := make([]string, len(w))
			for i, p := range w {
				ids[i] = p.ID
			}
			got = append(got, strings.Join(ids, ","))
		}
		want := []string{"01-models", "02-api,03-ui", "04-docs"}
		if strings.Join(got, " | ") != strings.Join(want, " | ") {
			t.Errorf("waves = %v, want %v", got, want)
		}
	})

	t.Run("critical path follows the longest chain", func(t *testing.T) {
		if got := strings.Join(criticalPath(parts), " → "); got != "01-models → 02-api → 04-docs" {
			t.Errorf("critical path = %q", got)
		}
	})

	t.Run("parallel roots give a one-part path", func(t *testing.T) {
		flat := []specPart{{ID: "01-a"}, {ID: "02-b"}}
		if waves := partWaves(flat); len(waves) != 1 || len(waves[0]) != 2 {
			t.Errorf("expected both parts in wave 1, got %v", waves)
		}
		if got := criticalPath(flat); len(got) != 1 {
			t.Errorf("expected a single-part critical path, got %v", got)
		}
	})
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// stubSpecFeatureLLM overrides specFeatureLLM so tests never shell out to a
// live agent CLI. Prompts are distinguished by markers unique to their
// template: the split prompt always contains the literal "Depends-On"
// instruction text; anything else is treated as a generate/revise prompt.
func stubSpecFeatureLLM(t *testing.T, taskContent, splitOutput string) {
	t.Helper()
	orig := specFeatureLLM
	specFeatureLLM = func(cmd *cobra.Command, ex *llm.Executor, root, prompt string, out io.Writer) error {
		if strings.Contains(prompt, "Depends-On") {
			_, err := io.WriteString(out, splitOutput)
			return err
		}
		_, err := io.WriteString(out, taskContent)
		return err
	}
	t.Cleanup(func() { specFeatureLLM = orig })
}

// captureStdout redirects os.Stdout for the duration of fn and returns what
// was written. Needed because the spec-feature split output goes straight to
// os.Stdout via fmt.Printf, not through the cobra command's writer.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fnErr := fn()
	w.Close()
	os.Stdout = orig
	data, _ := io.ReadAll(r)
	return string(data), fnErr
}

func resetSpecFeatureFlags() {
	sfFile, sfInteractive, sfContinue, sfSplit, sfForce = "", false, false, false, false
	sfProvider = "deepseek"
}

func TestSpecFeatureSplit(t *testing.T) {
	splitFixture := fourPartFixture()

	t.Run("existing TASK.md splits only", func(t *testing.T) {
		dir := initTestRepo(t)
		setupMinimalProject(t, dir)
		origWd, _ := os.Getwd()
		os.Chdir(dir)
		defer os.Chdir(origWd)

		featureDir := filepath.Join(dir, ".colony", "specs", "payments")
		if err := os.MkdirAll(featureDir, 0755); err != nil {
			t.Fatal(err)
		}
		taskFile := filepath.Join(featureDir, "TASK.md")
		original := "# Feature: payments\n\n# Agent Task Spec\n\nOriginal content.\n"
		if err := os.WriteFile(taskFile, []byte(original), 0644); err != nil {
			t.Fatal(err)
		}

		stubSpecFeatureLLM(t, "should not be used", splitFixture)
		resetSpecFeatureFlags()
		sfSplit = true

		cmd := &cobra.Command{}
		if err := runSpecFeature(cmd, []string{"payments"}); err != nil {
			t.Fatalf("runSpecFeature: %v", err)
		}

		after, err := os.ReadFile(taskFile)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != original {
			t.Error("expected TASK.md to remain unchanged when it already exists")
		}

		entries, err := os.ReadDir(filepath.Join(featureDir, "parts"))
		if err != nil {
			t.Fatalf("read parts dir: %v", err)
		}
		if len(entries) != 4 {
			t.Fatalf("expected 4 part files, got %d", len(entries))
		}
	})

	t.Run("no TASK.md generates then splits", func(t *testing.T) {
		dir := initTestRepo(t)
		setupMinimalProject(t, dir)
		origWd, _ := os.Getwd()
		os.Chdir(dir)
		defer os.Chdir(origWd)

		stubSpecFeatureLLM(t, "# Feature: payments\n\n# Agent Task Spec\n\nGenerated content.\n", splitFixture)
		resetSpecFeatureFlags()
		sfSplit = true

		cmd := &cobra.Command{}
		if err := runSpecFeature(cmd, []string{"payments", "handle payments"}); err != nil {
			t.Fatalf("runSpecFeature: %v", err)
		}

		featureDir := filepath.Join(dir, ".colony", "specs", "payments")
		if _, err := os.Stat(filepath.Join(featureDir, "TASK.md")); err != nil {
			t.Errorf("expected TASK.md to be generated: %v", err)
		}
		entries, err := os.ReadDir(filepath.Join(featureDir, "parts"))
		if err != nil || len(entries) != 4 {
			t.Fatalf("expected 4 part files, err=%v entries=%d", err, len(entries))
		}
	})

	t.Run("no TASK.md and no requirements errors", func(t *testing.T) {
		dir := initTestRepo(t)
		setupMinimalProject(t, dir)
		origWd, _ := os.Getwd()
		os.Chdir(dir)
		defer os.Chdir(origWd)

		stubSpecFeatureLLM(t, "unused", "unused")
		resetSpecFeatureFlags()
		sfSplit = true

		cmd := &cobra.Command{}
		err := runSpecFeature(cmd, []string{"payments"})
		if err == nil || !strings.Contains(err.Error(), "requirements are empty") {
			t.Fatalf("expected the same requirements-empty error as plain spec-feature, got %v", err)
		}
	})

	t.Run("existing parts dir refused", func(t *testing.T) {
		dir := initTestRepo(t)
		setupMinimalProject(t, dir)
		origWd, _ := os.Getwd()
		os.Chdir(dir)
		defer os.Chdir(origWd)

		featureDir := filepath.Join(dir, ".colony", "specs", "payments")
		if err := os.MkdirAll(filepath.Join(featureDir, "parts"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(featureDir, "TASK.md"), []byte("# Agent Task Spec\n\nx"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(featureDir, "parts", "stale.md"), []byte("stale"), 0644); err != nil {
			t.Fatal(err)
		}

		stubSpecFeatureLLM(t, "unused", splitFixture)
		resetSpecFeatureFlags()
		sfSplit = true

		cmd := &cobra.Command{}
		err := runSpecFeature(cmd, []string{"payments"})
		if err == nil || !strings.Contains(err.Error(), "parts directory already exists") {
			t.Fatalf("expected a parts-directory-exists error, got %v", err)
		}
	})

	t.Run("force overwrites", func(t *testing.T) {
		dir := initTestRepo(t)
		setupMinimalProject(t, dir)
		origWd, _ := os.Getwd()
		os.Chdir(dir)
		defer os.Chdir(origWd)

		featureDir := filepath.Join(dir, ".colony", "specs", "payments")
		if err := os.MkdirAll(filepath.Join(featureDir, "parts"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(featureDir, "TASK.md"), []byte("# Agent Task Spec\n\nx"), 0644); err != nil {
			t.Fatal(err)
		}
		stalePath := filepath.Join(featureDir, "parts", "stale.md")
		if err := os.WriteFile(stalePath, []byte("stale"), 0644); err != nil {
			t.Fatal(err)
		}

		stubSpecFeatureLLM(t, "unused", splitFixture)
		resetSpecFeatureFlags()
		sfSplit = true
		sfForce = true

		cmd := &cobra.Command{}
		if err := runSpecFeature(cmd, []string{"payments"}); err != nil {
			t.Fatalf("runSpecFeature with --force: %v", err)
		}
		if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
			t.Error("expected --force to clear the stale parts directory")
		}
		entries, err := os.ReadDir(filepath.Join(featureDir, "parts"))
		if err != nil || len(entries) != 4 {
			t.Fatalf("expected 4 part files after --force, err=%v entries=%d", err, len(entries))
		}
	})

	t.Run("frontmatter written", func(t *testing.T) {
		dir := initTestRepo(t)
		setupMinimalProject(t, dir)
		origWd, _ := os.Getwd()
		os.Chdir(dir)
		defer os.Chdir(origWd)

		featureDir := filepath.Join(dir, ".colony", "specs", "payments")
		if err := os.MkdirAll(featureDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(featureDir, "TASK.md"), []byte("# Agent Task Spec\n\nx"), 0644); err != nil {
			t.Fatal(err)
		}

		stubSpecFeatureLLM(t, "unused", splitFixture)
		resetSpecFeatureFlags()
		sfSplit = true

		cmd := &cobra.Command{}
		if err := runSpecFeature(cmd, []string{"payments"}); err != nil {
			t.Fatalf("runSpecFeature: %v", err)
		}

		data, err := os.ReadFile(filepath.Join(featureDir, "parts", "02-api.md"))
		if err != nil {
			t.Fatalf("read part file: %v", err)
		}
		content := string(data)
		if !strings.HasPrefix(content, "---\nid: 02-api\ndepends_on: [01-models]\nsource: ../TASK.md\n---\n") {
			t.Errorf("unexpected frontmatter, got:\n%s", content)
		}
	})

	t.Run("tree printed", func(t *testing.T) {
		dir := initTestRepo(t)
		setupMinimalProject(t, dir)
		origWd, _ := os.Getwd()
		os.Chdir(dir)
		defer os.Chdir(origWd)

		featureDir := filepath.Join(dir, ".colony", "specs", "payments")
		if err := os.MkdirAll(featureDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(featureDir, "TASK.md"), []byte("# Agent Task Spec\n\nx"), 0644); err != nil {
			t.Fatal(err)
		}

		stubSpecFeatureLLM(t, "unused", splitFixture)
		resetSpecFeatureFlags()
		sfSplit = true

		cmd := &cobra.Command{}
		out, err := captureStdout(t, func() error { return runSpecFeature(cmd, []string{"payments"}) })
		if err != nil {
			t.Fatalf("runSpecFeature: %v", err)
		}
		if !strings.Contains(out, "✓ Split payments into 4 parts") {
			t.Errorf("expected split summary line, got:\n%s", out)
		}
		if !strings.Contains(out, "01-models") || !strings.Contains(out, "02-api") ||
			!strings.Contains(out, "03-ui") || !strings.Contains(out, "04-docs") {
			t.Errorf("expected every part id in the tree, got:\n%s", out)
		}
		if !strings.Contains(out, "Next: colony task add --group") {
			t.Errorf("expected a Next: line, got:\n%s", out)
		}
		if !strings.Contains(out, "Wave 2") || !strings.Contains(out, "Critical path: 01-models → 02-api → 04-docs") {
			t.Errorf("expected waves and critical path, got:\n%s", out)
		}
		plan, err := os.ReadFile(filepath.Join(featureDir, "PLAN.md"))
		if err != nil {
			t.Fatalf("expected PLAN.md: %v", err)
		}
		if !strings.Contains(string(plan), "Critical path:") {
			t.Errorf("PLAN.md missing waves, got:\n%s", plan)
		}
	})

	t.Run("file spec split as-is", func(t *testing.T) {
		dir := initTestRepo(t)
		setupMinimalProject(t, dir)
		origWd, _ := os.Getwd()
		os.Chdir(dir)
		defer os.Chdir(origWd)

		specFile := filepath.Join(t.TempDir(), "bridge.md")
		original := "# Spec: bridge\n\n## 5.3 Customer sign-in\n\nLong hand-written detail.\n"
		if err := os.WriteFile(specFile, []byte(original), 0644); err != nil {
			t.Fatal(err)
		}
		withPlan := "## PLAN\n- Notes: move the spec out of reports/\n\n" + splitFixture

		stubSpecFeatureLLM(t, "generation must not run", withPlan)
		resetSpecFeatureFlags()
		sfSplit = true
		sfFile = specFile

		cmd := &cobra.Command{}
		if _, err := captureStdout(t, func() error { return runSpecFeature(cmd, []string{"bridge"}) }); err != nil {
			t.Fatalf("runSpecFeature: %v", err)
		}

		featureDir := filepath.Join(dir, ".colony", "specs", "bridge")
		source, err := os.ReadFile(filepath.Join(featureDir, "SOURCE.md"))
		if err != nil || string(source) != original {
			t.Fatalf("expected SOURCE.md to be an unchanged copy, err=%v got %q", err, source)
		}
		if _, err := os.Stat(filepath.Join(featureDir, "TASK.md")); !os.IsNotExist(err) {
			t.Error("expected no TASK.md to be generated for a --file split")
		}
		part, err := parsePartFrontmatter(filepath.Join(featureDir, "parts", "02-api.md"))
		if err != nil || part.Source != "../SOURCE.md" {
			t.Errorf("expected part source ../SOURCE.md, got %+v err=%v", part, err)
		}
		plan, _ := os.ReadFile(filepath.Join(featureDir, "PLAN.md"))
		if !strings.Contains(string(plan), "move the spec out of reports/") {
			t.Errorf("expected the LLM plan in PLAN.md, got:\n%s", plan)
		}
	})
}

func TestSpecHashSidecar(t *testing.T) {
	dir := t.TempDir()
	taskFile := filepath.Join(dir, "TASK.md")
	hashFile := strings.TrimSuffix(taskFile, ".md") + ".hash"

	content := []byte("# Agent Task Spec\n\n## 1. Login\n\nImplement login flow.")
	if err := os.WriteFile(taskFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	h := specHash(content)
	if err := os.WriteFile(hashFile, []byte(h), 0644); err != nil {
		t.Fatal(err)
	}

	t.Run("unchanged file matches sidecar hash", func(t *testing.T) {
		saved, err := os.ReadFile(hashFile)
		if err != nil {
			t.Fatal(err)
		}
		current := specHash(content)
		if strings.TrimSpace(string(saved)) != current {
			t.Error("sidecar hash should match content hash when file is unchanged")
		}
	})

	t.Run("edited file does not match sidecar hash", func(t *testing.T) {
		edited := append(content, []byte("\n<!-- change: add OAuth support -->")...)
		saved, err := os.ReadFile(hashFile)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(saved)) == specHash(edited) {
			t.Error("edited content should not match the original sidecar hash")
		}
	})
}
