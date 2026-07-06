package cmd

import (
	"fmt"

	"github.com/futureboard-dev/colony/pkg/config"
	"github.com/futureboard-dev/colony/pkg/module"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create .colony/config.json in the current project",
	RunE:  runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	root, err := module.FindRoot()
	if err != nil {
		return err
	}
	if err := config.Init(root); err != nil {
		return err
	}
	fmt.Printf("✓ Created .colony/config.json\n")
	fmt.Printf("  Edit provider/model/roles to configure multi-model orchestration.\n")
	return nil
}

// loadConfig is a shared helper for all commands that need project config.
func loadConfig() (*config.Config, string, error) {
	root, err := module.FindRoot()
	if err != nil {
		return nil, "", err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, "", err
	}
	return cfg, root, nil
}
