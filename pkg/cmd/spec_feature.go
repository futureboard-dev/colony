package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jirateep/colony/pkg/llm"
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

var sfFile string

func init() {
	specFeatureCmd.Flags().StringVar(&sfFile, "file", "", "read requirements from this file instead of inline text")
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

	slugName := slugify(featureName)
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

func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")

	var result strings.Builder
	var lastChar rune
	for i, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			if r == '-' && lastChar == '-' {
				continue
			}
			result.WriteRune(r)
			lastChar = r
		}
		if i == len(s)-1 && r == '-' {
			break
		}
	}

	slug := result.String()
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "feature"
	}
	return slug
}
