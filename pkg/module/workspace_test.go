package module

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWorktreeBase(t *testing.T) {
	// SetWorktreeBase writes package state; reset it around every subtest.
	reset := func(t *testing.T) {
		t.Helper()
		t.Cleanup(func() { SetWorktreeBase("") })
	}

	t.Run("defaults to ~/Projects/.worktrees", func(t *testing.T) {
		reset(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("COLONY_WORKTREE_BASE", "")

		want := filepath.Join(home, "Projects", ".worktrees")
		if got := WorktreeBase(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("config value overrides the default", func(t *testing.T) {
		reset(t)
		t.Setenv("HOME", t.TempDir())
		t.Setenv("COLONY_WORKTREE_BASE", "")

		SetWorktreeBase("/srv/agents")
		if got := WorktreeBase(); got != "/srv/agents" {
			t.Errorf("got %q, want /srv/agents", got)
		}
	})

	t.Run("env overrides config", func(t *testing.T) {
		reset(t)
		t.Setenv("HOME", t.TempDir())
		t.Setenv("COLONY_WORKTREE_BASE", "/srv/from-env")

		SetWorktreeBase("/srv/from-config")
		if got := WorktreeBase(); got != "/srv/from-env" {
			t.Errorf("got %q, want /srv/from-env", got)
		}
	})

	t.Run("empty config value keeps the default", func(t *testing.T) {
		reset(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("COLONY_WORKTREE_BASE", "")

		SetWorktreeBase("   ")
		want := filepath.Join(home, "Projects", ".worktrees")
		if got := WorktreeBase(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("expands a leading tilde", func(t *testing.T) {
		reset(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("COLONY_WORKTREE_BASE", "")

		SetWorktreeBase("~/code/.worktrees")
		want := filepath.Join(home, "code", ".worktrees")
		if got := WorktreeBase(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("SetupWorktreeLocal creates under the configured base", func(t *testing.T) {
		reset(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("COLONY_WORKTREE_BASE", "")

		root := filepath.Join(home, "repo")
		if err := os.MkdirAll(root, 0755); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{
			{"init", "-q"},
			{"config", "user.email", "t@example.com"},
			{"config", "user.name", "t"},
			{"commit", "-q", "--allow-empty", "-m", "init"},
		} {
			cmd := exec.Command("git", args...)
			cmd.Dir = root
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}

		base := filepath.Join(home, "custom-trees")
		SetWorktreeBase(base)

		path, err := SetupWorktreeLocal(root, "repo", "agent/demo", "")
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(base, "repo", "agent", "demo")
		if path != want {
			t.Errorf("worktree at %q, want %q", path, want)
		}
		if _, err := os.Stat(want); err != nil {
			t.Errorf("worktree dir missing: %v", err)
		}
	})

	t.Run("WorktreePath is built from the resolved base", func(t *testing.T) {
		reset(t)
		t.Setenv("HOME", t.TempDir())
		t.Setenv("COLONY_WORKTREE_BASE", "")

		SetWorktreeBase("/srv/agents")
		want := "/srv/agents/proj/agent/task-1"
		if got := WorktreePath("proj", "agent/task-1"); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}
