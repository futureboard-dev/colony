package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var loopStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Signal the running loop to stop after the current task",
	RunE: func(cmd *cobra.Command, args []string) error {
		sentinel := filepath.Join(".colony", "loop.stop")
		if err := os.MkdirAll(filepath.Dir(sentinel), 0755); err != nil {
			return fmt.Errorf("create .colony dir: %w", err)
		}
		if err := os.WriteFile(sentinel, nil, 0644); err != nil {
			return fmt.Errorf("write sentinel: %w", err)
		}
		fmt.Println("✓ stop signal sent to loop (will stop after current task)")
		return nil
	},
}

func init() {
	loopCmd.AddCommand(loopStopCmd)
}
