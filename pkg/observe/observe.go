package mission

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/jirateep/colony/pkg/storage"
)

// ObserveResult describes what happened during a single task observation.
type ObserveResult struct {
	TaskID      string
	Changed     bool   // true if the task's state or feedback was updated
	NewStatus   string // the status set (empty if unchanged)
	NewFeedback string // feedback set (empty if unchanged)
}

// ObserveTasksForPR polls CI and PR comments for tasks that have a pr_url set.
// It updates task state in the store when feedback is found.
func ObserveTasksForPR(ctx context.Context, store *storage.SQLiteStore) ([]ObserveResult, error) {
	tasks, err := store.QueryTasks(storage.TaskFilter{})
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}

	var results []ObserveResult
	for _, t := range tasks {
		// Only observe tasks with a pr_url that aren't already done.
		if t.PRURL == "" || t.State == "done" {
			continue
		}

		result, err := observeTask(ctx, &t, store)
		if err != nil {
			// Log but continue with other tasks.
			fmt.Printf("observe task %s: %v\n", t.ID, err)
			continue
		}
		results = append(results, *result)
	}
	return results, nil
}

// observeTask checks CI status and PR comments for a single task.
func observeTask(ctx context.Context, task *storage.Task, store *storage.SQLiteStore) (*ObserveResult, error) {
	result := &ObserveResult{TaskID: task.ID}
	ciChanged := false

	// 1. Check CI status.
	if task.Branch != "" {
		ciStatus, ciErr := checkCIStatus(ctx, task.Branch)
		if ciErr == nil && ciStatus != "" {
			switch ciStatus {
			case "success":
				// Green CI — mark done.
				if task.State != "done" {
					_ = store.UpdateTaskState(task.ID, "done", "CI passed")
					result.Changed = true
					result.NewStatus = "done"
					result.NewFeedback = "CI passed"
					ciChanged = true
				}
			case "failure":
				// Red CI — flip to needs-fix.
				feedback := "CI failure"
				if task.State != "needs-fix" || task.LastFeedback != feedback {
					_ = store.UpdateTaskState(task.ID, "needs-fix", feedback)
					result.Changed = true
					result.NewStatus = "needs-fix"
					result.NewFeedback = feedback
					ciChanged = true
				}
			}
		}
	}

	// 2. Check PR comments (only if CI didn't already mark done).
	if !ciChanged && task.PRURL != "" && task.State != "done" {
		comments, err := checkPRComments(ctx, task.PRURL)
		if err == nil && comments != "" {
			feedback := fmt.Sprintf("PR comment: %s", comments)
			_ = store.UpdateTaskState(task.ID, "needs-fix", feedback)
			result.Changed = true
			result.NewStatus = "needs-fix"
			result.NewFeedback = feedback
		}
	}

	return result, nil
}

// checkCIStatus queries GitHub Actions for the latest CI run on a branch.
// Returns "success", "failure", or "" if unknown.
func checkCIStatus(ctx context.Context, branch string) (string, error) {
	// gh run view --branch <branch> --json conclusion --jq .conclusion --limit 1
	cmd := exec.CommandContext(ctx, "gh", "run", "view",
		"--branch", branch,
		"--json", "conclusion",
		"--jq", ".conclusion",
		"--limit", "1",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// gh returns non-zero when no runs exist or gh isn't configured.
		return "", fmt.Errorf("gh run view: %w\n%s", err, strings.TrimSpace(stderr.String()))
	}

	out := strings.TrimSpace(stdout.String())
	switch out {
	case "success":
		return "success", nil
	case "failure", "cancelled", "timed_out":
		return "failure", nil
	default:
		return "", nil
	}
}

// checkPRComments queries the latest PR comment (excluding the PR author).
// Returns the comment body if there's a new comment, or empty string.
func checkPRComments(ctx context.Context, prURL string) (string, error) {
	// Extract owner/repo/number from prURL.
	prNum := extractPRNumber(prURL)
	if prNum == "" {
		return "", fmt.Errorf("cannot parse PR number from URL: %s", prURL)
	}

	// gh pr view <number> --json comments --jq '.comments[-1].body'
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", prNum,
		"--json", "comments",
		"--jq", ".comments[-1].body",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gh pr view: %w\n%s", err, strings.TrimSpace(stderr.String()))
	}

	body := strings.TrimSpace(stdout.String())
	if body == "" || body == "null" {
		return "", nil
	}
	return body, nil
}

// extractPRNumber tries to extract a PR number from a GitHub PR URL.
// e.g. "https://github.com/owner/repo/pull/123" → "123"
// Falls back to the entire string if it looks like a number.
func extractPRNumber(prURL string) string {
	// Try to match /pull/NUMBER pattern.
	if idx := strings.Index(prURL, "/pull/"); idx >= 0 {
		rest := prURL[idx+len("/pull/"):]
		// Take up to the next / or end.
		if slash := strings.Index(rest, "/"); slash >= 0 {
			return rest[:slash]
		}
		return rest
	}
	// Maybe it's just a number already.
	prURL = strings.TrimSpace(prURL)
	if prURL != "" {
		return prURL
	}
	return ""
}
