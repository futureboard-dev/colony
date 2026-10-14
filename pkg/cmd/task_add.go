package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/futureboard-dev/colony/pkg/module"
	"github.com/futureboard-dev/colony/pkg/storage"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

var (
	taskAddFile     string
	taskAddBase     string
	taskAddLang     string
	taskAddNoFormat bool
	taskAddGroup    string
)

// taskAddCmd represents the `colony task add` subcommand.
var taskAddCmd = &cobra.Command{
	Use:   "add [description]",
	Short: "Enqueue a task into the loop queue",
	Long: `Enqueues a new task into the loop queue for processing by colony loop.

Flags:
  --file <path>     Path to a spec file (stored in spec_path)
  --base <branch>   Base branch (stored in base_branch)
  --lang <lang>     Language for gates: typescript, python, go, none (required)
  --no-format       Comma-joined gate names to skip (e.g. --no-format skips "format")
  --group <dir>     Enqueue a spec-feature --split spec directory as a dependency-ordered
                    group landing on a shared epic branch (see: colony spec-feature --split)`,
	Args: cobra.ArbitraryArgs,
	RunE: runTaskAdd,
}

func init() {
	taskAddCmd.Flags().StringVar(&taskAddFile, "file", "", "path to a spec file")
	taskAddCmd.Flags().StringVar(&taskAddBase, "base", "", "base branch")
	taskAddCmd.Flags().StringVar(&taskAddLang, "lang", "", "language for gates: typescript, python, go, none (required)")
	taskAddCmd.Flags().BoolVar(&taskAddNoFormat, "no-format", false, "skip format gate")
	taskAddCmd.Flags().StringVar(&taskAddGroup, "group", "", "path to a spec directory (with parts/) to enqueue as a dependency-ordered group")
}

func runTaskAdd(cmd *cobra.Command, args []string) error {
	if taskAddGroup != "" {
		return runTaskAddGroup()
	}

	description := strings.Join(args, " ")
	if description == "" && taskAddFile == "" {
		return fmt.Errorf("task description or --file is required")
	}

	if taskAddLang == "" {
		return fmt.Errorf("--lang is required (typescript, python, go, none)")
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
}

// groupPart is one part read back from a spec-feature --split parts/ file.
// The frontmatter (id, depends_on) is the source of truth, since users may
// hand-edit the part files between --split and `task add --group`.
type groupPart struct {
	ID        string
	DependsOn []string
	Body      string
	Path      string
}

// parsePartFrontmatter reads one parts/NN-<slug>.md file, in the format
// formatPartFile writes: a "---" delimited frontmatter block holding "id:"
// and "depends_on: [...]", followed by the Agent Task Spec body.
func parsePartFrontmatter(path string) (groupPart, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return groupPart{}, err
	}
	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		return groupPart{}, fmt.Errorf("%s: missing frontmatter (expected to start with '---')", path)
	}
	rest := content[len("---\n"):]
	frontmatter, rawBody, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return groupPart{}, fmt.Errorf("%s: unterminated frontmatter", path)
	}
	body := strings.TrimSpace(rawBody)

	p := groupPart{Path: path, Body: body}
	for line := range strings.SplitSeq(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "id:"); ok {
			p.ID = strings.TrimSpace(v)
			continue
		}
		if v, ok := strings.CutPrefix(line, "depends_on:"); ok {
			v = strings.TrimSpace(v)
			v = strings.TrimPrefix(v, "[")
			v = strings.TrimSuffix(v, "]")
			for d := range strings.SplitSeq(v, ",") {
				if d = strings.TrimSpace(d); d != "" {
					p.DependsOn = append(p.DependsOn, d)
				}
			}
		}
	}
	if p.ID == "" {
		return groupPart{}, fmt.Errorf("%s: missing 'id' in frontmatter", path)
	}
	return p, nil
}

// runTaskAddGroup implements `task add --group <spec-dir> --lang <lang>`: it
// reads <spec-dir>/parts/*.md, re-validates the dependency graph (parts may
// have been hand-edited since --split), creates the epic branch, and inserts
// one group row plus one task per part in topological order.
func runTaskAddGroup() error {
	if taskAddLang == "" {
		return fmt.Errorf("--lang is required (typescript, python, go, none)")
	}
	if _, err := module.CommandsFor(taskAddLang); err != nil {
		return err
	}

	specDir, err := filepath.Abs(taskAddGroup)
	if err != nil {
		return fmt.Errorf("resolve spec dir: %w", err)
	}
	partsDir := filepath.Join(specDir, "parts")
	files, err := filepath.Glob(filepath.Join(partsDir, "*.md"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no part files found under %s — run 'colony spec-feature <name> --split' first", partsDir)
	}
	sort.Strings(files)

	groupParts := make([]groupPart, 0, len(files))
	for _, f := range files {
		gp, err := parsePartFrontmatter(f)
		if err != nil {
			return fmt.Errorf("parse part: %w", err)
		}
		groupParts = append(groupParts, gp)
	}

	specParts := make([]specPart, len(groupParts))
	for i, gp := range groupParts {
		specParts[i] = specPart{ID: gp.ID, DependsOn: gp.DependsOn, Body: gp.Body}
	}
	if _, err := validateParts(specParts); err != nil {
		return fmt.Errorf("invalid part graph: %w", err)
	}
	order, err := topoSortParts(specParts)
	if err != nil {
		return err
	}

	byID := make(map[string]groupPart, len(groupParts))
	for _, gp := range groupParts {
		byID[gp.ID] = gp
	}

	_, root, err := loadConfig()
	if err != nil {
		return err
	}

	baseBranch := taskAddBase
	if baseBranch == "" {
		baseBranch = module.DefaultBranch()
	}
	name := filepath.Base(specDir)
	epicBranch := "feature/" + name

	branchCmd := exec.Command("git", "-C", root, "branch", epicBranch, baseBranch)
	if out, err := branchCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create epic branch %q: %w\n%s", epicBranch, err, strings.TrimSpace(string(out)))
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

	groupID := uuid.New().String()
	if err := store.InsertGroup(storage.TaskGroup{
		ID:         groupID,
		Name:       name,
		EpicBranch: epicBranch,
		BaseBranch: baseBranch,
		Lang:       taskAddLang,
		State:      "open",
		CreatedAt:  time.Now(),
	}); err != nil {
		return fmt.Errorf("insert group: %w", err)
	}

	partIDToTaskID := make(map[string]string, len(order))
	taskIDs := make([]string, 0, len(order))
	for _, partID := range order {
		gp := byID[partID]
		taskID := uuid.New().String()
		partIDToTaskID[partID] = taskID

		dependsOnTaskIDs := make([]string, len(gp.DependsOn))
		for i, dep := range gp.DependsOn {
			dependsOnTaskIDs[i] = partIDToTaskID[dep]
		}

		task := storage.Task{
			ID:         taskID,
			SpecPath:   gp.Path,
			BaseBranch: epicBranch,
			Lang:       taskAddLang,
			State:      "open",
			GroupID:    groupID,
			DependsOn:  strings.Join(dependsOnTaskIDs, ","),
			CreatedAt:  time.Now(),
		}
		if err := store.InsertTask(task); err != nil {
			return fmt.Errorf("insert task for part %q: %w", partID, err)
		}
		taskIDs = append(taskIDs, taskID)
	}

	fmt.Printf("Group enqueued: %s\n", groupID)
	for i, partID := range order {
		fmt.Printf("  %s -> %s\n", partID, taskIDs[i])
	}
	return nil
}
