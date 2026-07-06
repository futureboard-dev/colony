package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/futureboard-dev/colony/pkg/llm"
	"github.com/futureboard-dev/colony/pkg/module"
	"github.com/spf13/cobra"
)

var taskCmd = &cobra.Command{
	Use:   "task [description]",
	Short: "Create an isolated worktree and open an interactive agent session",
	Long: `Creates a git worktree on a fresh agent branch, writes TASK.md, then launches
an interactive agent session (claude for anthropic, crush for all others).

Subcommands:
  colony task done <branch>   clean up after review
  colony task list             list active agent worktrees`,
	Args:               cobra.ArbitraryArgs,
	DisableFlagParsing: false,
	RunE:               runTask,
}

var taskFrom string
var taskFile string
var taskNoFormat bool

func init() {
	taskCmd.Flags().StringVar(&taskFrom, "from", "", "base branch (default: current or main)")
	taskCmd.Flags().StringVar(&taskFile, "file", "", "path to a spec file to use as the task description")
	taskCmd.Flags().BoolVar(&taskNoFormat, "no-format", false, "skip format gate in loop mode")
}

func runTask(cmd *cobra.Command, args []string) error {
	if len(args) == 0 && taskFile == "" {
		return fmt.Errorf("task description required or --file required\n\nExample:\n  colony task \"fix login bug\"\n  colony task \"add banner component\" --from feature/homepage\n  colony task --file SPEC.md")
	}
	taskDesc := strings.Join(args, " ")

	// If --file is specified, read the file for the description body.
	specPath := taskFile
	if specPath != "" {
		if _, err := os.Stat(specPath); err != nil {
			return fmt.Errorf("spec file not found: %s", specPath)
		}
		if taskDesc == "" {
			data, err := os.ReadFile(specPath)
			if err != nil {
				return fmt.Errorf("read spec file: %w", err)
			}
			taskDesc = string(data)
		}
	}

	cfg, root, err := loadConfig()
	if err != nil {
		return err
	}
	projectName := module.ProjectName(root)
	defaultBranch := module.DefaultBranch()
	currentBranch, _ := module.CurrentBranch("")

	// Determine base branch
	baseBranch := defaultBranch
	if taskFrom != "" {
		baseBranch = taskFrom
	} else if currentBranch != "" && currentBranch != defaultBranch &&
		currentBranch != "main" && currentBranch != "master" && currentBranch != "develop" {
		// On a WIP branch — ask user which base to use
		fmt.Printf("\n⚠  You are on branch: %s\n", currentBranch)
		fmt.Printf("   Default base: %s\n\n", defaultBranch)
		fmt.Printf("Branch from:\n")
		fmt.Printf("  1) %s  (your WIP — task depends on this work)\n", currentBranch)
		fmt.Printf("  2) %s  (default — task is independent)\n\n", defaultBranch)
		fmt.Printf("Choose [1/2] (default: 1): ")
		reader := bufio.NewReader(os.Stdin)
		choice, _ := reader.ReadString('\n')
		if strings.TrimSpace(choice) == "2" {
			baseBranch = defaultBranch
		} else {
			baseBranch = currentBranch
		}
	}

	branch := module.NewBranch(taskDesc)

	fmt.Printf("\n🌿 Setting up isolated agent session...\n")
	fmt.Printf("   Project:  %s\n", projectName)
	fmt.Printf("   Task:     %s\n", taskDesc)
	fmt.Printf("   Branch:   %s\n", branch)
	fmt.Printf("   Base:     %s\n\n", baseBranch)

	worktreePath, err := module.SetupWorktree(root, projectName, branch, baseBranch)
	if err != nil {
		return err
	}
	fmt.Printf("✓ Worktree created: %s\n", worktreePath)

	// Write TASK.md
	taskContent := fmt.Sprintf(`# Agent Task

**Task:** %s
**Branch:** %s
**Started:** %s
**Base:** %s

## Instructions
- Work only on files relevant to this task
- Do NOT run git push
- When done, leave a clear commit message summarising what changed and why
`, taskDesc, branch, time.Now().UTC().Format(time.RFC3339), baseBranch)

	taskFile := filepath.Join(worktreePath, "TASK.md")
	if err := os.WriteFile(taskFile, []byte(taskContent), 0644); err != nil {
		return err
	}
	fmt.Printf("✓ TASK.md written\n")

	// Print next steps before launching
	ex := llm.New(cfg.Role("engineer"))
	fmt.Printf("\n🤖 Launching %s in worktree...\n", ex.CLI())
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")
	fmt.Printf("When done:\n")
	fmt.Printf("  Review:  git diff %s..%s\n", baseBranch, branch)
	fmt.Printf("  Push:    cd %s && git push -u origin %s\n", worktreePath, branch)
	fmt.Printf("  Cleanup: colony task done %s\n\n", branch)

	return ex.RunInteractive(worktreePath, taskContent)
}
