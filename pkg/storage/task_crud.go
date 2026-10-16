package storage

import (
	"database/sql"
	"fmt"
	"github.com/futureboard-dev/colony/pkg/handoff"
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
		 (id, description, spec_path, base_branch, gate_overrides, lang, state, cycle_count, last_feedback, branch, pr_url, group_id, depends_on, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Description, t.SpecPath, t.BaseBranch, t.GateOverrides, t.Lang,
		t.State, t.CycleCount, t.LastFeedback, t.Branch, t.PRURL, t.GroupID, t.DependsOn,
		t.CreatedAt.UTC().Format(time.RFC3339),
		nil,
	)
	return err
}

const taskColumns = `id, description, spec_path, base_branch, gate_overrides, lang, state, cycle_count, last_feedback, branch, pr_url, group_id, depends_on, created_at, updated_at`

// scanTask reads one task row from a rows-like cursor whose columns match taskColumns.
func scanTask(scan func(dest ...any) error) (Task, error) {
	var t Task
	var startedStr string
	var updatedStr *string
	err := scan(
		&t.ID, &t.Description, &t.SpecPath, &t.BaseBranch, &t.GateOverrides,
		&t.Lang, &t.State, &t.CycleCount, &t.LastFeedback, &t.Branch, &t.PRURL,
		&t.GroupID, &t.DependsOn, &startedStr, &updatedStr,
	)
	if err != nil {
		return Task{}, err
	}
	t.CreatedAt, _ = time.Parse(time.RFC3339, startedStr)
	if updatedStr != nil {
		u, _ := time.Parse(time.RFC3339, *updatedStr)
		t.UpdatedAt = &u
	}
	return t, nil
}

// QueryTasks returns tasks matching the given filter.
func (s *SQLiteStore) QueryTasks(f TaskFilter) ([]Task, error) {
	query := `SELECT ` + taskColumns + ` FROM tasks WHERE 1=1`
	args := []any{}
	if f.ID != "" {
		query += " AND id=?"
		args = append(args, f.ID)
	}
	if f.GroupID != "" {
		query += " AND group_id=?"
		args = append(args, f.GroupID)
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
		t, err := scanTask(rows.Scan)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// ClaimNextTask atomically claims the oldest task in "open" or "needs-fix"
// whose dependencies (tasks.depends_on, comma-joined task IDs) are all
// "done", moving it to "running" in the same transaction. Returns (nil, nil)
// when nothing is currently claimable. A dependency on a "blocked" task keeps
// the dependent unclaimable without blocking it itself.
func (s *SQLiteStore) ClaimNextTask() (*Task, error) {
	for {
		tx, err := s.db.Begin()
		if err != nil {
			return nil, err
		}

		candidate, err := findClaimableTask(tx)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if candidate == nil {
			_ = tx.Rollback()
			return nil, nil
		}

		res, err := tx.Exec(
			`UPDATE tasks SET state='running', updated_at=? WHERE id=? AND state IN ('open','needs-fix')`,
			time.Now().UTC().Format(time.RFC3339), candidate.ID,
		)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if n == 0 {
			// Lost the race for this candidate — retry the selection.
			_ = tx.Rollback()
			continue
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		// candidate.State keeps the state it was claimed from (stored state is
		// now "running") so callers can tell a retry from a fresh start.
		return candidate, nil
	}
}

// findClaimableTask selects the oldest open/needs-fix task whose dependencies
// are all done, within tx.
func findClaimableTask(tx *sql.Tx) (*Task, error) {
	rows, err := tx.Query(`SELECT ` + taskColumns + ` FROM tasks ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var all []Task
	for rows.Next() {
		t, err := scanTask(rows.Scan)
		if err != nil {
			return nil, err
		}
		all = append(all, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	stateByID := make(map[string]string, len(all))
	for _, t := range all {
		stateByID[t.ID] = t.State
	}

	for i := range all {
		t := &all[i]
		if t.State != "open" && t.State != "needs-fix" {
			continue
		}
		if depsSatisfied(t.DependsOn, stateByID) {
			return t, nil
		}
	}
	return nil, nil
}

// depsSatisfied reports whether every task ID in a comma-joined depends_on
// string has state "done". An empty string has no dependencies.
func depsSatisfied(dependsOn string, stateByID map[string]string) bool {
	for id := range strings.SplitSeq(dependsOn, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if stateByID[id] != "done" {
			return false
		}
	}
	return true
}

// InsertGroup creates a new task_groups row.
func (s *SQLiteStore) InsertGroup(g TaskGroup) error {
	if g.ID == "" {
		g.ID = uuid.New().String()
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now()
	}
	if g.State == "" {
		g.State = "open"
	}
	_, err := s.db.Exec(
		`INSERT INTO task_groups
		 (id, name, epic_branch, base_branch, lang, state, pr_url, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		g.ID, g.Name, g.EpicBranch, g.BaseBranch, g.Lang, g.State, g.PRURL,
		g.CreatedAt.UTC().Format(time.RFC3339), nil,
	)
	return err
}

// QueryGroups returns groups matching the given filter.
func (s *SQLiteStore) QueryGroups(f GroupFilter) ([]TaskGroup, error) {
	query := `SELECT id, name, epic_branch, base_branch, lang, state, pr_url, created_at, updated_at
	          FROM task_groups WHERE 1=1`
	args := []any{}
	if f.ID != "" {
		query += " AND id=?"
		args = append(args, f.ID)
	}
	query += " ORDER BY created_at ASC"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []TaskGroup
	for rows.Next() {
		var g TaskGroup
		var startedStr string
		var updatedStr *string
		if err := rows.Scan(
			&g.ID, &g.Name, &g.EpicBranch, &g.BaseBranch, &g.Lang, &g.State, &g.PRURL,
			&startedStr, &updatedStr,
		); err != nil {
			return nil, err
		}
		g.CreatedAt, _ = time.Parse(time.RFC3339, startedStr)
		if updatedStr != nil {
			u, _ := time.Parse(time.RFC3339, *updatedStr)
			g.UpdatedAt = &u
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// UpdateGroup updates a group's state and PR URL.
func (s *SQLiteStore) UpdateGroup(id, state, prURL string) error {
	_, err := s.db.Exec(
		`UPDATE task_groups SET state=?, pr_url=?, updated_at=? WHERE id=?`,
		state, prURL, time.Now().UTC().Format(time.RFC3339), id,
	)
	return err
}

// UpdateTaskState updates a task's state and optional feedback.
func (s *SQLiteStore) UpdateTaskState(id, state, feedback string) error {
	if len(feedback) > handoff.FeedbackLimit {
		path, err := handoff.Save(s.logRoot, "task-feedback", feedback)
		if err != nil {
			return fmt.Errorf("archive task feedback: %w", err)
		}
		feedback = handoff.Summary(feedback, path, handoff.FeedbackLimit)
	}

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

// UpdateTaskLang records the language used for a task's quality gates so a
// later resume runs the correct toolchain.
func (s *SQLiteStore) UpdateTaskLang(id, lang string) error {
	_, err := s.db.Exec(
		`UPDATE tasks SET lang=?, updated_at=? WHERE id=?`,
		lang, time.Now().UTC().Format(time.RFC3339), id,
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
