package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/jirateep/colony/pkg/module"
	"github.com/spf13/cobra"
)

var taskDoneCmd = &cobra.Command{
	Use:   "done <branch>",
	Short: "Clean up worktree and local branch after review/merge",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskDone,
}

var taskDoneWorktreeOnly bool

func init() {
	taskDoneCmd.Flags().BoolVar(&taskDoneWorktreeOnly, "worktree-only", false, "remove worktree but keep local branch")
}

func runTaskDone(cmd *cobra.Command, args []string) error {
	branch := args[0]

	_, root, err := loadConfig()
	if err != nil {
		return err
	}
	projectName := module.ProjectName(root)

	fmt.Printf("\n🧹 Cleaning up agent session...\n")
	fmt.Printf("   Branch:   %s\n", branch)
	fmt.Printf("\n")

	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("Have you reviewed, merged, or pushed this branch to origin? [y/N] ")
	answer, _ := reader.ReadString('\n')
	if !strings.EqualFold(strings.TrimSpace(answer), "y") {
		fmt.Println("Aborted. Push or merge first, then run this again.")
		return nil
	}

	if err := module.RemoveWorktree(root, projectName, branch, !taskDoneWorktreeOnly); err != nil {
		return err
	}

	if taskDoneWorktreeOnly {
		fmt.Printf("✓ Worktree removed (branch kept: %s)\n", branch)
	} else {
		fmt.Printf("✓ Worktree and branch removed\n")
	}
	fmt.Printf("\n✅ Agent session cleaned up.\n")
	return nil
}
