package cmd

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/futureboard-dev/colony/pkg/config"
	"github.com/futureboard-dev/colony/pkg/llm"
	"github.com/futureboard-dev/colony/pkg/module"
	"github.com/futureboard-dev/colony/pkg/prompt"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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
	sfFile        []string
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
	specFeatureCmd.Flags().StringSliceVar(&sfFile, "file", nil, "read requirements from this file instead of inline text; repeat or comma-separate to combine several files")
	specFeatureCmd.Flags().BoolVar(&sfInteractive, "interactive", false, "collaborate on the spec in a live agent session instead of one-shot generation")
	specFeatureCmd.Flags().BoolVar(&sfContinue, "continue", false, "revise an existing TASK.md using inline comments you added to it")
	specFeatureCmd.Flags().StringVar(&sfProvider, "provider", "deepseek", "model provider for spec generation: deepseek (default, deepseek-reasoner) or anthropic (claude-opus-4-8)")
	specFeatureCmd.Flags().BoolVar(&sfSplit, "split", false, "split the spec into dependent part briefs under parts/ plus PLAN.md; with --file, split that spec as-is")
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
	if len(sfFile) > 0 {
		data, err := readSpecFiles(sfFile)
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

// readSpecFiles reads the --file inputs. A single file is returned unchanged
// so a hand-written spec is split as-is; several files are joined in order,
// each under a "## <path>" heading so the LLM can tell their sources apart.
func readSpecFiles(paths []string) ([]byte, error) {
	if len(paths) == 1 {
		return os.ReadFile(paths[0])
	}
	var b strings.Builder
	for i, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "## %s\n\n%s", p, strings.TrimRight(string(data), "\n"))
	}
	b.WriteString("\n")
	return []byte(b.String()), nil
}

// runSpecFeatureSplit implements `spec-feature <name> --split`. With --file,
// that spec is copied to SOURCE.md and split as-is. Otherwise an existing
// TASK.md is split, or one is generated first from inline requirements,
// exactly as plain spec-feature would. It writes parts/*.md briefs and PLAN.md.
func runSpecFeatureSplit(cmd *cobra.Command, args []string, cfg *config.Config, root, slugName, featureDir, taskFile string) error {
	// sourceFile is the spec the parts point into. A --file spec is split as-is
	// (copied to SOURCE.md) so a long hand-written spec is never rewritten.
	sourceFile := taskFile
	if len(sfFile) > 0 {
		sourceFile = filepath.Join(featureDir, "SOURCE.md")
	}

	partsDir := filepath.Join(featureDir, "parts")
	if _, err := os.Stat(partsDir); err == nil && !sfForce {
		return fmt.Errorf("parts directory already exists: %s (use --force to overwrite)", partsDir)
	}

	if len(sfFile) > 0 {
		data, err := readSpecFiles(sfFile)
		if err != nil {
			return fmt.Errorf("read spec file: %w", err)
		}
		if err := os.MkdirAll(featureDir, 0755); err != nil {
			return fmt.Errorf("cannot create spec directory: %w", err)
		}
		if err := os.WriteFile(sourceFile, data, 0644); err != nil {
			return fmt.Errorf("write SOURCE.md: %w", err)
		}
	} else if _, err := os.Stat(taskFile); err != nil {
		var input string
		if len(args) > 1 {
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

	specData, err := os.ReadFile(sourceFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", sourceFile, err)
	}

	splitPrompt, err := prompt.SpecSplit(string(specData), repoFileList(root), repoConventions(root))
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
	source := filepath.Join("..", filepath.Base(sourceFile))
	for i := range parts {
		parts[i].Source = source
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

	var plan strings.Builder
	fmt.Fprintf(&plan, "# Plan: %s\n\nSource: %s\n\n## Waves\n\n", slugName, filepath.Base(sourceFile))
	writeWaves(&plan, parts)
	if llmPlan := parsePlan(out.String()); llmPlan != "" {
		fmt.Fprintf(&plan, "\n## Plan\n\n%s\n", llmPlan)
	}
	if len(warnings) > 0 {
		plan.WriteString("\n## Warnings\n\n")
		for _, w := range warnings {
			fmt.Fprintf(&plan, "- %s\n", w)
		}
	}
	planFile := filepath.Join(featureDir, "PLAN.md")
	if err := os.WriteFile(planFile, []byte(plan.String()), 0644); err != nil {
		return fmt.Errorf("write PLAN.md: %w", err)
	}

	for _, w := range warnings {
		fmt.Printf("%s⚠ %s%s\n", ansiYellow, w, ansiReset)
	}

	fmt.Printf("%s✓ Split %s into %d parts%s\n", ansiGreen, slugName, len(parts), ansiReset)
	writeWaves(os.Stdout, parts)
	fmt.Printf("  Plan: %s\n", planFile)
	fmt.Printf("  Next: colony task add --group %s --lang <lang>\n", featureDir)
	return nil
}

// formatPartFile renders one part as a frontmatter-prefixed brief: the
// frontmatter is the source of truth `task add --group` reads back; the body
// is the part's brief, which points into the source spec.
func formatPartFile(p specPart) string {
	var fm strings.Builder
	enc := yaml.NewEncoder(&fm)
	enc.SetIndent(2)
	// Encoding plain strings and string slices cannot fail.
	_ = enc.Encode(partFrontmatter{ID: p.ID, DependsOn: p.DependsOn, Source: p.Source, Owns: p.Owns, Reserves: p.Reserves})
	_ = enc.Close()
	return "---\n" + fm.String() + "---\n" + p.Body + "\n"
}

// partFrontmatter is a part file's header. It is real YAML, written and read
// with a YAML library, so paths with braces, commas or backticks stay intact
// and editors that parse frontmatter accept it.
type partFrontmatter struct {
	ID        string   `yaml:"id"`
	DependsOn []string `yaml:"depends_on"`
	Source    string   `yaml:"source,omitempty"`
	Owns      []string `yaml:"owns,omitempty"`
	Reserves  []string `yaml:"reserves,omitempty"`
}

// partWaves groups parts by how early they can start: wave 1 has no
// dependencies, and every other part sits one wave after its latest
// dependency. Parts in the same wave can build in parallel. The graph must
// already be validated (no cycles, no unknown dependencies).
func partWaves(parts []specPart) [][]specPart {
	byID := make(map[string]specPart, len(parts))
	for _, p := range parts {
		byID[p.ID] = p
	}
	wave := make(map[string]int, len(parts))
	var waveOf func(id string) int
	waveOf = func(id string) int {
		if w, ok := wave[id]; ok {
			return w
		}
		w := 1
		for _, d := range byID[id].DependsOn {
			w = max(w, waveOf(d)+1)
		}
		wave[id] = w
		return w
	}

	var waves [][]specPart
	for _, p := range parts {
		w := waveOf(p.ID)
		for len(waves) < w {
			waves = append(waves, nil)
		}
		waves[w-1] = append(waves[w-1], p)
	}
	return waves
}

// criticalPath returns the longest dependency chain, which bounds how fast
// the group can finish however many parts run in parallel.
func criticalPath(parts []specPart) []string {
	waves := partWaves(parts)
	if len(waves) == 0 {
		return nil
	}
	waveOf := make(map[string]int, len(parts))
	byID := make(map[string]specPart, len(parts))
	for i, w := range waves {
		for _, p := range w {
			waveOf[p.ID] = i + 1
			byID[p.ID] = p
		}
	}
	cur := waves[len(waves)-1][0]
	path := []string{cur.ID}
	for len(cur.DependsOn) > 0 {
		for _, d := range cur.DependsOn {
			if waveOf[d] == waveOf[cur.ID]-1 {
				cur = byID[d]
				break
			}
		}
		path = append([]string{cur.ID}, path...)
	}
	return path
}

// writeWaves renders the waves and critical path shown after a split and
// saved in PLAN.md.
func writeWaves(out io.Writer, parts []specPart) {
	for i, w := range partWaves(parts) {
		fmt.Fprintf(out, "  Wave %d\n", i+1)
		for _, p := range w {
			if len(p.DependsOn) == 0 {
				fmt.Fprintf(out, "    %s\n", p.ID)
				continue
			}
			deps := make([]string, len(p.DependsOn))
			for j, d := range p.DependsOn {
				deps[j] = partNum(d)
			}
			fmt.Fprintf(out, "    %-20s (after %s)\n", p.ID, strings.Join(deps, ", "))
		}
	}
	fmt.Fprintf(out, "  Critical path: %s\n", strings.Join(criticalPath(parts), " → "))
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
	Owns      []string // files or directories only this part may edit
	Reserves  []string // sequence numbers or names only this part may use
	Source    string   // source spec path, relative to the part file
	Body      string   // the brief for this part
}

var (
	partHeaderRe    = regexp.MustCompile(`^## PART (\d{2}): (.+)$`)
	partHeaderLoose = regexp.MustCompile(`^## PART\b`)
	dependsOnRe     = regexp.MustCompile(`^Depends-On:\s*(.+)$`)
	planHeaderRe    = regexp.MustCompile(`^## PLAN\s*$`)
)

// parsePlan returns the body of the split output's "## PLAN" section (up to
// the first part header), or "" when the LLM gave none.
func parsePlan(output string) string {
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		if !planHeaderRe.MatchString(strings.TrimRight(line, "\r")) {
			continue
		}
		end := i + 1
		for end < len(lines) && !partHeaderLoose.MatchString(strings.TrimRight(lines[end], "\r")) {
			end++
		}
		return strings.TrimSpace(strings.Join(lines[i+1:end], "\n"))
	}
	return ""
}

// splitList parses a comma-separated header value, treating "none" as empty.
func splitList(v string) []string {
	v = strings.TrimSpace(v)
	if strings.EqualFold(v, "none") {
		return nil
	}
	var items []string
	for item := range strings.SplitSeq(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// parseParts parses the LLM's split output into parts, mirroring the pattern
// of parseSubtasks in pkg/cmd/swarm.go. Each part starts with a
// "## PART NN: <slug>" header, followed immediately by a "Depends-On: ..."
// line, then optional "Owns: ..." and "Reserves: ..." lines, then the part's
// brief up to the next header (or end of input). Depends-On part numbers are resolved to full part IDs
// ("01-models") using the numbers seen in this same output.
func parseParts(output string) ([]specPart, error) {
	lines := strings.Split(output, "\n")

	type rawPart struct {
		num, slug      string
		deps           []string
		owns, reserves []string
		body           string
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
		deps := splitList(dm[1])
		i++

		var owns, reserves []string
		for i < len(lines) {
			line := strings.TrimSpace(lines[i])
			if v, ok := strings.CutPrefix(line, "Owns:"); ok {
				owns = splitList(v)
			} else if v, ok := strings.CutPrefix(line, "Reserves:"); ok {
				reserves = splitList(v)
			} else {
				break
			}
			i++
		}

		bodyStart := i
		for i < len(lines) && !partHeaderLoose.MatchString(strings.TrimRight(lines[i], "\r")) {
			i++
		}
		body := strings.TrimSpace(strings.Join(lines[bodyStart:i], "\n"))
		raws = append(raws, rawPart{num: num, slug: slug, deps: deps, owns: owns, reserves: reserves, body: body})
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
			Owns:      r.owns,
			Reserves:  r.reserves,
			Body:      r.body,
		})
	}
	return parts, nil
}

// maxRepoFiles caps the file list given to the split prompt, so a huge repo
// can't blow the context; the split only needs enough to name real paths.
const maxRepoFiles = 3000

// repoFileList returns the repo's tracked files (git ls-files) for the split
// prompt. The split runs without file tools, so this is how it sees real
// paths and the next free migration number. Outside a git repo it returns "".
func repoFileList(root string) string {
	out, err := exec.Command("git", "-C", root, "ls-files").Output()
	if err != nil {
		return ""
	}
	files := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(files) > maxRepoFiles {
		more := len(files) - maxRepoFiles
		files = append(files[:maxRepoFiles], fmt.Sprintf("... (%d more files not shown)", more))
	}
	return strings.Join(files, "\n")
}

// maxConventionsBytes caps how much of the repo's CLAUDE.md goes into the
// split prompt.
const maxConventionsBytes = 20000

// repoConventions returns the repo's CLAUDE.md for the split prompt, so the
// split places new code where the repo's rules say it goes (it sees only file
// names otherwise). A repo without one returns "".
func repoConventions(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		return ""
	}
	if len(data) > maxConventionsBytes {
		return string(data[:maxConventionsBytes]) + "\n... (truncated)"
	}
	return string(data)
}

// validateParts checks the part graph parseParts produced: unique IDs,
// dependencies that resolve to a known part, and no cycles (via topological
// sort) are errors. A path owned by two parts (including a "dir/**" that
// covers another part's path), a number reserved by two parts, and an
// item that looks like a placeholder or a description, are reported as warnings only — the caller decides whether to surface them.
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
	type claim struct{ path, part string }
	var claims []claim
	reservedBy := make(map[string]string)
	for _, p := range parts {
		for _, item := range append(append([]string{}, p.Owns...), p.Reserves...) {
			if strings.ContainsAny(item, "{} \t") {
				warnings = append(warnings, fmt.Sprintf("part %q lists %q, which looks like a placeholder or a description, not a path or name", p.ID, item))
			}
		}
		for _, f := range p.Owns {
			claims = append(claims, claim{f, p.ID})
		}
		for _, r := range p.Reserves {
			if owner, ok := reservedBy[r]; ok && owner != p.ID {
				warnings = append(warnings, fmt.Sprintf("%q is reserved by both part %q and part %q", r, owner, p.ID))
				continue
			}
			reservedBy[r] = p.ID
		}
	}
	for i, a := range claims {
		for _, b := range claims[i+1:] {
			switch {
			case a.part == b.part:
			case a.path == b.path:
				warnings = append(warnings, fmt.Sprintf("%q is owned by both part %q and part %q", a.path, a.part, b.part))
			case ownsCover(a.path, b.path) || ownsCover(b.path, a.path):
				warnings = append(warnings, fmt.Sprintf("%q (part %q) overlaps %q (part %q)", a.path, a.part, b.path, b.part))
			}
		}
	}
	return warnings, nil
}

// ownsCover reports whether owns entry dir, a directory written "dir/**",
// covers path: the directory itself or anything under it, including a
// nested "dir/sub/**".
func ownsCover(dir, path string) bool {
	d, ok := strings.CutSuffix(dir, "/**")
	return ok && (path == d || strings.HasPrefix(path, d+"/"))
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
