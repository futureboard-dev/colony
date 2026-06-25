package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jirateep/colony/pkg/module"
	"github.com/jirateep/colony/pkg/storage"
	"github.com/spf13/cobra"
)

var (
	taskAddFile     string
	taskAddBase     string
	taskAddLang     string
	taskAddNoFormat bool
)

// taskAddCmd represents the `colony task add` subcommand.
var taskAddCmd = &cobra.Command{
	Use:   "add [description]",
	Short: "Enqueue a task into the loop queue",
	Long: `Enqueues a new task into the loop queue for processing by colony loop.

Flags:
  --file <path>     Path to a spec file (stored in spec_path)
  --base <branch>   Base branch (stored in base_branch)
  --lang <lang>     Language for gates: typescript, python, go (required)
  --no-format       Comma-joined gate names to skip (e.g. --no-format skips "format")`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		description := strings.Join(args, " ")
		if description == "" && taskAddFile == "" {
			return fmt.Errorf("task description or --file is required")
		}

		if taskAddLang == "" {
			return fmt.Errorf("--lang is required (typescript, python, go)")
		}
		if _, err := module.CommandsFor(taskAddLang); err != nil {
			return err
		}

		specPath := taskAddFile
		if specPath != "" {
			abs, err := filepath.Abs(specPath)
			if err != nil {
				return fmt.Errorf("resolve spec path: %w", err)
			}
			if _, err := os.Stat(abs); err != nil {
				return fmt.Errorf("spec file not found: %s", specPath)
			}
			specPath = abs
		}

		gateOverrides := ""
		if taskAddNoFormat {
			gateOverrides = "format"
		}

		// Resolve .colony/missions.db relative to the current directory.
		_, root, err := loadConfig()
		if err != nil {
			return err
		}

		dbPath := storage.DefaultDBPath()
		if root != "" {
			dbPath = filepath.Join(root, ".colony", "missions.db")
		}
		store, err := storage.Open(dbPath)
		if err != nil {
			return fmt.Errorf("open store: %w", err)
		}
		defer func() { _ = store.Close() }()

		task := storage.Task{
			Description:   description,
			SpecPath:      specPath,
			BaseBranch:    taskAddBase,
			Lang:          taskAddLang,
			GateOverrides: gateOverrides,
			State:         "open",
			CreatedAt:     time.Now(),
		}
		if err := store.InsertTask(task); err != nil {
			return fmt.Errorf("insert task: %w", err)
		}

		fmt.Printf("Task enqueued: %s\n", task.ID)
		return nil
	},
}

func init() {
	taskAddCmd.Flags().StringVar(&taskAddFile, "file", "", "path to a spec file")
	taskAddCmd.Flags().StringVar(&taskAddBase, "base", "", "base branch")
	taskAddCmd.Flags().StringVar(&taskAddLang, "lang", "", "language for gates: typescript, python, go (required)")
	taskAddCmd.Flags().BoolVar(&taskAddNoFormat, "no-format", false, "skip format gate")
}
