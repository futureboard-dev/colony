package tui

import (
	"strings"
	"testing"
)

// TestAdvertisedActionKeysAreHandled is the guard against help text drifting
// ahead of the implementation: every action key the help sheet lists must be
// consumed by its view rather than falling through to a global binding.
func TestAdvertisedActionKeysAreHandled(t *testing.T) {
	// Keys whose effect is a modal or navigation handled by the global switch.
	globallyHandled := map[string]bool{"a": true, "l": true, "c": true, "o": true, "Enter": true}

	for view, entries := range actionHelp {
		for _, e := range entries {
			if globallyHandled[e.key] {
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
