package tui

import (
	"github.com/charmbracelet/bubbletea"
	"github.com/futureboard-dev/colony/pkg/storage"
)

// stubStore is an in-memory Store that satisfies the TUI's storage surface.
type stubStore struct {
	tasks    []storage.Task
	sessions []storage.Session
	steps    []storage.Step
	inserted []storage.Task
	closed   bool
	err      error
}

func (s *stubStore) QueryTasks(f storage.TaskFilter) ([]storage.Task, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make([]storage.Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		if f.ID != "" && t.ID != f.ID {
			continue
		}
		if len(f.States) > 0 && !containsStr(f.States, t.State) {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *stubStore) QuerySessions(f storage.SessionFilter) ([]storage.Session, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make([]storage.Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		if f.SessionID != "" && sess.ID != f.SessionID {
			continue
		}
		if f.MissionName != "" && sess.MissionName != f.MissionName {
			continue
		}
		if f.Status != "" && sess.Status != f.Status {
			continue
		}
		out = append(out, sess)
	}
	return out, nil
}

func (s *stubStore) QuerySteps(f storage.StepFilter) ([]storage.Step, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make([]storage.Step, 0, len(s.steps))
	for _, step := range s.steps {
		if f.SessionID != "" && step.SessionID != f.SessionID {
			continue
		}
		if f.Decision != "" && step.Decision != f.Decision {
			continue
		}
		out = append(out, step)
	}
	return out, nil
}

func (s *stubStore) InsertTask(t storage.Task) error {
	if s.err != nil {
		return s.err
	}
	s.inserted = append(s.inserted, t)
	s.tasks = append(s.tasks, t)
	return nil
}

func (s *stubStore) UpdateTaskState(id, state, feedback string) error {
	if s.err != nil {
		return s.err
	}
	for i := range s.tasks {
		if s.tasks[i].ID == id {
			s.tasks[i].State = state
			s.tasks[i].LastFeedback = feedback
		}
	}
	return nil
}

func (s *stubStore) DeleteTask(id string) error {
	if s.err != nil {
		return s.err
	}
	out := s.tasks[:0]
	for _, t := range s.tasks {
		if t.ID != id {
			out = append(out, t)
		}
	}
	s.tasks = out
	return nil
}

func (s *stubStore) Close() error {
	s.closed = true
	return nil
}

func containsStr(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

// key builds a bubbletea KeyMsg whose String() matches the dispatcher's cases.
func key(s string) tea.KeyMsg {
	special := map[string]tea.KeyType{
		"esc":    tea.KeyEsc,
		"enter":  tea.KeyEnter,
		"space":  tea.KeySpace,
		"down":   tea.KeyDown,
		"up":     tea.KeyUp,
		"ctrl+c": tea.KeyCtrlC,
		"tab":    tea.KeyTab,
	}
	if t, ok := special[s]; ok {
		return tea.KeyMsg{Type: t}
	}
	runes := []rune(s)
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: runes}
}
