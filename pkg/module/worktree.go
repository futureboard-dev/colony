package module

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type WorktreeInfo struct {
	Project string
	Branch  string
	Path    string
	Task    string
	Started string
}

// NewBranch generates an agent branch name from a task description.
func NewBranch(desc string) string {
	ts := time.Now().Format("20060102-150405")
	return fmt.Sprintf("agent/%s-%s", Slugify(desc), ts)
}

// SetupWorktree creates an isolated git worktree on a new branch.
func SetupWorktree(projectRoot, projectName, branch, baseBranch string) (string, error) {
	base := WorktreeBase()
	worktreePath := filepath.Join(base, projectName, branch)

	if err := os.MkdirAll(filepath.Dir(worktreePath), 0755); err != nil {
		return "", err
	}

	if err := gitCmd(projectRoot, "fetch", "origin", baseBranch, "--quiet"); err != nil {
		return "", fmt.Errorf("fetch %s: %w", baseBranch, err)
	}

	if err := gitCmd(projectRoot, "worktree", "add", worktreePath, "-b", branch,
		"origin/"+baseBranch, "--quiet", "--no-track"); err != nil {
		return "", fmt.Errorf("create worktree: %w", err)
	}

	// propagate .claude config and .env into the worktree
	if info, err := os.Stat(filepath.Join(projectRoot, ".claude")); err == nil && info.IsDir() {
		_ = exec.Command("cp", "-r", filepath.Join(projectRoot, ".claude")+"/",
			filepath.Join(worktreePath, ".claude")+"/").Run()
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".env")); err == nil {
		_ = exec.Command("cp", filepath.Join(projectRoot, ".env"),
			filepath.Join(worktreePath, ".env")).Run()
	}

	return worktreePath, nil
}

// RemoveWorktree removes the worktree and optionally the local branch.
func RemoveWorktree(projectRoot, projectName, branch string, deleteBranch bool) error {
	base := WorktreeBase()
	worktreePath := filepath.Join(base, projectName, branch)

	out, _ := exec.Command("git", "-C", projectRoot, "worktree", "list").Output()
	if strings.Contains(string(out), worktreePath) {
		if err := gitCmd(projectRoot, "worktree", "remove", worktreePath, "--force"); err != nil {
			return fmt.Errorf("remove worktree: %w", err)
		}
	}

	if deleteBranch {
		if err := gitCmd(projectRoot, "branch", "-d", branch); err != nil {
			gitCmd(projectRoot, "branch", "-D", branch) //nolint:errcheck
		}
	}
	return nil
}

// ListWorktrees scans the worktree base directory for active agent sessions.
func ListWorktrees() ([]WorktreeInfo, error) {
	base := WorktreeBase()
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var result []WorktreeInfo
	for _, proj := range entries {
		if !proj.IsDir() {
			continue
		}
		agentDir := filepath.Join(base, proj.Name(), "agent")
		branches, err := os.ReadDir(agentDir)
		if err != nil {
			continue
		}
		for _, b := range branches {
			if !b.IsDir() {
				continue
			}
			wPath := filepath.Join(agentDir, b.Name())
			info := WorktreeInfo{
				Project: proj.Name(),
				Branch:  "agent/" + b.Name(),
				Path:    wPath,
			}
			if data, err := os.ReadFile(filepath.Join(wPath, "TASK.md")); err == nil {
				for _, line := range strings.Split(string(data), "\n") {
					if after, ok := strings.CutPrefix(line, "**Task:**"); ok {
						info.Task = strings.TrimSpace(after)
					}
					if after, ok := strings.CutPrefix(line, "**Started:**"); ok {
						info.Started = strings.TrimSpace(after)
					}
				}
			}
			result = append(result, info)
		}
	}
	return result, nil
}

func gitCmd(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
