package cmd

import (
	"os"
	"strings"
	"testing"
)

func TestTUIRefreshFlagValidation(t *testing.T) {
	t.Run("refresh 100ms is accepted", func(t *testing.T) {
		tuiRefresh = "100ms"
		if err := parseRefreshFlag(); err != nil {
			t.Fatalf("expected 100ms accepted, got %v", err)
		}
	})
	t.Run("refresh 50ms is rejected", func(t *testing.T) {
		tuiRefresh = "50ms"
		err := parseRefreshFlag()
		if err == nil {
			t.Fatal("expected 50ms to be rejected")
		}
		if !strings.Contains(err.Error(), "at least 100ms") {
			t.Errorf("expected min-100ms error, got %v", err)
		}
	})
	t.Run("refresh 500ms parses", func(t *testing.T) {
		tuiRefresh = "500ms"
		if err := parseRefreshFlag(); err != nil {
			t.Fatalf("expected 500ms accepted, got %v", err)
		}
	})
}

func TestTUINewCommandParse(t *testing.T) {
	t.Run("view live", func(t *testing.T) {
		view, err := resolveStartView("live")
		if err != nil {
			t.Fatalf("resolve start view: %v", err)
		}
		if view.String() != "Live Output" {
			t.Errorf("expected Live Output view, got %s", view)
		}
	})
	t.Run("view invalid", func(t *testing.T) {
		if _, err := resolveStartView("bogus"); err == nil {
			t.Error("expected unknown view to error")
		}
	})
	t.Run("empty view defaults to dashboard", func(t *testing.T) {
		view, err := resolveStartView("")
		if err != nil {
			t.Fatalf("resolve start view empty: %v", err)
		}
		if view.String() != "Dashboard" {
			t.Errorf("expected Dashboard default, got %s", view)
		}
	})
}

func TestTUICommandSurfaceFlags(t *testing.T) {
	// --refresh 500ms, --no-color, --force, --view live parse into the right
	// model settings via the real cobra command.
	cmd := tuiCmd
	cmd.SetArgs([]string{"--refresh", "500ms", "--no-color", "--force", "--view", "live"})
	if err := cmd.ParseFlags([]string{"--refresh", "500ms", "--no-color", "--force", "--view", "live"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	if !strings.HasSuffix(tuiRefresh, "ms") {
		t.Errorf("expected --refresh to apply, got %q", tuiRefresh)
	}
	if !tuiNoColor {
		t.Error("expected --no-color to be set")
	}
	if !tuiForce {
		t.Error("expected --force to be set")
	}
	view, err := resolveStartView(tuiStartView)
	if err != nil {
		t.Fatalf("resolve start view: %v", err)
	}
	if view.String() != "Live Output" {
		t.Errorf("expected --view live to resolve to Live Output, got %s", view)
	}
}

func TestTUINotInProject(t *testing.T) {
	// Move to a directory that is not inside a git project so FindRoot fails.
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	err = runTUI(tuiCmd, nil)
	if err == nil {
		t.Fatal("expected an error when not inside a Colony project")
	}
	if !strings.Contains(err.Error(), "git") && !strings.Contains(err.Error(), ".colony") {
		t.Errorf("expected a clear 'no .colony' error, got %v", err)
	}
}
