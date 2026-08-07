package module

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func FindRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not inside a git repo — cd into your project first")
	}
	return strings.TrimSpace(string(out)), nil
}

func ProjectName(root string) string {
	return filepath.Base(root)
}

func ColonyDir(root string) string {
	return filepath.Join(root, ".colony")
}

func LogDir(root string) string {
	return filepath.Join(root, ".colony", "logs")
}

func EnsureLogDir(root string) error {
	return os.MkdirAll(LogDir(root), 0755)
}

// configuredWorktreeBase is set once per process from .colony/config.json by
// SetWorktreeBase, so the worktree helpers below can stay config-free.
var configuredWorktreeBase string

// SetWorktreeBase records the base directory from project config. Call it once
// after loading config; an empty value leaves the default in place.
func SetWorktreeBase(base string) {
	configuredWorktreeBase = expandHome(strings.TrimSpace(base))
}

// WorktreeBase returns the directory agent worktrees are created under.
// Precedence: COLONY_WORKTREE_BASE env → worktree_base in config → the
// default ~/Projects/.worktrees.
func WorktreeBase() string {
	if env := expandHome(strings.TrimSpace(os.Getenv("COLONY_WORKTREE_BASE"))); env != "" {
		return env
	}
	if configuredWorktreeBase != "" {
		return configuredWorktreeBase
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Projects", ".worktrees")
}

// expandHome resolves a leading "~" and makes the path absolute, so a config
// value like "~/code/.worktrees" or a relative path behaves as written.
func expandHome(path string) string {
	if path == "" {
		return ""
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return abs
}

func DefaultBranch() string {
	out, err := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD").Output()
	if err != nil {
		return "main"
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "/")
	return parts[len(parts)-1]
}

// CurrentBranch returns the branch checked out in dir. Pass "" for the CWD.
func CurrentBranch(dir string) (string, error) {
	args := []string{"rev-parse", "--abbrev-ref", "HEAD"}
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func RemoteBranchExists(branch string) bool {
	err := exec.Command("git", "rev-parse", "origin/"+branch).Run()
	return err == nil
}

// RemoteURL returns the origin remote URL for dir, normalized to https://.
// Returns "" if there is no origin or the command fails.
func RemoteURL(dir string) string {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	url := strings.TrimSpace(string(out))
	if strings.HasPrefix(url, "git@") {
		url = strings.Replace(url, ":", "/", 1)
		url = strings.Replace(url, "git@", "https://", 1)
	}
	return strings.TrimSuffix(url, ".git")
}
