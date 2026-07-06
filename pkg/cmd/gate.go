package cmd

import (
	"fmt"
	"os"

	"github.com/futureboard-dev/colony/pkg/module"
	"github.com/spf13/cobra"
)

var gateCmd = &cobra.Command{
	Use:   "gate",
	Short: "Run quality gates for a language on the current directory",
	Long: `Runs the full gate sequence: format → vet → lint → typecheck → test → build.
Exits 0 on all pass, prints output and exits non-zero on first failure.

Example:
  colony gate --lang go
  colony gate --lang typescript --no-format`,
	RunE: runGate,
}

var (
	gateLang     string
	gateNoFormat bool
)

func init() {
	gateCmd.Flags().StringVar(&gateLang, "lang", "", "language: go, typescript, python (required)")
	gateCmd.Flags().BoolVar(&gateNoFormat, "no-format", false, "skip the format gate")
	_ = gateCmd.MarkFlagRequired("lang")
}

func runGate(cmd *cobra.Command, args []string) error {
	skip := make(map[string]bool)
	if gateNoFormat {
		skip["format"] = true
	}

	output, err := module.RunGateCaptureAll(gateLang, ".", skip)
	if err != nil {
		fmt.Fprint(os.Stderr, output)
		return fmt.Errorf("gate %q failed: %w", gateLang, err)
	}
	fmt.Print(output)
	return nil
}
