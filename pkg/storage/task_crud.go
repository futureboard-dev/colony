package storage

import (
	"time"
)

// InsertTask adds a new task to the queue.
func (s *SQLiteStore) InsertTask(t Task) error {
	_, err := s.db.Exec(
		`INSERT INTO tasks (id, description, spec_path, status, branch, pr_url, last_feedback, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Description, t.SpecPath, t.Status, t.Branch, t.PRURL, t.LastFeedback,
		t.CreatedAt.UTC().Format(time.RFC3339),
		t.UpdatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

// QueryTasks returns all tasks ordered by created_at ASC.
func (s *SQLiteStore) QueryTasks() ([]Task, error) {
	rows, err := s.db.Query(
		`SELECT id, description, spec_path, status, branch, pr_url, last_feedback, created_at, updated_at
		 FROM tasks ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		var createdStr, updatedStr string
		if err := rows.Scan(
			&t.ID, &t.Description, &t.SpecPath, &t.Status, &t.Branch, &t.PRURL,
			&t.LastFeedback, &createdStr, &updatedStr,
		); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		t.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// UpdateTaskState updates a task's status, last_feedback, and updated_at.
func (s *SQLiteStore) UpdateTaskState(id, status, lastFeedback string) error {
	_, err := s.db.Exec(
		`UPDATE tasks SET status=?, last_feedback=?, updated_at=? WHERE id=?`,
		status, lastFeedback, time.Now().UTC().Format(time.RFC3339), id,
	)
	return err
}
