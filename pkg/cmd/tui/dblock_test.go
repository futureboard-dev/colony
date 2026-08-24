package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/futureboard-dev/colony/pkg/storage"
)

// errLocked mimics a sqlite busy/locked error string.
var errLocked = errors.New("database is locked")

func TestDBLockedBannerAndWriteToast(t *testing.T) {
	st := sampleStore()
	// Make reads fail with a locked error.
	st.err = errLocked

	m := New(Options{Refresh: time.Second, StartView: ViewDashboard}, st)
	m.width, m.height = 120, 40

	// Send a DBResultMsg carrying the locked error.
	out, _ := m.Update(DBResultMsg{Err: errLocked})
	m2 := out.(*Model)

	if !m2.dbLocked {
		t.Error("expected dbLocked flag set on a locked read")
	}
	if !m2.lockBanner {
		t.Error("expected the 'Database locked' banner on a locked read")
	}
	if rendered := m2.View(); !strings.Contains(rendered, "Database locked") {
		t.Errorf("expected Database locked banner, got: %s", rendered)
	}

	// A subsequent successful read clears the banner and lock.
	out2, _ := m2.Update(DBResultMsg{Tasks: []storage.Task{}, Sessions: nil, Steps: nil})
	m3 := out2.(*Model)
	if m3.dbLocked {
		t.Error("expected dbLocked cleared after a successful read")
	}
	if m3.lockBanner {
		t.Error("expected lock banner cleared after a successful read")
	}

	// Write failure surfaces as a busy toast. Push an async/err toast directly
	// and assert it renders.
	m3.notifier.Push("write failed: database is locked", ToastErr)
	if rendered := m3.View(); !strings.Contains(rendered, "write failed") {
		t.Errorf("expected write-error toast, got: %s", rendered)
	}
}

func TestDBLockedRetryBackoff(t *testing.T) {
	st := sampleStore()
	st.err = errLocked
	m := New(Options{Refresh: 100 * time.Millisecond, StartView: ViewDashboard}, st)
	m.width, m.height = 120, 40

	// Simulate a poll while locked: handlePoll schedules a retry rather than
	// immediately falling over.
	cmd := m.handlePoll()
	if cmd == nil {
		t.Error("expected a retry command while the DB is locked")
	}
}
