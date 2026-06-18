package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/jirateep/colony/pkg/module"
	"github.com/jirateep/colony/pkg/storage"
	"github.com/spf13/cobra"
)

var (
	clearState string
	clearAll   bool
	clearYes   bool
)

var loopClearCmd = &cobra.Command{
	Use:   "clear [task-id]",
	Short: "Remove loop tasks and their worktrees from the queue",
	Long: `Removes one or more tasks from the loop queue and cleans up each task's
git worktree and local branch.

Provide exactly one selector:
  clear <task-id>      remove a single task by id
  clear --state <s>    remove all tasks in a state (open, needs-fix, blocked, done)
  clear --all          remove every task in the queue

Flags:
  --state <s>   remove all tasks in the given state
  --all         remove every task
  --yes, -y     skip the confirmation prompt for bulk deletes`,
	Args: cobra.MaximumNArgs(1),
	RunE: runLoopClear,
}

func init() {
	loopClearCmd.Flags().StringVar(&clearState, "state", "", "remove all tasks in the given state")
	loopClearCmd.Flags().BoolVar(&clearAll, "all", false, "remove every task")
	loopClearCmd.Flags().BoolVarP(&clearYes, "yes", "y", false, "skip the confirmation prompt for bulk deletes")
}

func runLoopClear(cmd *cobra.Command, args []string) error {
	var taskID string
	if len(args) == 1 {
		taskID = args[0]
	}

	if err := validateClearSelector(taskID, clearState, clearAll); err != nil {
		return err
	}

	_, root, err := loadConfig()
	if err != nil {
		return err
	}

	store, err := openLoopStore(root)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = store.Close() }()

	tasks, err := selectClearTasks(store, taskID, clearState)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		fmt.Println("No matching tasks to clear.")
		return nil
	}

	// Confirm bulk deletes (anything not a single explicit id) unless --yes.
	if taskID == "" && !clearYes {
		reader := bufio.NewReader(os.Stdin)
		fmt.Printf("Remove %d task(s) and their worktrees? [y/N] ", len(tasks))
		answer, _ := reader.ReadString('\n')
		if !strings.EqualFold(strings.TrimSpace(answer), "y") {
			fmt.Println("Aborted.")
			return nil
		}
	}

	n := clearTasks(store, root, tasks)
	fmt.Printf("%s✓ Cleared %d task(s)%s\n", ansiGreen, n, ansiReset)
	return nil
}

// validateClearSelector enforces exactly one of: task id, --state, --all.
func validateClearSelector(taskID, state string, all bool) error {
	count := 0
	if taskID != "" {
		count++
	}
	if state != "" {
		count++
	}
	if all {
		count++
	}
	if count == 0 {
		return fmt.Errorf("provide exactly one of: <task-id>, --state, or --all")
	}
	if count > 1 {
		return fmt.Errorf("provide only one of: <task-id>, --state, or --all")
	}
	return nil
}

// selectClearTasks resolves the tasks targeted by the given selectors. An empty
// state with no id (the --all case) returns every task.
func selectClearTasks(store *storage.SQLiteStore, taskID, state string) ([]storage.Task, error) {
	if taskID != "" {
		all, err := store.QueryTasks(storage.TaskFilter{})
		if err != nil {
			return nil, fmt.Errorf("query tasks: %w", err)
		}
		for _, t := range all {
			if t.ID == taskID {
				return []storage.Task{t}, nil
			}
		}
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	filter := storage.TaskFilter{}
	if state != "" {
		filter.States = []string{state}
	}
	tasks, err := store.QueryTasks(filter)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}
	return tasks, nil
}

// clearTasks removes each task's worktree (best-effort) and deletes its row.
// Returns the number of task rows successfully deleted. Worktree-removal
// failures (e.g. an already-pruned worktree) are reported but not fatal.
func clearTasks(store *storage.SQLiteStore, root string, tasks []storage.Task) int {
	projectName := module.ProjectName(root)
	n := 0
	for _, t := range tasks {
		if t.Branch != "" {
			if err := module.RemoveWorktree(root, projectName, t.Branch, true); err != nil {
				fmt.Fprintf(os.Stderr, "warn: remove worktree for %s (%s): %v\n", t.ID, t.Branch, err)
			}
		}
		if err := store.DeleteTask(t.ID); err != nil {
			fmt.Fprintf(os.Stderr, "warn: delete task %s: %v\n", t.ID, err)
			continue
		}
		n++
	}
	return n
}
