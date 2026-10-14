package cmd

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/futureboard-dev/colony/pkg/config"
	"github.com/futureboard-dev/colony/pkg/llm"
	"github.com/futureboard-dev/colony/pkg/module"
	"github.com/futureboard-dev/colony/pkg/prompt"
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
	Args: cobra.RangeArgs(1, 2),
	RunE: runSpecFeature,
}

var (
	sfFile        string
	sfInteractive bool
	sfContinue    bool
	sfProvider    string
	sfSplit       bool
	sfForce       bool
)

// specFeatureLLM runs a spec-feature prompt (generate, revise, or split) and
// writes its output to out. It is a package-level seam so tests can supply
// fixture output instead of shelling out to a live agent CLI.
var specFeatureLLM = func(cmd *cobra.Command, ex *llm.Executor, root, prompt string, out io.Writer) error {
	return ex.RunHeadless(cmd.Context(), root, prompt, out)
}

func init() {
	specFeatureCmd.Flags().StringVar(&sfFile, "file", "", "read requirements from this file instead of inline text")
	specFeatureCmd.Flags().BoolVar(&sfInteractive, "interactive", false, "collaborate on the spec in a live agent session instead of one-shot generation")
	specFeatureCmd.Flags().BoolVar(&sfContinue, "continue", false, "revise an existing TASK.md using inline comments you added to it")
	specFeatureCmd.Flags().StringVar(&sfProvider, "provider", "deepseek", "model provider for spec generation: deepseek (default, deepseek-reasoner) or anthropic (claude-opus-4-8)")
	specFeatureCmd.Flags().BoolVar(&sfSplit, "split", false, "split TASK.md into dependent parts under parts/ for parallel building")
	specFeatureCmd.Flags().BoolVar(&sfForce, "force", false, "overwrite an existing parts/ directory")
	rootCmd.AddCommand(specFeatureCmd)
}

// providerLLM selects the LLM config for a command's --provider flag:
// "deepseek" (the default) uses the project's default LLM; "anthropic" uses
// the given role.
func providerLLM(cfg *config.Config, provider, anthropicRole string) config.LLMConfig {
	if provider == "anthropic" {
		return cfg.Role(anthropicRole)
	}
	return cfg.LLM
}

// validateProvider rejects unknown --provider values.
func validateProvider(provider string) error {
	if provider != "deepseek" && provider != "anthropic" {
		return fmt.Errorf("invalid --provider %q: use \"deepseek\" or \"anthropic\"", provider)
	}
	return nil
}

func runSpecFeature(cmd *cobra.Command, args []string) error {
	if err := validateProvider(sfProvider); err != nil {
		return err
	}

	featureName := args[0]

	cfg, root, err := loadConfig()
	if err != nil {
		return err
	}

	slugName := module.Slugify(featureName)
	if slugName == "" {
		slugName = "feature"
	}
	featureDir := filepath.Join(root, ".colony", "specs", slugName)
	taskFile := filepath.Join(featureDir, "TASK.md")

	if sfContinue {
		return runSpecFeatureContinue(cmd, cfg, root, slugName, taskFile)
	}

	if sfSplit {
		return runSpecFeatureSplit(cmd, args, cfg, root, slugName, featureDir, taskFile)
	}

	var input string
	if sfFile != "" {
		data, err := os.ReadFile(sfFile)
		if err != nil {
			return fmt.Errorf("read requirements file: %w", err)
		}
		input = string(data)
	} else if len(args) > 1 {
		input = args[1]
	}

	if strings.TrimSpace(input) == "" {
		return fmt.Errorf("requirements are empty — pass text as the argument or use --file")
	}

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

	ex := llm.New(providerLLM(cfg, sfProvider, "spec"))

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
		if generated, err := os.ReadFile(taskFile); err == nil {
			hashFile := strings.TrimSuffix(taskFile, ".md") + ".hash"
			_ = os.WriteFile(hashFile, []byte(specHash(generated)), 0644)
		}
		fmt.Printf("%s✓ Created feature: %s%s\n", ansiGreen, slugName, ansiReset)
		fmt.Printf("  Task file: %s\n", taskFile)
		return nil
	}

	f, err := os.Create(taskFile)
	if err != nil {
		return fmt.Errorf("create TASK.md: %w", err)
	}
	fmt.Fprintf(f, "# Feature: %s\n\n", slugName)

	fmt.Printf("%sGenerating spec for %q...%s\n", ansiCyan, slugName, ansiReset)
	if err := specFeatureLLM(cmd, ex, root, p, f); err != nil {
		f.Close()
		os.RemoveAll(featureDir)
		return fmt.Errorf("llm: %w", err)
	}
	if err := f.Close(); err != nil {
		os.RemoveAll(featureDir)
		return fmt.Errorf("write TASK.md: %w", err)
	}

	// Record hash so --continue can detect edits.
	if generated, err := os.ReadFile(taskFile); err == nil {
		hashFile := strings.TrimSuffix(taskFile, ".md") + ".hash"
		_ = os.WriteFile(hashFile, []byte(specHash(generated)), 0644)
	}

	fmt.Printf("%s✓ Created feature: %s%s\n", ansiGreen, slugName, ansiReset)
	fmt.Printf("  Task file: %s\n", taskFile)
	return nil
}

// runSpecFeatureSplit implements `spec-feature <name> --split`. If TASK.md
// exists it is split as-is; otherwise it is generated first (requirements
// required), exactly as plain spec-feature would, and then split.
func runSpecFeatureSplit(cmd *cobra.Command, args []string, cfg *config.Config, root, slugName, featureDir, taskFile string) error {
	if _, err := os.Stat(taskFile); err != nil {
		var input string
		if sfFile != "" {
			data, err := os.ReadFile(sfFile)
			if err != nil {
				return fmt.Errorf("read requirements file: %w", err)
			}
			input = string(data)
		} else if len(args) > 1 {
			input = args[1]
		}
		if strings.TrimSpace(input) == "" {
			return fmt.Errorf("requirements are empty — pass text as the argument or use --file")
		}
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
		ex := llm.New(providerLLM(cfg, sfProvider, "spec"))

		f, err := os.Create(taskFile)
		if err != nil {
			return fmt.Errorf("create TASK.md: %w", err)
		}
		fmt.Fprintf(f, "# Feature: %s\n\n", slugName)

		fmt.Printf("%sGenerating spec for %q...%s\n", ansiCyan, slugName, ansiReset)
		if err := specFeatureLLM(cmd, ex, root, p, f); err != nil {
			f.Close()
			os.RemoveAll(featureDir)
			return fmt.Errorf("llm: %w", err)
		}
		if err := f.Close(); err != nil {
			os.RemoveAll(featureDir)
			return fmt.Errorf("write TASK.md: %w", err)
		}
		if generated, err := os.ReadFile(taskFile); err == nil {
			hashFile := strings.TrimSuffix(taskFile, ".md") + ".hash"
			_ = os.WriteFile(hashFile, []byte(specHash(generated)), 0644)
		}
	}

	partsDir := filepath.Join(featureDir, "parts")
	if _, err := os.Stat(partsDir); err == nil && !sfForce {
		return fmt.Errorf("parts directory already exists: %s (use --force to overwrite)", partsDir)
	}

	specData, err := os.ReadFile(taskFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", taskFile, err)
	}

	splitPrompt, err := prompt.SpecSplit(string(specData))
	if err != nil {
		return fmt.Errorf("build split prompt: %w", err)
	}

	ex := llm.New(providerLLM(cfg, sfProvider, "spec"))
	var out strings.Builder
	fmt.Printf("%sSplitting %q into parts...%s\n", ansiCyan, slugName, ansiReset)
	if err := specFeatureLLM(cmd, ex, root, splitPrompt, &out); err != nil {
		return fmt.Errorf("llm: %w", err)
	}

	parts, err := parseParts(out.String())
	if err != nil {
		return fmt.Errorf("parse split output: %w", err)
	}

	warnings, err := validateParts(parts)
	if err != nil {
		return fmt.Errorf("validate parts: %w", err)
	}

	if err := os.RemoveAll(partsDir); err != nil {
		return fmt.Errorf("clear parts directory: %w", err)
	}
	if err := os.MkdirAll(partsDir, 0755); err != nil {
		return fmt.Errorf("create parts directory: %w", err)
	}
	for _, p := range parts {
		path := filepath.Join(partsDir, p.ID+".md")
		if err := os.WriteFile(path, []byte(formatPartFile(p)), 0644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}

	for _, w := range warnings {
		fmt.Printf("%s⚠ %s%s\n", ansiYellow, w, ansiReset)
	}

	fmt.Printf("%s✓ Split %s into %d parts%s\n", ansiGreen, slugName, len(parts), ansiReset)
	printPartsTree(os.Stdout, parts)
	fmt.Printf("  Next: colony task add --group %s --lang <lang>\n", featureDir)
	return nil
}

// formatPartFile renders one part as a frontmatter-prefixed Agent Task Spec
// file: the frontmatter (id, depends_on) is the source of truth `task add
// --group` reads back; the body is the part's own spec.
func formatPartFile(p specPart) string {
	return fmt.Sprintf("---\nid: %s\ndepends_on: [%s]\n---\n%s\n", p.ID, strings.Join(p.DependsOn, ", "), p.Body)
}

// printPartsTree renders the dependency tree shown after a successful split:
// parts with no dependencies at the top, then every dependent part annotated
// with the part numbers it waits on.
func printPartsTree(out io.Writer, parts []specPart) {
	var roots, rest []specPart
	for _, p := range parts {
		if len(p.DependsOn) == 0 {
			roots = append(roots, p)
		} else {
			rest = append(rest, p)
		}
	}
	for _, p := range roots {
		fmt.Fprintf(out, "  %s\n", p.ID)
	}
	for i, p := range rest {
		branch := "├─"
		if i == len(rest)-1 {
			branch = "└─"
		}
		deps := make([]string, len(p.DependsOn))
		for j, d := range p.DependsOn {
			deps[j] = partNum(d)
		}
		fmt.Fprintf(out, "  %s %-14s (after %s)\n", branch, p.ID, strings.Join(deps, ", "))
	}
}

// partNum extracts the two-digit number prefix from a part ID like "02-api".
func partNum(id string) string {
	if before, _, ok := strings.Cut(id, "-"); ok {
		return before
	}
	return id
}

func runSpecFeatureContinue(cmd *cobra.Command, cfg *config.Config, root, slugName, taskFile string) error {
	data, err := os.ReadFile(taskFile)
	if err != nil {
		return fmt.Errorf("read existing spec: %w — run spec-feature %s first", err, slugName)
	}

	// Skip LLM if the file hasn't changed since the last generation/revision.
	hashFile := strings.TrimSuffix(taskFile, ".md") + ".hash"
	current := specHash(data)
	if saved, err := os.ReadFile(hashFile); err == nil && strings.TrimSpace(string(saved)) == current {
		fmt.Printf("%sSpec %q is unchanged — nothing to revise.%s\n", ansiCyan, slugName, ansiReset)
		fmt.Printf("  Add comments to %s then re-run.\n", taskFile)
		return nil
	}

	p, err := prompt.SpecFeatureRevise(string(data))
	if err != nil {
		return fmt.Errorf("build prompt: %w", err)
	}

	// Write revised output to a temp file, then atomically replace the original.
	tmp, err := os.CreateTemp(filepath.Dir(taskFile), "TASK.*.md")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { os.Remove(tmpName) }()

	ex := llm.New(providerLLM(cfg, sfProvider, "spec"))
	fmt.Printf("%sRevising spec for %q...%s\n", ansiCyan, slugName, ansiReset)
	if err := specFeatureLLM(cmd, ex, root, p, tmp); err != nil {
		tmp.Close()
		return fmt.Errorf("llm: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write temp spec: %w", err)
	}

	if err := os.Rename(tmpName, taskFile); err != nil {
		return fmt.Errorf("replace TASK.md: %w", err)
	}

	// Record the hash of the freshly-revised spec so subsequent --continue
	// calls without edits are skipped.
	revised, _ := os.ReadFile(taskFile)
	_ = os.WriteFile(hashFile, []byte(specHash(revised)), 0644)

	fmt.Printf("%s✓ Revised spec: %s%s\n", ansiGreen, slugName, ansiReset)
	fmt.Printf("  Task file: %s\n", taskFile)
	return nil
}

func specHash(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// specPart is one part produced by parseParts: a slice of a larger Agent Task
// Spec that can be built once its dependencies are done.
type specPart struct {
	ID        string   // "01-models"
	Num       string   // "01"
	Slug      string   // "models"
	DependsOn []string // full part IDs, e.g. ["01-models"]
	Body      string   // the Agent Task Spec body for this part
}

var (
	partHeaderRe    = regexp.MustCompile(`^## PART (\d{2}): (.+)$`)
	partHeaderLoose = regexp.MustCompile(`^## PART\b`)
	dependsOnRe     = regexp.MustCompile(`^Depends-On:\s*(.+)$`)
)

// parseParts parses the LLM's split output into parts, mirroring the pattern
// of parseSubtasks in pkg/cmd/swarm.go. Each part starts with a
// "## PART NN: <slug>" header, followed immediately by a "Depends-On: ..."
// line, followed by the part's Agent Task Spec body up to the next header (or
// end of input). Depends-On part numbers are resolved to full part IDs
// ("01-models") using the numbers seen in this same output.
func parseParts(output string) ([]specPart, error) {
	lines := strings.Split(output, "\n")

	type rawPart struct {
		num, slug string
		deps      []string
		body      string
	}
	var raws []rawPart

	i := 0
	for i < len(lines) {
		line := strings.TrimRight(lines[i], "\r")
		if !partHeaderLoose.MatchString(line) {
			i++
			continue
		}
		m := partHeaderRe.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("malformed part header: %q", line)
		}
		num, slug := m[1], strings.TrimSpace(m[2])
		i++

		for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
			i++
		}
		if i >= len(lines) {
			return nil, fmt.Errorf("part %s: missing Depends-On line", num)
		}
		dm := dependsOnRe.FindStringSubmatch(strings.TrimSpace(lines[i]))
		if dm == nil {
			return nil, fmt.Errorf("part %s: malformed header — expected a Depends-On line, got %q", num, lines[i])
		}
		var deps []string
		depVal := strings.TrimSpace(dm[1])
		if !strings.EqualFold(depVal, "none") {
			for d := range strings.SplitSeq(depVal, ",") {
				if d = strings.TrimSpace(d); d != "" {
					deps = append(deps, d)
				}
			}
		}
		i++

		bodyStart := i
		for i < len(lines) && !partHeaderLoose.MatchString(strings.TrimRight(lines[i], "\r")) {
			i++
		}
		body := strings.TrimSpace(strings.Join(lines[bodyStart:i], "\n"))
		raws = append(raws, rawPart{num: num, slug: slug, deps: deps, body: body})
	}

	if len(raws) == 0 {
		return nil, fmt.Errorf("no parts found in split output")
	}

	numToID := make(map[string]string, len(raws))
	for _, r := range raws {
		numToID[r.num] = r.num + "-" + r.slug
	}

	parts := make([]specPart, 0, len(raws))
	for _, r := range raws {
		var dependsOn []string
		for _, d := range r.deps {
			id, ok := numToID[d]
			if !ok {
				return nil, fmt.Errorf("part %s depends on unknown part %q", r.num, d)
			}
			dependsOn = append(dependsOn, id)
		}
		parts = append(parts, specPart{
			ID:        r.num + "-" + r.slug,
			Num:       r.num,
			Slug:      r.slug,
			DependsOn: dependsOn,
			Body:      r.body,
		})
	}
	return parts, nil
}

// validateParts checks the part graph parseParts produced: unique IDs,
// dependencies that resolve to a known part, and no cycles (via topological
// sort) are errors. Overlapping CREATE/MODIFY file paths across parts are
// reported as warnings only — the caller decides whether to surface them.
func validateParts(parts []specPart) ([]string, error) {
	seen := make(map[string]bool, len(parts))
	for _, p := range parts {
		if seen[p.ID] {
			return nil, fmt.Errorf("duplicate part id %q", p.ID)
		}
		seen[p.ID] = true
	}
	for _, p := range parts {
		for _, d := range p.DependsOn {
			if !seen[d] {
				return nil, fmt.Errorf("part %q depends on unknown part %q", p.ID, d)
			}
		}
	}
	if _, err := topoSortParts(parts); err != nil {
		return nil, err
	}

	var warnings []string
	fileOwner := make(map[string]string)
	for _, p := range parts {
		files := append(extractFileSet(p.Body, "CREATE:", "MODIFY:"), extractFileSet(p.Body, "MODIFY:", "DO NOT TOUCH:")...)
		for _, f := range files {
			if f == "" {
				continue
			}
			if owner, ok := fileOwner[f]; ok && owner != p.ID {
				warnings = append(warnings, fmt.Sprintf("file %q is in scope for both part %q and part %q", f, owner, p.ID))
				continue
			}
			fileOwner[f] = p.ID
		}
	}
	return warnings, nil
}

// topoSortParts returns part IDs in dependency order (a part always follows
// everything it depends on), or an error if the graph has a cycle.
func topoSortParts(parts []specPart) ([]string, error) {
	byID := make(map[string]specPart, len(parts))
	for _, p := range parts {
		byID[p.ID] = p
	}

	const (
		white = iota
		gray
		black
	)
	color := make(map[string]int, len(parts))
	var order []string

	var visit func(id string) error
	visit = func(id string) error {
		switch color[id] {
		case black:
			return nil
		case gray:
			return fmt.Errorf("dependency cycle detected involving part %q", id)
		}
		color[id] = gray
		for _, d := range byID[id].DependsOn {
			if err := visit(d); err != nil {
				return err
			}
		}
		color[id] = black
		order = append(order, id)
		return nil
	}

	for _, p := range parts {
		if err := visit(p.ID); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// extractFileSet reads the bullet-listed file paths between a header line
// (e.g. "CREATE:") and the next section header (e.g. "MODIFY:") in a spec
// body, matching the format spec_feature.md produces. Paths may be backtick
// wrapped ("- `path`: description") or bare ("- path: description").
func extractFileSet(body, header, nextHeader string) []string {
	var files []string
	collecting := false
	for line := range strings.SplitSeq(body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.EqualFold(trimmed, header):
			collecting = true
			continue
		case collecting && strings.EqualFold(trimmed, nextHeader):
			return files
		case collecting && strings.HasPrefix(trimmed, "---"):
			return files
		case collecting && strings.HasPrefix(trimmed, "-"):
			files = append(files, extractBulletPath(trimmed))
		}
	}
	return files
}

// extractBulletPath pulls the file path out of one "- ..." bullet line.
func extractBulletPath(bullet string) string {
	s := strings.TrimSpace(strings.TrimPrefix(bullet, "-"))
	if strings.HasPrefix(s, "`") {
		if idx := strings.Index(s[1:], "`"); idx >= 0 {
			return s[1 : idx+1]
		}
	}
	if before, _, ok := strings.Cut(s, ":"); ok {
		return strings.TrimSpace(before)
	}
	return s
}
