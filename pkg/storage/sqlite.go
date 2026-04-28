package storage

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaDDL string

// Step represents a single agent execution step.
type Step struct {
	ID         int64
	SessionID  string
	StepNum    int
	SubStep    string
	AgentID    string
	Role       string
	InputText  string
	OutputJSON string
	Decision   string
	DurationMS int64
	StartedAt  time.Time
	FinishedAt time.Time
}

// Session represents a mission run.
type Session struct {
	ID          string
	MissionName string
	StartedAt   time.Time
	FinishedAt  *time.Time
	Status      string
}

// StepFilter controls which steps to return from QuerySteps.
type StepFilter struct {
	SessionID string
	Decision  string
}

// SessionFilter controls which sessions to return from QuerySessions.
type SessionFilter struct {
	MissionName string
	SessionID   string
}

// Store is the persistence interface for mission runs.
type Store interface {
	InsertSession(s Session) error
	UpdateSession(id, status string, finishedAt time.Time) error
	InsertStep(s Step) error
	QuerySessions(f SessionFilter) ([]Session, error)
	QuerySteps(f StepFilter) ([]Step, error)
	Close() error
}

// SQLiteStore implements Store using modernc.org/sqlite.
type SQLiteStore struct {
	db *sql.DB
}

// DefaultDBPath returns the default path for missions.db.
// COLONY_DB_PATH env var overrides it.
func DefaultDBPath() string {
	if p := os.Getenv("COLONY_DB_PATH"); p != "" {
		return p
	}
	return filepath.Join(".colony", "missions.db")
}

// Open opens (or creates) the SQLite database and runs migrations.
func Open(dbPath string) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schemaDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) InsertSession(sess Session) error {
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, mission_name, started_at, status) VALUES (?,?,?,?)`,
		sess.ID, sess.MissionName, sess.StartedAt.UTC().Format(time.RFC3339), sess.Status,
	)
	return err
}

func (s *SQLiteStore) UpdateSession(id, status string, finishedAt time.Time) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET status=?, finished_at=? WHERE id=?`,
		status, finishedAt.UTC().Format(time.RFC3339), id,
	)
	return err
}

func (s *SQLiteStore) InsertStep(step Step) error {
	var finishedAt any
	if !step.FinishedAt.IsZero() {
		finishedAt = step.FinishedAt.UTC().Format(time.RFC3339)
	}
	_, err := s.db.Exec(
		`INSERT INTO steps
		 (session_id, step_num, sub_step, agent_id, role, input_text, output_json, decision, duration_ms, started_at, finished_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		step.SessionID, step.StepNum, step.SubStep, step.AgentID, step.Role,
		step.InputText, step.OutputJSON, step.Decision, step.DurationMS,
		step.StartedAt.UTC().Format(time.RFC3339), finishedAt,
	)
	return err
}

func (s *SQLiteStore) QuerySessions(f SessionFilter) ([]Session, error) {
	query := `SELECT id, mission_name, started_at, finished_at, status FROM sessions WHERE 1=1`
	args := []any{}
	if f.MissionName != "" {
		query += " AND mission_name=?"
		args = append(args, f.MissionName)
	}
	if f.SessionID != "" {
		query += " AND id=?"
		args = append(args, f.SessionID)
	}
	query += " ORDER BY started_at ASC"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var sess Session
		var startedStr string
		var finishedStr *string
		if err := rows.Scan(&sess.ID, &sess.MissionName, &startedStr, &finishedStr, &sess.Status); err != nil {
			return nil, err
		}
		sess.StartedAt, _ = time.Parse(time.RFC3339, startedStr)
		if finishedStr != nil {
			t, _ := time.Parse(time.RFC3339, *finishedStr)
			sess.FinishedAt = &t
		}
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

func (s *SQLiteStore) QuerySteps(f StepFilter) ([]Step, error) {
	query := `SELECT id, session_id, step_num, sub_step, agent_id, role, input_text, output_json, decision, duration_ms, started_at, finished_at
	          FROM steps WHERE 1=1`
	args := []any{}
	if f.SessionID != "" {
		query += " AND session_id=?"
		args = append(args, f.SessionID)
	}
	if f.Decision != "" {
		query += " AND decision=?"
		args = append(args, f.Decision)
	}
	query += " ORDER BY step_num ASC, sub_step ASC"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var steps []Step
	for rows.Next() {
		var step Step
		var startedStr string
		var finishedStr *string
		var inputText, outputJSON, decision *string
		var durationMS *int64
		if err := rows.Scan(
			&step.ID, &step.SessionID, &step.StepNum, &step.SubStep,
			&step.AgentID, &step.Role,
			&inputText, &outputJSON, &decision, &durationMS,
			&startedStr, &finishedStr,
		); err != nil {
			return nil, err
		}
		if inputText != nil {
			step.InputText = *inputText
		}
		if outputJSON != nil {
			step.OutputJSON = *outputJSON
		}
		if decision != nil {
			step.Decision = *decision
		}
		if durationMS != nil {
			step.DurationMS = *durationMS
		}
		step.StartedAt, _ = time.Parse(time.RFC3339, startedStr)
		if finishedStr != nil {
			step.FinishedAt, _ = time.Parse(time.RFC3339, *finishedStr)
		}
		steps = append(steps, step)
	}
	return steps, rows.Err()
}
