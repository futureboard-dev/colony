package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRawReports(t *testing.T) {
	t.Run("reads lens json files", func(t *testing.T) {
		dir := t.TempDir()
		raw := filepath.Join(dir, "raw")
		if err := os.MkdirAll(raw, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(raw, "bugs.json"), []byte(`{"findings":[]}`), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(raw, "security.json"), []byte(`{"findings":[1]}`), 0644); err != nil {
			t.Fatal(err)
		}
		// Non-json files should be ignored.
		if err := os.WriteFile(filepath.Join(raw, "notes.txt"), []byte("ignore me"), 0644); err != nil {
			t.Fatal(err)
		}

		reports, err := loadRawReports(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(reports) != 2 {
			t.Fatalf("expected 2 reports, got %d: %v", len(reports), reports)
		}
		if reports["bugs"] != `{"findings":[]}` {
			t.Errorf("bugs report mismatch: %q", reports["bugs"])
		}
		if _, ok := reports["security"]; !ok {
			t.Error("missing security report")
		}
	})

	t.Run("errors when raw dir missing", func(t *testing.T) {
		if _, err := loadRawReports(t.TempDir()); err == nil {
			t.Fatal("expected error for missing raw/ dir")
		}
	})

	t.Run("errors when no json reports", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "raw"), 0755); err != nil {
			t.Fatal(err)
		}
		if _, err := loadRawReports(dir); err == nil {
			t.Fatal("expected error for empty raw/ dir")
		}
	})
}
