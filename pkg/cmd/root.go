package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Set via -ldflags at build time.
var (
	Version   = "dev"
	CommitSHA = "unknown"
	BuildDate = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "colony",
	Short: "Agentic engineering toolkit — orchestrate multi-model agent pipelines",
	Long: `Colony orchestrates multi-role agent pipelines across any LLM provider.

Agents run in isolated git worktrees. Each role (coordinator, engineer, reviewer)
can use a different model. Provider switching is a config change, not a code change.

Providers: anthropic → claude CLI, everything else → crush CLI.

Quick start:
  colony init
  colony task "fix the login bug"
  colony craft --spec SPEC.md --lang typescript
  colony swarm --spec SPEC.md --lang go --mode full`,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version, commit, and build date",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("colony %s (commit %s, built %s)\n", Version, CommitSHA, BuildDate)
	},
}

const banner = `
  ○         ○
   ╲       ╱
○───●─────●───○      C o l o n y
   ╱       ╲
  ○         ○

`

func Execute() {
	fmt.Print(banner)
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(craftCmd)
	rootCmd.AddCommand(swarmCmd)
	rootCmd.AddCommand(logCmd)
	rootCmd.AddCommand(reviewCmd)

	taskCmd.AddCommand(taskDoneCmd)
	taskCmd.AddCommand(taskListCmd)
	rootCmd.AddCommand(taskCmd)

	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(uninstallCmd)
	rootCmd.AddCommand(missionCmd)
	rootCmd.AddCommand(loopCmd)
	rootCmd.AddCommand(gateCmd)
}
