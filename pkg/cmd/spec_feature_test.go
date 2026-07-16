package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
