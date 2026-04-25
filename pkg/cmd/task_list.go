package cmd

import (
	"fmt"

	"github.com/jirateep/colony/pkg/module"
	"github.com/spf13/cobra"
)

var taskListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all active agent worktrees",
	RunE:  runTaskList,
}

func runTaskList(cmd *cobra.Command, args []string) error {
	worktrees, err := module.ListWorktrees()
	if err != nil {
		return err
	}

	fmt.Printf("\n🤖 Active agent worktrees:\n")
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

	if len(worktrees) == 0 {
		fmt.Printf("  None — no active agent sessions.\n")
	} else {
		for _, wt := range worktrees {
			fmt.Printf("\n  📁 %s\n", wt.Project)
			fmt.Printf("     Branch:  %s\n", wt.Branch)
			if wt.Task != "" {
				fmt.Printf("     Task:    %s\n", wt.Task)
			}
			if wt.Started != "" {
				fmt.Printf("     Started: %s\n", wt.Started)
			}
			fmt.Printf("     Path:    %s\n", wt.Path)
		}
	}

	fmt.Printf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("Commands:\n")
	fmt.Printf("  Start:   colony task \"your task description\"\n")
	fmt.Printf("  Cleanup: colony task done <branch-name>\n")
	return nil
}
