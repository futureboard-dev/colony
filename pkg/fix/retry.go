package fix

import (
	"context"
	"fmt"
	"strings"

	"github.com/futureboard-dev/colony/pkg/storage"
)

// storeRetrier implements Retrier by resuming a blocked task directly in the
// task store: it moves the task back to "needs-fix" with its block reason
// (falling back to the task's stored last feedback when the request carries
// none) plus any user hint folded into the feedback the loop reads back in,
// records the hint on the task row for later inspection, and never touches
// the task's branch — so the loop reuses the same worktree on its next pass.
type storeRetrier struct {
	open func() (storage.Store, error)
}

func init() {
	SetRetrier(storeRetrier{open: defaultOpenStore})
}

// defaultOpenStore opens the project's task store at its default path.
func defaultOpenStore() (storage.Store, error) {
	return storage.Open(storage.DefaultDBPath())
}

// Retry implements Retrier.
func (r storeRetrier) Retry(ctx context.Context, req Request) error {
	store, err := r.open()
	if err != nil {
		return fmt.Errorf("retry: open store: %w", err)
	}
	defer func() { _ = store.Close() }()

	tasks, err := store.QueryTasks(storage.TaskFilter{ID: req.TaskID})
	if err != nil {
		return fmt.Errorf("retry: query task %s: %w", req.TaskID, err)
	}
	if len(tasks) == 0 {
		return fmt.Errorf("retry: task %s not found", req.TaskID)
	}
	task := tasks[0]

	reason := req.Reason
	if reason == "" {
		reason = task.LastFeedback
	}
	feedback := reason
	if req.Hint != "" {
		feedback = strings.TrimSpace(feedback + "\n\nUser hint: " + req.Hint)
	}

	if err := store.UpdateTaskBlockHint(req.TaskID, req.Hint); err != nil {
		return fmt.Errorf("retry: record hint: %w", err)
	}
	if err := store.UpdateTaskState(req.TaskID, "needs-fix", feedback); err != nil {
		return fmt.Errorf("retry: update state: %w", err)
	}
	return nil
}
