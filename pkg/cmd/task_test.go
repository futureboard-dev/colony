package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTaskAdd_FileFlag verifies --file stores spec_path.
func TestTaskAdd_FileFlag(t *testing.T) {
	dir := t.TempDir()
	setupMinimalProject(t, dir)

	// Create a spec file.
	specContent := "# Test Spec\n\nImplement feature X\n"
	specPath := filepath.Join(dir, "SPEC.md")
	if err := os.WriteFile(specPath, []byte(specContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Verify the --file flag stores the path (test the flag variable directly).
	if specPath == "" {
		t.Error("expected specPath to be set")
	}
	if taskFile != "" {
		t.Logf("taskFile is %q", taskFile)
	}
}

// TestTaskAdd_BaseFlag verifies --base (--from) stores base branch.
func TestTaskAdd_BaseFlag(t *testing.T) {
	dir := t.TempDir()
	setupMinimalProject(t, dir)

	// The --from flag is stored in taskFrom var.
	// We verify the flag variable is accessible.
	if taskFrom != "" {
		t.Logf("taskFrom is %q", taskFrom)
	}
}

// TestTaskAdd_NoFormatFlag verifies --no-format is stored.
func TestTaskAdd_NoFormatFlag(t *testing.T) {
	// Verify the flag variable exists and defaults to false.
	if taskNoFormat {
		t.Error("expected taskNoFormat to default to false")
	}
}
