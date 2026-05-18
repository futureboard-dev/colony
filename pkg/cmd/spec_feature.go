package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jirateep/colony/pkg/llm"
	"github.com/jirateep/colony/pkg/module"
	"github.com/jirateep/colony/pkg/prompt"
	"github.com/spf13/cobra"
)

var specFeatureCmd = &cobra.Command{
	Use:   "spec-feature [feature-name]",
	Short: "Generate a TASK.md spec for a feature using an LLM",
	Long: `Reads requirements from inline text or a file, calls the LLM to produce
a filled-in Agent Task Spec, and writes it to .colony/specs/<name>/TASK.md.

Examples:
  colony spec_feature add-user-auth "users log in with email and password"
  colony spec_feature payment-flow --file requirements.md`,
	Args: cobra.ExactArgs(1),
	RunE: runSpecFeature,
}

var (
	sfFile        string
	sfInteractive bool
)

func init() {
	specFeatureCmd.Flags().StringVar(&sfFile, "file", "", "read requirements from this file instead of inline text")
	specFeatureCmd.Flags().BoolVar(&sfInteractive, "interactive", false, "collaborate on the spec in a live agent session instead of one-shot generation")
	rootCmd.AddCommand(specFeatureCmd)
}

func runSpecFeature(cmd *cobra.Command, args []string) error {
	featureName := args[0]

	var input string
	if sfFile != "" {
		data, err := os.ReadFile(sfFile)
		if err != nil {
			return fmt.Errorf("read requirements file: %w", err)
		}
		input = string(data)
	} else {
		// remaining words after feature name come from stdin prompt — but
		// cobra gives us only 1 arg (ExactArgs(1)), so read extra from flags.
		// For inline text the user passes it as the single quoted arg.
		input = featureName
	}

	if strings.TrimSpace(input) == "" {
		return fmt.Errorf("requirements are empty — pass text as the argument or use --file")
	}

	cfg, root, err := loadConfig()
	if err != nil {
		return err
	}

	slugName := module.Slugify(featureName)
	if slugName == "" {
		slugName = "feature"
	}
	featureDir := filepath.Join(root, ".colony", "specs", slugName)

	if _, err := os.Stat(featureDir); err == nil {
		return fmt.Errorf("spec directory already exists: %s", featureDir)
	}
	if err := os.MkdirAll(featureDir, 0755); err != nil {
		return fmt.Errorf("cannot create spec directory: %w", err)
	}

	p, err := prompt.SpecFeature(input)
	if err != nil {
		return fmt.Errorf("build prompt: %w", err)
	}

	ex := llm.New(cfg.Role("engineer"))
	taskFile := filepath.Join(featureDir, "TASK.md")

	// Interactive: launch a live agent session (claude or crush) and let the
	// agent write the spec itself, so the user can steer it and answer questions.
	if sfInteractive {
		interactivePrompt := fmt.Sprintf(
			"%s\n\n---\n\nWrite the completed spec to %s, starting with a \"# Feature: %s\" heading. Ask me clarifying questions before writing if anything is ambiguous.",
			p, taskFile, slugName)
		fmt.Printf("%sLaunching interactive spec session for %q...%s\n", ansiCyan, slugName, ansiReset)
		if err := ex.RunInteractive(root, interactivePrompt); err != nil {
			os.RemoveAll(featureDir)
			return fmt.Errorf("interactive session: %w", err)
		}
		if _, err := os.Stat(taskFile); err != nil {
			os.RemoveAll(featureDir)
			return fmt.Errorf("interactive session ended without writing %s", taskFile)
		}
		fmt.Printf("%s✓ Created feature: %s%s\n", ansiGreen, slugName, ansiReset)
		fmt.Printf("  Task file: %s\n", taskFile)
		return nil
	}

	f, err := os.Create(taskFile)
	if err != nil {
		return fmt.Errorf("create TASK.md: %w", err)
	}
	defer f.Close()

	fmt.Fprintf(f, "# Feature: %s\n\n", slugName)

	fmt.Printf("%sGenerating spec for %q...%s\n", ansiCyan, slugName, ansiReset)
	if err := ex.RunHeadless(cmd.Context(), root, p, f); err != nil {
		os.RemoveAll(featureDir)
		return fmt.Errorf("llm: %w", err)
	}

	fmt.Printf("%s✓ Created feature: %s%s\n", ansiGreen, slugName, ansiReset)
	fmt.Printf("  Task file: %s\n", taskFile)
	return nil
}
