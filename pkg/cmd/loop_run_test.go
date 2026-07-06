package cmd

import (
	"os"
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
		{"missing lang", "", "--lang is required"},
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
