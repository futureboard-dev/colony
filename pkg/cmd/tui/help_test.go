package tui

import (
	"strings"
	"testing"
)

// TestAdvertisedActionKeysAreHandled is the guard against help text drifting
// ahead of the implementation: every action key the help sheet lists must be
// consumed by its view rather than falling through to a global binding.
func TestAdvertisedActionKeysAreHandled(t *testing.T) {
	// Keys the global switch consumes by opening a modal. Asserting the modal
	// rather than skipping keeps the exemption honest: an entry listed here
	// with no handler fails instead of being waved through.
	globalModals := map[string]Modal{
		"a": ModalAddTask,
		"l": ModalLoopControl,
		"c": ModalPalette,
		"o": ModalObserve,
		"R": ModalReview,
	}

	for view, entries := range actionHelp {
		for _, e := range entries {
			// Enter is drill-in navigation, not an action; it has no modal.
			if e.key == "Enter" {
				continue
			}
			if want, ok := globalModals[e.key]; ok {
				t.Run(view.String()+"/"+e.key, func(t *testing.T) {
					m := newTestModel(t, view, sampleStore())
					m.opts.ColonyDir = t.TempDir()
					m.handleKey(key(e.key))
					if m.modal != want {
						t.Errorf("help advertises %q (%s) in the %s view, but pressing it left modal %v, want %v",
							e.key, e.desc, view, m.modal, want)
					}
				})
				continue
			}
			t.Run(view.String()+"/"+e.key, func(t *testing.T) {
				m := newTestModel(t, view, sampleStore())
				m.opts.ColonyDir = t.TempDir()
				if view == ViewQueue || view == ViewTaskDetail {
					focusTask(t, m, "t-1", "")
				}
				handled, _ := m.handleViewKey(key(e.key))
				if !handled {
					t.Errorf("help advertises %q (%s) in the %s view, but the view does not handle it",
						e.key, e.desc, view)
				}
			})
		}
	}
}

// TestStatusBarKeysAreAdvertisedConsistently keeps the bottom bar honest about
// the action keys, which is where most users read them.
func TestStatusBarKeysAreAdvertisedConsistently(t *testing.T) {
	for view, lines := range statusHints {
		hints := lines[0] + "  " + lines[1]
		for _, e := range actionHelp[view] {
			if e.key == "Enter" {
				continue
			}
			if !strings.Contains(hints, "["+e.key+"]") {
				t.Errorf("%s status bar omits the %q action advertised in help", view, e.key)
			}
		}
	}
}

// TestNoViewAdvertisesUnimplementedActions pins the specific regressions found
// in the original TUI: keys promised in help that silently did nothing.
func TestNoViewAdvertisesUnimplementedActions(t *testing.T) {
	removed := map[View][]string{
		ViewSessions:  {"v"}, // "tail log" was never wired
		ViewDashboard: {"v"}, // duplicated the global view switch
	}
	for view, keys := range removed {
		for _, k := range keys {
			for _, e := range actionHelp[view] {
				if e.key == k {
					t.Errorf("%s still advertises the unimplemented %q action", view, k)
				}
			}
		}
	}
}
