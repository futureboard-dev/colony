package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var specFeatureCmd = &cobra.Command{
	Use:   "spec-feature [feature-name]",
	Short: "Create a new feature directory with Agent Task Spec template",
	Long: `Creates a feature directory at ./colony/features/[feature-name] with a TASK.md
template following the Agent Task Spec format.

Example:
  colony spec-feature add-user-auth
  colony spec-feature "implement payment flow"`,
	Args: cobra.ExactArgs(1),
	RunE: runSpecFeature,
}

func init() {
	rootCmd.AddCommand(specFeatureCmd)
}

func runSpecFeature(cmd *cobra.Command, args []string) error {
	featureName := strings.Join(args, " ")
	slugName := slugify(featureName)

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot determine current directory: %w", err)
	}

	featureDir := filepath.Join(cwd, ".colony", "features", slugName)

	if _, err := os.Stat(featureDir); err == nil {
		return fmt.Errorf("feature directory already exists: %s", featureDir)
	}

	if err := os.MkdirAll(featureDir, 0755); err != nil {
		return fmt.Errorf("cannot create feature directory: %w", err)
	}

	taskContent := fmt.Sprintf(`# Agent Task Spec

## 1. Task (one sentence, one deliverable)
<!-- What is the single thing Claude must produce? -->


---

## 2. Files In Scope

CREATE:
-

MODIFY:
-

DO NOT TOUCH:
-

---

## 3. Done Criteria
<!-- Must be testable. Not "it works" — specific assertions. -->

- [ ]
- [ ]
- [ ]

---

## 4. Explicit Decisions (do not infer these)
<!-- Decisions already made. Claude must not deviate or "improve" these. -->

-
-

---

## 5. Environment Variables Needed

` + "```bash" + `

` + "```" + `

---

## 6. Tests to Write

-

---

## 7. Context
<!-- Paste your full requirements doc / notes below -->

---
`)

	taskFile := filepath.Join(featureDir, "TASK.md")
	if err := os.WriteFile(taskFile, []byte(taskContent), 0644); err != nil {
		return fmt.Errorf("cannot write TASK.md: %w", err)
	}

	fmt.Printf("✓ Created feature: %s\n", slugName)
	fmt.Printf("  Directory: %s\n", featureDir)
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
