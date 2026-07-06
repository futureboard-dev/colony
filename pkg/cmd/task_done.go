package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/futureboard-dev/colony/pkg/module"
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
	input := args[0]

	_, root, err := loadConfig()
	if err != nil {
		return err
	}
	projectName := module.ProjectName(root)
	branch := normalizeBranchArg(input, projectName)

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

// normalizeBranchArg accepts either a branch name (e.g. "agent/foo-...")
// or an absolute worktree path and returns the branch name.
func normalizeBranchArg(input, projectName string) string {
	input = strings.TrimSpace(input)
	if !filepath.IsAbs(input) {
		return input
	}
	abs := filepath.Clean(input)
	marker := string(filepath.Separator) + projectName + string(filepath.Separator)
	if i := strings.Index(abs, marker); i >= 0 {
		return abs[i+len(marker):]
	}
	return filepath.Base(filepath.Dir(abs)) + "/" + filepath.Base(abs)
}
