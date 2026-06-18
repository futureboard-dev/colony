package cmd

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jirateep/colony/pkg/storage"
)

func TestValidateClearSelector(t *testing.T) {
	cases := []struct {
		name    string
		taskID  string
		state   string
		all     bool
		wantErr bool
	}{
		{"id only", "t1", "", false, false},
		{"state only", "", "done", false, false},
		{"all only", "", "", true, false},
		{"none", "", "", false, true},
		{"id and state", "t1", "done", false, true},
		{"id and all", "t1", "", true, true},
		{"state and all", "", "done", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateClearSelector(tc.taskID, tc.state, tc.all)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateClearSelector(%q,%q,%v) err=%v, wantErr=%v", tc.taskID, tc.state, tc.all, err, tc.wantErr)
			}
		})
	}
}

func TestSelectClearTasks(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "missions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now()
	for _, tk := range []storage.Task{
		{ID: "a", Description: "a", State: "open", CreatedAt: now},
		{ID: "b", Description: "b", State: "done", CreatedAt: now.Add(time.Second)},
	} {
		if err := store.InsertTask(tk); err != nil {
			t.Fatal(err)
		}
	}

	// By id.
	got, err := selectClearTasks(store, "a", "")
	if err != nil || len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("by id: got %+v err %v", got, err)
	}

	// Missing id is an error.
	if _, err := selectClearTasks(store, "missing", ""); err == nil {
		t.Error("expected error for missing id")
	}

	// By state.
	got, err = selectClearTasks(store, "", "done")
	if err != nil || len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("by state: got %+v err %v", got, err)
	}

	// All (no selectors).
	got, err = selectClearTasks(store, "", "")
	if err != nil || len(got) != 2 {
		t.Fatalf("all: got %d tasks err %v", len(got), err)
	}
}

func TestClearTasks_RemovesRowsAndToleratesMissingWorktree(t *testing.T) {
	dir := t.TempDir()
	setupMinimalProject(t, dir)

	store, err := storage.Open(filepath.Join(dir, ".colony", "missions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now()
	// One task references a branch that has no worktree on disk — removal must
	// be tolerated. One has no branch at all.
	tasks := []storage.Task{
		{ID: "x", Description: "x", State: "done", Branch: "agent/never-created", CreatedAt: now},
		{ID: "y", Description: "y", State: "done", CreatedAt: now.Add(time.Second)},
	}
	for _, tk := range tasks {
		if err := store.InsertTask(tk); err != nil {
			t.Fatal(err)
		}
	}

	n := clearTasks(store, dir, tasks)
	if n != 2 {
		t.Errorf("clearTasks removed %d rows, want 2", n)
	}

	remaining, err := store.QueryTasks(storage.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Errorf("expected 0 tasks after clear, got %d", len(remaining))
	}
}
