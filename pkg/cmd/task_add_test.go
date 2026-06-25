package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jirateep/colony/pkg/storage"
	"github.com/spf13/cobra"
)

func TestTaskAdd_InlineDescription(t *testing.T) {
	dir := initTestRepo(t)
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	taskAddFile = ""
	taskAddBase = ""
	taskAddLang = "go"
	taskAddNoFormat = false

	cmd := &cobra.Command{}
	err := taskAddCmd.RunE(cmd, []string{"my inline task"})
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}

	dbPath := filepath.Join(dir, ".colony", "missions.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	tasks, err := store.QueryTasks(storage.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Description != "my inline task" {
		t.Errorf("expected description %q, got %q", "my inline task", tasks[0].Description)
	}
	if tasks[0].State != "open" {
		t.Errorf("expected state 'open', got %q", tasks[0].State)
	}
	if tasks[0].ID == "" {
		t.Error("expected task ID to be set")
	}
}

func TestTaskAdd_FileFlag(t *testing.T) {
	dir := initTestRepo(t)
	setupMinimalProject(t, dir)

	specContent := "# Test Spec\n"
	specPath := filepath.Join(dir, "SPEC.md")
	if err := os.WriteFile(specPath, []byte(specContent), 0644); err != nil {
		t.Fatal(err)
	}

	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	taskAddFile = specPath
	taskAddBase = ""
	taskAddLang = "go"
	taskAddNoFormat = false

	cmd := &cobra.Command{}
	err := taskAddCmd.RunE(cmd, []string{})
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}

	dbPath := filepath.Join(dir, ".colony", "missions.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	tasks, err := store.QueryTasks(storage.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].SpecPath != specPath {
		t.Errorf("expected spec_path %q, got %q", specPath, tasks[0].SpecPath)
	}
}

func TestTaskAdd_FileFlagMissing(t *testing.T) {
	dir := initTestRepo(t)
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	taskAddFile = filepath.Join(dir, "nonexistent.md")
	taskAddBase = ""
	taskAddLang = "go"
	taskAddNoFormat = false

	cmd := &cobra.Command{}
	err := taskAddCmd.RunE(cmd, []string{})
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected error about file not found, got %v", err)
	}
}

func TestTaskAdd_BaseFlag(t *testing.T) {
	dir := initTestRepo(t)
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	taskAddFile = ""
	taskAddBase = "feature/foo"
	taskAddLang = "go"
	taskAddNoFormat = false

	cmd := &cobra.Command{}
	err := taskAddCmd.RunE(cmd, []string{"desc"})
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}

	dbPath := filepath.Join(dir, ".colony", "missions.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	tasks, err := store.QueryTasks(storage.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].BaseBranch != "feature/foo" {
		t.Errorf("expected base_branch %q, got %q", "feature/foo", tasks[0].BaseBranch)
	}
}

func TestTaskAdd_NoFormatFlag(t *testing.T) {
	dir := initTestRepo(t)
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	taskAddFile = ""
	taskAddBase = ""
	taskAddLang = "go"
	taskAddNoFormat = true

	cmd := &cobra.Command{}
	err := taskAddCmd.RunE(cmd, []string{"desc"})
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}

	dbPath := filepath.Join(dir, ".colony", "missions.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	tasks, err := store.QueryTasks(storage.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].GateOverrides != "format" {
		t.Errorf("expected gate_overrides %q, got %q", "format", tasks[0].GateOverrides)
	}
}

func TestTaskAdd_AllFlags(t *testing.T) {
	dir := initTestRepo(t)
	setupMinimalProject(t, dir)

	specContent := "# All flags\n"
	specPath := filepath.Join(dir, "SPEC.md")
	if err := os.WriteFile(specPath, []byte(specContent), 0644); err != nil {
		t.Fatal(err)
	}

	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	taskAddFile = specPath
	taskAddBase = "develop"
	taskAddLang = "typescript"
	taskAddNoFormat = true

	cmd := &cobra.Command{}
	err := taskAddCmd.RunE(cmd, []string{"full task"})
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}

	dbPath := filepath.Join(dir, ".colony", "missions.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	tasks, err := store.QueryTasks(storage.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Description != "full task" {
		t.Errorf("expected description %q, got %q", "full task", tasks[0].Description)
	}
	if tasks[0].SpecPath != specPath {
		t.Errorf("expected spec_path %q, got %q", specPath, tasks[0].SpecPath)
	}
	if tasks[0].BaseBranch != "develop" {
		t.Errorf("expected base_branch %q, got %q", "develop", tasks[0].BaseBranch)
	}
	if tasks[0].GateOverrides != "format" {
		t.Errorf("expected gate_overrides %q, got %q", "format", tasks[0].GateOverrides)
	}
	if tasks[0].Lang != "typescript" {
		t.Errorf("expected lang %q, got %q", "typescript", tasks[0].Lang)
	}
}

func TestTaskAdd_LangRequired(t *testing.T) {
	dir := initTestRepo(t)
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	taskAddFile = ""
	taskAddBase = ""
	taskAddLang = ""
	taskAddNoFormat = false

	cmd := &cobra.Command{}
	err := taskAddCmd.RunE(cmd, []string{"desc"})
	if err == nil {
		t.Fatal("expected error for missing --lang, got nil")
	}
	if !strings.Contains(err.Error(), "--lang is required") {
		t.Errorf("expected error about --lang required, got %v", err)
	}
}

func TestTaskAdd_LangInvalid(t *testing.T) {
	dir := initTestRepo(t)
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	taskAddFile = ""
	taskAddBase = ""
	taskAddLang = "rust"
	taskAddNoFormat = false

	cmd := &cobra.Command{}
	err := taskAddCmd.RunE(cmd, []string{"desc"})
	if err == nil {
		t.Fatal("expected error for invalid --lang, got nil")
	}
	if !strings.Contains(err.Error(), "unknown language") {
		t.Errorf("expected error about unknown language, got %v", err)
	}
}

func TestTaskAdd_NoLLM(t *testing.T) {
	dir := initTestRepo(t)
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	taskAddFile = ""
	taskAddBase = ""
	taskAddLang = "go"
	taskAddNoFormat = false

	cmd := &cobra.Command{}
	err := taskAddCmd.RunE(cmd, []string{"no llm"})
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}

	dbPath := filepath.Join(dir, ".colony", "missions.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	sessions, err := store.QuerySessions(storage.SessionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions (no LLM), got %d", len(sessions))
	}
}

// initTestRepo creates a temp directory with a git repo and returns its path.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, string(out))
	}
	for _, arg := range []struct{ k, v string }{
		{"user.name", "test"},
		{"user.email", "test@test.com"},
	} {
		ecmd := exec.Command("git", "config", arg.k, arg.v)
		ecmd.Dir = dir
		_ = ecmd.Run()
	}
	return dir
}
