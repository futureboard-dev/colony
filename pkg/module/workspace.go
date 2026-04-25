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

func WorktreeBase() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Projects", ".worktrees")
}

func DefaultBranch() string {
	out, err := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD").Output()
	if err != nil {
		return "main"
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "/")
	return parts[len(parts)-1]
}

func CurrentBranch() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func RemoteBranchExists(branch string) bool {
	err := exec.Command("git", "rev-parse", "origin/"+branch).Run()
	return err == nil
}
