package storage

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// InsertTask creates a new task row.
func (s *SQLiteStore) InsertTask(t Task) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	_, err := s.db.Exec(
		`INSERT INTO tasks
		 (id, description, spec_path, base_branch, gate_overrides, lang, state, cycle_count, last_feedback, branch, pr_url, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Description, t.SpecPath, t.BaseBranch, t.GateOverrides, t.Lang,
		t.State, t.CycleCount, t.LastFeedback, t.Branch, t.PRURL, t.CreatedAt.UTC().Format(time.RFC3339),
		nil,
	)
	return err
}

// QueryTasks returns tasks matching the given filter.
func (s *SQLiteStore) QueryTasks(f TaskFilter) ([]Task, error) {
	query := `SELECT id, description, spec_path, base_branch, gate_overrides, lang, state, cycle_count, last_feedback, branch, pr_url, created_at, updated_at
	          FROM tasks WHERE 1=1`
	args := []any{}
	if f.ID != "" {
		query += " AND id=?"
		args = append(args, f.ID)
	}
	if len(f.States) > 0 {
		placeholders := make([]string, len(f.States))
		for i, st := range f.States {
			placeholders[i] = "?"
			args = append(args, st)
		}
		query += fmt.Sprintf(" AND state IN (%s)", strings.Join(placeholders, ","))
	}
	query += " ORDER BY created_at ASC"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		var startedStr string
		var updatedStr *string
		if err := rows.Scan(
			&t.ID, &t.Description, &t.SpecPath, &t.BaseBranch, &t.GateOverrides,
			&t.Lang, &t.State, &t.CycleCount, &t.LastFeedback, &t.Branch, &t.PRURL, &startedStr, &updatedStr,
		); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339, startedStr)
		if updatedStr != nil {
			u, _ := time.Parse(time.RFC3339, *updatedStr)
			t.UpdatedAt = &u
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// UpdateTaskState updates a task's state and optional feedback.
func (s *SQLiteStore) UpdateTaskState(id, state, feedback string) error {
	_, err := s.db.Exec(
		`UPDATE tasks SET state=?, last_feedback=?, updated_at=? WHERE id=?`,
		state, feedback, time.Now().UTC().Format(time.RFC3339), id,
	)
	return err
}

// UpdateTaskBranch records the worktree branch created for a task so a later
// retry can reuse the same worktree instead of starting fresh.
func (s *SQLiteStore) UpdateTaskBranch(id, branch string) error {
	_, err := s.db.Exec(
		`UPDATE tasks SET branch=?, updated_at=? WHERE id=?`,
		branch, time.Now().UTC().Format(time.RFC3339), id,
	)
	return err
}

// DeleteTask removes a task row by id. Deleting a missing id is a no-op.
func (s *SQLiteStore) DeleteTask(id string) error {
	_, err := s.db.Exec(`DELETE FROM tasks WHERE id=?`, id)
	return err
}

// IncrementCycle increments the cycle_count for a task.
func (s *SQLiteStore) IncrementCycle(id string) error {
	_, err := s.db.Exec(
		`UPDATE tasks SET cycle_count=cycle_count+1, updated_at=? WHERE id=?`,
		time.Now().UTC().Format(time.RFC3339), id,
	)
	return err
}

// PickNextOpenTask returns the next task with state "open" or "needs-fix",
// favouring "needs-fix" first, then oldest "open" first.
// Returns nil when no tasks are pending.
func (s *SQLiteStore) PickNextOpenTask() (*Task, error) {
	query := `SELECT id, description, spec_path, base_branch, gate_overrides, lang, state, cycle_count, last_feedback, branch, pr_url, created_at, updated_at
	          FROM tasks WHERE state IN ('open','needs-fix')
	          ORDER BY
	            CASE state WHEN 'needs-fix' THEN 0 WHEN 'open' THEN 1 ELSE 2 END,
	            created_at ASC
	          LIMIT 1`
	row := s.db.QueryRow(query)
	var t Task
	var startedStr string
	var updatedStr *string
	err := row.Scan(&t.ID, &t.Description, &t.SpecPath, &t.BaseBranch, &t.GateOverrides,
		&t.Lang, &t.State, &t.CycleCount, &t.LastFeedback, &t.Branch, &t.PRURL, &startedStr, &updatedStr)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, err
	}
	t.CreatedAt, _ = time.Parse(time.RFC3339, startedStr)
	if updatedStr != nil {
		u, _ := time.Parse(time.RFC3339, *updatedStr)
		t.UpdatedAt = &u
	}
	return &t, nil
}
