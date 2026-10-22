package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/futureboard-dev/colony/pkg/storage"
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

// writeTestParts writes a parts/ directory with one frontmatter file per part,
// mirroring the output of `spec-feature --split`.
func writeTestParts(t *testing.T, specDir string, parts []specPart) {
	t.Helper()
	partsDir := filepath.Join(specDir, "parts")
	if err := os.MkdirAll(partsDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, p := range parts {
		if err := os.WriteFile(filepath.Join(partsDir, p.ID+".md"), []byte(formatPartFile(p)), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func resetTaskAddFlags() {
	taskAddFile, taskAddBase, taskAddLang, taskAddNoFormat, taskAddGroup = "", "", "", false, ""
}

func TestTaskAddGroup(t *testing.T) {
	validParts := func() []specPart {
		return []specPart{
			{ID: "01-models", Body: "# Agent Task Spec\n\nModels."},
			{ID: "02-api", DependsOn: []string{"01-models"}, Body: "# Agent Task Spec\n\nAPI."},
		}
	}

	t.Run("creates epic branch from base", func(t *testing.T) {
		dir := initTestRepo(t)
		setupMinimalProject(t, dir)
		origWd, _ := os.Getwd()
		os.Chdir(dir)
		defer os.Chdir(origWd)

		specDir := filepath.Join(dir, ".colony", "specs", "payments")
		writeTestParts(t, specDir, validParts())

		resetTaskAddFlags()
		taskAddGroup = specDir
		taskAddLang = "go"

		cmd := &cobra.Command{}
		if err := taskAddCmd.RunE(cmd, nil); err != nil {
			t.Fatalf("RunE: %v", err)
		}

		out, err := exec.Command("git", "-C", dir, "branch", "--list", "feature/payments").Output()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), "feature/payments") {
			t.Errorf("expected epic branch feature/payments to exist, git branch output: %q", out)
		}
	})

	t.Run("epic branch exists errors", func(t *testing.T) {
		dir := initTestRepo(t)
		setupMinimalProject(t, dir)
		origWd, _ := os.Getwd()
		os.Chdir(dir)
		defer os.Chdir(origWd)

		if err := exec.Command("git", "-C", dir, "branch", "feature/payments", "main").Run(); err != nil {
			t.Fatal(err)
		}

		specDir := filepath.Join(dir, ".colony", "specs", "payments")
		writeTestParts(t, specDir, validParts())

		resetTaskAddFlags()
		taskAddGroup = specDir
		taskAddLang = "go"

		cmd := &cobra.Command{}
		err := taskAddCmd.RunE(cmd, nil)
		if err == nil {
			t.Fatal("expected an error when the epic branch already exists")
		}

		dbPath := filepath.Join(dir, ".colony", "missions.db")
		store, serr := storage.Open(dbPath)
		if serr != nil {
			t.Fatal(serr)
		}
		defer store.Close()
		groups, _ := store.QueryGroups(storage.GroupFilter{})
		if len(groups) != 0 {
			t.Errorf("expected no groups inserted, got %d", len(groups))
		}
		tasks, _ := store.QueryTasks(storage.TaskFilter{})
		if len(tasks) != 0 {
			t.Errorf("expected no tasks inserted, got %d", len(tasks))
		}
	})

	t.Run("tasks inserted in topo order with mapped task-id deps", func(t *testing.T) {
		dir := initTestRepo(t)
		setupMinimalProject(t, dir)
		origWd, _ := os.Getwd()
		os.Chdir(dir)
		defer os.Chdir(origWd)

		specDir := filepath.Join(dir, ".colony", "specs", "payments")
		writeTestParts(t, specDir, validParts())

		resetTaskAddFlags()
		taskAddGroup = specDir
		taskAddLang = "go"

		cmd := &cobra.Command{}
		if err := taskAddCmd.RunE(cmd, nil); err != nil {
			t.Fatalf("RunE: %v", err)
		}

		dbPath := filepath.Join(dir, ".colony", "missions.db")
		store, err := storage.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()

		groups, err := store.QueryGroups(storage.GroupFilter{})
		if err != nil || len(groups) != 1 {
			t.Fatalf("expected 1 group, err=%v got=%d", err, len(groups))
		}
		if groups[0].EpicBranch != "feature/payments" || groups[0].State != "open" {
			t.Errorf("unexpected group: %+v", groups[0])
		}

		tasks, err := store.QueryTasks(storage.TaskFilter{GroupID: groups[0].ID})
		if err != nil || len(tasks) != 2 {
			t.Fatalf("expected 2 tasks, err=%v got=%d", err, len(tasks))
		}
		var models, api storage.Task
		for _, tk := range tasks {
			if strings.HasSuffix(tk.SpecPath, "01-models.md") {
				models = tk
			}
			if strings.HasSuffix(tk.SpecPath, "02-api.md") {
				api = tk
			}
		}
		if models.ID == "" || api.ID == "" {
			t.Fatalf("expected both tasks to be found, got %+v", tasks)
		}
		if models.BaseBranch != "feature/payments" || api.BaseBranch != "feature/payments" {
			t.Errorf("expected BaseBranch=feature/payments on both tasks, got %+v %+v", models, api)
		}
		if api.DependsOn != models.ID {
			t.Errorf("expected api.DependsOn to be the models task id %q, got %q", models.ID, api.DependsOn)
		}
		if models.DependsOn != "" {
			t.Errorf("expected models task to have no dependencies, got %q", models.DependsOn)
		}
	})

	t.Run("cycle rejected", func(t *testing.T) {
		dir := initTestRepo(t)
		setupMinimalProject(t, dir)
		origWd, _ := os.Getwd()
		os.Chdir(dir)
		defer os.Chdir(origWd)

		specDir := filepath.Join(dir, ".colony", "specs", "payments")
		writeTestParts(t, specDir, []specPart{
			{ID: "01-a", DependsOn: []string{"02-b"}, Body: "x"},
			{ID: "02-b", DependsOn: []string{"01-a"}, Body: "y"},
		})

		resetTaskAddFlags()
		taskAddGroup = specDir
		taskAddLang = "go"

		cmd := &cobra.Command{}
		if err := taskAddCmd.RunE(cmd, nil); err == nil {
			t.Fatal("expected a cycle error")
		}

		dbPath := filepath.Join(dir, ".colony", "missions.db")
		store, err := storage.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		groups, _ := store.QueryGroups(storage.GroupFilter{})
		tasks, _ := store.QueryTasks(storage.TaskFilter{})
		if len(groups) != 0 || len(tasks) != 0 {
			t.Errorf("expected no rows inserted, got groups=%d tasks=%d", len(groups), len(tasks))
		}
	})

	t.Run("missing dep rejected", func(t *testing.T) {
		dir := initTestRepo(t)
		setupMinimalProject(t, dir)
		origWd, _ := os.Getwd()
		os.Chdir(dir)
		defer os.Chdir(origWd)

		specDir := filepath.Join(dir, ".colony", "specs", "payments")
		writeTestParts(t, specDir, []specPart{
			{ID: "02-api", DependsOn: []string{"01-models"}, Body: "x"},
		})

		resetTaskAddFlags()
		taskAddGroup = specDir
		taskAddLang = "go"

		cmd := &cobra.Command{}
		if err := taskAddCmd.RunE(cmd, nil); err == nil {
			t.Fatal("expected a missing-dependency error")
		}

		dbPath := filepath.Join(dir, ".colony", "missions.db")
		store, err := storage.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		groups, _ := store.QueryGroups(storage.GroupFilter{})
		tasks, _ := store.QueryTasks(storage.TaskFilter{})
		if len(groups) != 0 || len(tasks) != 0 {
			t.Errorf("expected no rows inserted, got groups=%d tasks=%d", len(groups), len(tasks))
		}
	})

	t.Run("edited frontmatter respected", func(t *testing.T) {
		dir := initTestRepo(t)
		setupMinimalProject(t, dir)
		origWd, _ := os.Getwd()
		os.Chdir(dir)
		defer os.Chdir(origWd)

		specDir := filepath.Join(dir, ".colony", "specs", "payments")
		writeTestParts(t, specDir, validParts())

		// Hand-edit 02-api.md to drop its dependency on 01-models.
		editedPath := filepath.Join(specDir, "parts", "02-api.md")
		edited := formatPartFile(specPart{ID: "02-api", Body: "# Agent Task Spec\n\nAPI, edited."})
		if err := os.WriteFile(editedPath, []byte(edited), 0644); err != nil {
			t.Fatal(err)
		}

		resetTaskAddFlags()
		taskAddGroup = specDir
		taskAddLang = "go"

		cmd := &cobra.Command{}
		if err := taskAddCmd.RunE(cmd, nil); err != nil {
			t.Fatalf("RunE: %v", err)
		}

		dbPath := filepath.Join(dir, ".colony", "missions.db")
		store, err := storage.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		groups, err := store.QueryGroups(storage.GroupFilter{})
		if err != nil || len(groups) != 1 {
			t.Fatalf("expected 1 group, err=%v got=%d", err, len(groups))
		}
		tasks, err := store.QueryTasks(storage.TaskFilter{GroupID: groups[0].ID})
		if err != nil || len(tasks) != 2 {
			t.Fatalf("expected 2 tasks, err=%v got=%d", err, len(tasks))
		}
		for _, tk := range tasks {
			if strings.HasSuffix(tk.SpecPath, "02-api.md") && tk.DependsOn != "" {
				t.Errorf("expected the edited part's dependency to be dropped, got DependsOn=%q", tk.DependsOn)
			}
		}
	})
}

// initTestRepo creates a temp directory with a git repo and returns its path.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "--initial-branch=main")
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
