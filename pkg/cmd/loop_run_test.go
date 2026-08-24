package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/futureboard-dev/colony/pkg/storage"
	"github.com/spf13/cobra"
)

func TestLoopRun_LangValidation(t *testing.T) {
	dir := initTestRepo(t)
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(origWd) }()

	cases := []struct {
		name    string
		lang    string
		wantErr string
	}{
		// An omitted --lang is only rejected once the task is known to have no
		// recorded language, so this case fails on lookup, not on the flag.
		{"missing lang", "", "not found"},
		{"invalid lang", "rust", "unknown language"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loopRunLang = tc.lang
			err := runLoopRun(&cobra.Command{}, []string{"some-id"})
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// A task carrying no language cannot be gated without --lang. (The inverse —
// a task with a recorded language running flagless — is not asserted here
// because it proceeds into a real build.)
func TestLoopRun_MissingLangOnlyFailsForUnsetTask(t *testing.T) {
	dir := initTestRepo(t)
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(origWd) }()

	store, err := storage.Open(filepath.Join(dir, ".colony", "missions.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.InsertTask(storage.Task{ID: "no-lang", Description: "d", State: "open", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	loopRunLang = ""
	err = runLoopRun(&cobra.Command{}, []string{"no-lang"})
	if err == nil || !strings.Contains(err.Error(), "no recorded language") {
		t.Fatalf("expected a missing-language error, got %v", err)
	}
}

func TestLoopRun_TaskNotFound(t *testing.T) {
	dir := initTestRepo(t)
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(origWd) }()

	loopRunLang = "go"
	err := runLoopRun(&cobra.Command{}, []string{"missing-id"})
	if err == nil {
		t.Fatal("expected error for missing task, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got %v", err)
	}
}

func TestUpdateTaskLang(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(dir + "/missions.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	if err := store.InsertTask(storage.Task{
		ID: "t1", Description: "d", State: "blocked", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.UpdateTaskLang("t1", "typescript"); err != nil {
		t.Fatal(err)
	}

	tasks, err := store.QueryTasks(storage.TaskFilter{ID: "t1"})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("query: %v tasks=%d", err, len(tasks))
	}
	if tasks[0].Lang != "typescript" {
		t.Errorf("expected lang typescript, got %q", tasks[0].Lang)
	}
}

func TestResolveGateLang(t *testing.T) {
	newStore := func(t *testing.T, task storage.Task) *storage.SQLiteStore {
		t.Helper()
		store, err := storage.Open(t.TempDir() + "/missions.db")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		task.CreatedAt = time.Now()
		if err := store.InsertTask(task); err != nil {
			t.Fatal(err)
		}
		return store
	}

	t.Run("recorded lang wins over the flag", func(t *testing.T) {
		task := storage.Task{ID: "t1", Description: "d", State: "open", Lang: "typescript"}
		got, err := resolveGateLang(newStore(t, task), &task, "go", true)
		if err != nil || got != "typescript" {
			t.Errorf("expected typescript, got %q err=%v", got, err)
		}
	})

	t.Run("legacy task without an explicit flag is refused", func(t *testing.T) {
		task := storage.Task{ID: "t2", Description: "d", State: "open"}
		_, err := resolveGateLang(newStore(t, task), &task, "go", false)
		if err == nil {
			t.Fatal("expected a refusal for a task with no recorded language")
		}
		if !strings.Contains(err.Error(), "no recorded language") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("explicit flag fills in and persists", func(t *testing.T) {
		task := storage.Task{ID: "t3", Description: "d", State: "open"}
		store := newStore(t, task)
		got, err := resolveGateLang(store, &task, "typescript", true)
		if err != nil || got != "typescript" {
			t.Fatalf("expected typescript, got %q err=%v", got, err)
		}
		tasks, err := store.QueryTasks(storage.TaskFilter{ID: "t3"})
		if err != nil || len(tasks) != 1 {
			t.Fatalf("query: %v tasks=%d", err, len(tasks))
		}
		if tasks[0].Lang != "typescript" {
			t.Errorf("expected the language to be persisted, got %q", tasks[0].Lang)
		}
	})

	t.Run("explicit flag is validated", func(t *testing.T) {
		task := storage.Task{ID: "t4", Description: "d", State: "open"}
		if _, err := resolveGateLang(newStore(t, task), &task, "rust", true); err == nil {
			t.Error("expected an error for an unsupported language")
		}
	})
}

// TestProcessTask_RefusesUnsetLang pins the refusal at the loop's entry point:
// the language is resolved before any worktree or runner work happens, so a
// legacy task is never built with the wrong toolchain.
func TestProcessTask_RefusesUnsetLang(t *testing.T) {
	store, err := storage.Open(t.TempDir() + "/missions.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	task := storage.Task{ID: "legacy-1", Description: "d", State: "open", CreatedAt: time.Now()}
	if err := store.InsertTask(task); err != nil {
		t.Fatal(err)
	}

	origLang, origExplicit := loopLang, loopLangExplicit
	defer func() { loopLang, loopLangExplicit = origLang, origExplicit }()
	loopLang, loopLangExplicit = "go", false

	err = processTask(context.Background(), nil, t.TempDir(), store, &task)
	if err == nil || !strings.Contains(err.Error(), "no recorded language") {
		t.Fatalf("expected a refusal for a task with no recorded language, got %v", err)
	}
}
