package storage

import (
	"github.com/futureboard-dev/colony/pkg/handoff"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRepeatedOversizedTaskFeedback(t *testing.T) {
	db := openTestDB(t)
	if err := db.InsertTask(Task{ID: "bounded", Description: "required spec", State: "blocked", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"old failure", "new failure"} {
		full := marker + strings.Repeat("x", handoff.FeedbackLimit*4)
		if err := db.UpdateTaskState("bounded", "blocked", full); err != nil {
			t.Fatal(err)
		}
		tasks, err := db.QueryTasks(TaskFilter{ID: "bounded"})
		if err != nil {
			t.Fatal(err)
		}
		task := tasks[0]
		if len(task.LastFeedback) > handoff.FeedbackLimit || !strings.HasPrefix(task.LastFeedback, marker) || task.Description != "required spec" {
			t.Fatal(task)
		}
		_, path, ok := strings.Cut(task.LastFeedback, "\nFull log: ")
		if !ok {
			t.Fatal("no log reference")
		}
		saved, err := os.ReadFile(path)
		if err != nil || string(saved) != full {
			t.Fatal("full feedback not retained", err)
		}
	}
}
