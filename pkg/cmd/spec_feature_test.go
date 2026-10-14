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

	t.Run("overlapping files warns but passes", func(t *testing.T) {
		body1 := "# Agent Task Spec\n\n## 2. Files In Scope\n\nCREATE:\n- `pkg/shared.go`: shared type\n\nMODIFY:\n\nDO NOT TOUCH:\n"
		body2 := "# Agent Task Spec\n\n## 2. Files In Scope\n\nCREATE:\n- `pkg/shared.go`: also touches it\n\nMODIFY:\n\nDO NOT TOUCH:\n"
		parts := []specPart{
			{ID: "01-a", Body: body1},
			{ID: "02-b", Body: body2},
		}
		warnings, err := validateParts(parts)
		if err != nil {
			t.Fatalf("expected overlap to be a warning, not an error: %v", err)
		}
		if len(warnings) == 0 {
			t.Error("expected a warning about the overlapping file")
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
		if !strings.HasPrefix(content, "---\nid: 02-api\ndepends_on: [01-models]\n---\n") {
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
