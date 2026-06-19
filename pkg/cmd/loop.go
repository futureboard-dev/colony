package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jirateep/colony/pkg/config"
	"github.com/jirateep/colony/pkg/mission"
	"github.com/jirateep/colony/pkg/module"
	"github.com/jirateep/colony/pkg/storage"
	"github.com/spf13/cobra"
)

// loopPollInterval is the sleep between passes in --watch daemon mode.
const loopPollInterval = 30 * time.Second

var loopCmd = &cobra.Command{
	Use:   "loop",
	Short: "Autonomous steward: build→gate→fix loop",
	Long: `Runs the autonomous build-gate-fix loop on open tasks from the queue.
Picks the next actionable task, runs BuildGateFix on it, and records the outcome.

Flags:
  --once          Run a single pass (pick one task, process it, exit)
  --watch         Run as a long-lived daemon (polls queue, observes CI/PR)
  --max-passes    Stop after N total passes (0 = unlimited)
  --max-cycles    Cap the inner fix loop per task (default 5)
  --escalate-to   Model to use for escalation role (default: off)
  --lang          Language for gates (default: go)
  --idle          Consecutive idle passes before stopping (default 10)`,
	RunE: runLoop,
}

var (
	loopOnce       bool
	loopWatch      bool
	loopMaxPasses  int
	loopMaxCycles  int
	loopEscalateTo string
	loopLang       string
	loopIdleLimit  int
	loopRetryBlock bool
	loopReview     bool
)

func init() {
	loopCmd.Flags().BoolVar(&loopOnce, "once", false, "run a single pass and exit")
	loopCmd.Flags().BoolVar(&loopWatch, "watch", false, "run as a long-lived daemon (polls queue, observes CI/PR)")
	loopCmd.Flags().IntVar(&loopMaxPasses, "max-passes", 0, "stop after N total passes (0 = unlimited)")
	loopCmd.Flags().IntVar(&loopMaxCycles, "max-cycles", 5, "cap the inner fix loop per task")
	loopCmd.Flags().StringVar(&loopEscalateTo, "escalate-to", "", "model for escalation role (default: off)")
	loopCmd.Flags().StringVar(&loopLang, "lang", "go", "language for gates")
	loopCmd.Flags().IntVar(&loopIdleLimit, "idle", 10, "consecutive idle passes before stopping")
	loopCmd.Flags().BoolVar(&loopRetryBlock, "retry-blocked", false, "re-queue blocked tasks (needs-fix) to continue them in their existing worktree")
	loopCmd.Flags().BoolVar(&loopReview, "review", false, "run an LLM review gate before marking a task done (also auto-enabled when a 'review' role is configured)")

	loopCmd.AddCommand(loopStatusCmd)
	loopCmd.AddCommand(loopScheduleCmd)
	loopCmd.AddCommand(loopClearCmd)
}

func runLoop(cmd *cobra.Command, args []string) error {
	cfg, root, err := loadConfig()
	if err != nil {
		return err
	}

	// --watch runs the resident daemon: pidfile, log rotation, continuous
	// polling, and the OBSERVE phase (CI/PR). It reuses the same pass body
	// (pickNextTask → processTask) as the one-shot path.
	if loopWatch {
		return runWatchDaemon(cmd.Context(), cfg, root)
	}

	// Clear stale sentinel on startup if present.
	_ = removeSentinel(root)

	store, err := openLoopStore(root)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	// Re-queue blocked tasks so they continue in their existing worktree.
	if loopRetryBlock {
		n, rqErr := requeueBlocked(store)
		if rqErr != nil {
			return fmt.Errorf("retry-blocked: %w", rqErr)
		}
		fmt.Fprintf(os.Stderr, "%sloop: re-queued %d blocked task(s)%s\n", ansiBlue, n, ansiReset)
	}

	maxPasses := loopMaxPasses
	if maxPasses <= 0 {
		maxPasses = 0 // unlimited
	}
	idleLimit := loopIdleLimit
	if idleLimit <= 0 {
		idleLimit = 10
	}

	// Wrap the command context with signal handling.
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	totalPasses := 0
	consecutiveIdle := 0

	for {
		// Check global pass cap.
		if maxPasses > 0 && totalPasses >= maxPasses {
			fmt.Fprintf(os.Stderr, "%sloop: reached max-passes (%d), exiting%s\n", ansiBlue, maxPasses, ansiReset)
			break
		}

		task, err := pickNextTask(ctx, store)
		if err != nil {
			return fmt.Errorf("pick task: %w", err)
		}
		if task == nil {
			consecutiveIdle++
			fmt.Fprintf(os.Stderr, "%sidle%s\n", ansiBlue, ansiReset)
			if consecutiveIdle >= idleLimit {
				fmt.Fprintf(os.Stderr, "%sloop: idle limit reached (%d consecutive), exiting%s\n", ansiBlue, idleLimit, ansiReset)
				break
			}
			if loopOnce {
				break
			}
			if err := sleepInterruptibly(ctx); err != nil {
				return nil
			}
			// Poll sentinel after waking from idle sleep so a stop request is
			// honoured without waiting to pick and process another task.
			if sentinelExists(root) {
				fmt.Fprintf(os.Stderr, "%sloop: stop sentinel detected, exiting%s\n", ansiBlue, ansiReset)
				_ = removeSentinel(root)
				break
			}
			continue
		}

		consecutiveIdle = 0
		totalPasses++

		if err := processTask(ctx, cfg, root, store, task); err != nil {
			fmt.Fprintf(os.Stderr, "%sloop: task %q failed: %v%s\n", ansiRed, task.ID, err, ansiReset)
			_ = markTaskBlocked(store, task.ID)
		}

		if loopOnce {
			break
		}

		// Poll sentinel at pass boundary.
		if sentinelExists(root) {
			fmt.Fprintf(os.Stderr, "%sloop: stop sentinel detected, exiting%s\n", ansiBlue, ansiReset)
			_ = removeSentinel(root)
			break
		}

		// Check context (signal) at pass boundary.
		if ctx.Err() != nil {
			fmt.Fprintf(os.Stderr, "%sloop: interrupted, exiting%s\n", ansiBlue, ansiReset)
			break
		}
	}

	return nil
}

// sleepInterruptibly sleeps for 5s or until the context is cancelled.
func sleepInterruptibly(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return nil
	}
}

// sentinelPath returns the path to the loop stop sentinel file.
func sentinelPath(root string) string {
	return filepath.Join(root, ".colony", "loop.stop")
}

// sentinelExists returns true if the stop sentinel file exists.
func sentinelExists(root string) bool {
	_, err := os.Stat(sentinelPath(root))
	return err == nil
}

// removeSentinel deletes the stop sentinel file if it exists.
func removeSentinel(root string) error {
	err := os.Remove(sentinelPath(root))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// pickNextTask queries the tasks table for the next open or needs-fix task.
func pickNextTask(ctx context.Context, store *storage.SQLiteStore) (*storage.Task, error) {
	tasks, err := store.QueryTasks(storage.TaskFilter{States: []string{"open", "needs-fix"}})
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, nil
	}
	return &tasks[0], nil
}

// processTask runs the BuildGateFix mission on a single task.
func processTask(ctx context.Context, cfg *config.Config, root string, store *storage.SQLiteStore, task *storage.Task) error {
	lang := task.Lang
	if lang == "" {
		lang = loopLang
	}

	// Increment cycle count before processing.
	if err := store.IncrementCycle(task.ID); err != nil {
		return fmt.Errorf("increment cycle: %w", err)
	}

	// Resolve the builder input: spec file contents (if --file was given),
	// the inline description, or both combined.
	input, err := taskInput(task)
	if err != nil {
		return err
	}

	// Each task runs in its own isolated git worktree so multiple loop agents
	// can work in parallel without colliding on the working tree. On a retry,
	// reuse the task's existing worktree to continue prior work (saves tokens).
	projectName := module.ProjectName(root)
	workdir, branch, err := resolveWorktree(root, projectName, task)
	if err != nil {
		return fmt.Errorf("setup worktree: %w", err)
	}
	if task.Branch != branch {
		if err := store.UpdateTaskBranch(task.ID, branch); err != nil {
			return fmt.Errorf("record task branch: %w", err)
		}
	}

	// Write the spec to SPEC.md in the worktree. The build/fix prompts instruct
	// the agent to "read SPEC.md in this directory"; unlike craft/swarm, the loop
	// resolves the spec inline, so without this the file the prompt names does
	// not exist — weaker models then hallucinate or stub the implementation.
	if err := os.WriteFile(filepath.Join(workdir, "SPEC.md"), []byte(input), 0644); err != nil {
		return fmt.Errorf("write SPEC.md: %w", err)
	}

	// Build the mission.
	opts := mission.BuildGateFixOpts{
		Name:      "loop-" + missionLabel(task),
		Input:     input,
		Lang:      lang,
		Workdir:   workdir,
		MaxCycles: loopMaxCycles,
	}
	if loopEscalateTo != "" {
		opts.EscalationRole = mission.RoleEscalation
	}
	// Enable the review gate when --review is passed or a review role is set in
	// config.json. The model is resolved via cfg.CommandRole("loop", "review").
	if loopReview || cfg.HasCommandRole("loop", mission.RoleReview) {
		opts.ReviewRole = mission.RoleReview
	}

	m := mission.BuildGateFix(opts)

	// Build the graph with CommandRole resolution.
	g, err := mission.BuildGraph(m, mission.DefaultRegistry, func(role string) config.LLMConfig {
		return cfg.CommandRole("loop", role)
	})
	if err != nil {
		return fmt.Errorf("build graph: %w", err)
	}

	// Create a session and run the mission.
	sessID := fmt.Sprintf("loop-%s-%s", task.ID, time.Now().Format("20060102-150405"))
	if err := store.InsertSession(storage.Session{
		ID:          sessID,
		MissionName: m.Name,
		StartedAt:   time.Now(),
		Status:      "running",
	}); err != nil {
		return fmt.Errorf("insert session: %w", err)
	}

	runner := mission.NewRunner()
	out, runErr := runner.Run(ctx, m, g, sessID, store)

	// Record outcome.
	if runErr != nil {
		// Check if the context was cancelled (signal received).
		if ctx.Err() != nil {
			_ = store.UpdateSession(sessID, "interrupted", time.Now())
			_ = store.UpdateTaskState(task.ID, "needs-fix", "")
			return nil
		}

		_ = store.UpdateSession(sessID, "failed", time.Now())

		// Stuck: identical gate failure repeated — the model can't fix it, so
		// escalation/more cycles would only waste tokens. Block with feedback.
		if _, ok := runErr.(*mission.ErrStuck); ok {
			feedback := extractFeedback(out, runErr)
			_ = store.UpdateTaskState(task.ID, "blocked", feedback)
			fmt.Fprintf(os.Stderr, "%sloop: task %q stuck (identical failure repeated), blocked%s\n", ansiRed, task.ID, ansiReset)
			return nil
		}

		// Check if it's a max-cycles error (task hit the cap).
		if _, ok := runErr.(*mission.ErrMaxCycles); ok && loopEscalateTo != "" {
			// Escalate: re-dispatch on escalation role once.
			if err := escalateTask(ctx, cfg, root, store, task, lang, out); err != nil {
				return fmt.Errorf("escalation failed: %w", err)
			}
			return nil
		}

		// Max-cycles hit but no escalation configured → block (terminal) with the
		// failing gate output as feedback. Re-queuing to needs-fix would have the
		// daemon re-pick the task and re-run the whole mission with a fresh cycle
		// budget, looping forever. A blocked task is not re-picked; resume it
		// explicitly with `colony loop --retry-blocked`.
		if _, ok := runErr.(*mission.ErrMaxCycles); ok {
			feedback := extractFeedback(out, runErr)
			_ = store.UpdateTaskState(task.ID, "blocked", feedback)
			fmt.Fprintf(os.Stderr, "%sloop: task %q hit max-cycles, blocked (use --retry-blocked to resume)%s\n", ansiRed, task.ID, ansiReset)
			return nil
		}

		return runErr
	}

	_ = store.UpdateSession(sessID, "completed", time.Now())

	// Gate green — run integration gate: merge origin/<base> into the worktree
	// and re-run the deterministic gate. This catches breakage from naming
	// collisions or semantic conflicts that the builder's isolated branch cannot
	// detect. Only a clean merge + green gate proceeds to finishTask.
	baseBranch := task.BaseBranch
	if baseBranch == "" {
		baseBranch = module.DefaultBranch()
	}
	if err := integrateTask(workdir, branch, baseBranch, lang, task, store); err != nil {
		// integrateTask handles marking the task blocked on failure.
		return nil
	}

	// The integration gate passed — finish the task like craft: drop SPEC.md,
	// commit, push, open PR. A delivery failure is an operational issue to
	// surface, never a reason to re-run the builder: the code is already
	// committed, so a re-run would only produce an empty diff and spin forever.
	prURL, finishErr := finishTask(workdir, branch, baseBranch, missionLabel(task))
	if finishErr != nil {
		fmt.Fprintf(os.Stderr, "%sloop: task %q done (gate passed) but delivery failed: %v%s\n", ansiYellow, task.ID, finishErr, ansiReset)
		_ = store.UpdateTaskState(task.ID, "done", "delivery failed: "+finishErr.Error())
		return nil
	}

	fmt.Fprintf(os.Stderr, "%sloop: task %q done → %s%s\n", ansiGreen, task.ID, prURL, ansiReset)
	_ = store.UpdateTaskState(task.ID, "done", prURL)
	return nil
}

// integrateTask fetches origin/<base>, merges it into the worktree branch, and
// re-runs the deterministic gate. On merge conflict or gate failure the task is
// marked blocked and never reaches finishTask.
func integrateTask(workdir, branch, baseBranch, lang string, task *storage.Task, store *storage.SQLiteStore) error {
	out := os.Stderr
	fmt.Fprintf(out, "%sloop: integrating %s with origin/%s%s\n", ansiBlue, branch, baseBranch, ansiReset)

	// Fetch the base branch from origin.
	fetch := exec.Command("git", "fetch", "origin", baseBranch)
	fetch.Dir = workdir
	fetch.Stdout = out
	fetch.Stderr = out
	if err := fetch.Run(); err != nil {
		_ = store.UpdateTaskState(task.ID, "blocked", fmt.Sprintf("git fetch origin/%s failed: %v", baseBranch, err))
		return fmt.Errorf("git fetch: %w", err)
	}

	// Merge FETCH_HEAD into the worktree branch.
	merge := exec.Command("git", "merge", "FETCH_HEAD")
	merge.Dir = workdir
	merge.Stdout = out
	merge.Stderr = out
	if err := merge.Run(); err != nil {
		// Merge conflict — abort and block the task.
		abort := exec.Command("git", "merge", "--abort")
		abort.Dir = workdir
		_ = abort.Run()

		// Collect conflicting file list.
		conflictFiles := getConflictFiles(workdir)
		feedback := fmt.Sprintf("merge conflict with origin/%s. Conflicting files: %s", baseBranch, strings.Join(conflictFiles, ", "))
		_ = store.UpdateTaskState(task.ID, "blocked", feedback)
		fmt.Fprintf(out, "%sloop: task %q blocked — merge conflict with origin/%s%s\n", ansiRed, task.ID, baseBranch, ansiReset)
		return fmt.Errorf("merge conflict with origin/%s", baseBranch)
	}

	// Merge succeeded — re-run the deterministic gate.
	fmt.Fprintf(out, "%sloop: merge clean — re-running gates%s\n", ansiBlue, ansiReset)
	gateOutput, gateErr := module.RunGateCaptureAll(lang, workdir, nil)
	if gateErr != nil {
		feedback := fmt.Sprintf("integration gate failed after merge with origin/%s:\n%s", baseBranch, gateOutput)
		_ = store.UpdateTaskState(task.ID, "blocked", feedback)
		fmt.Fprintf(out, "%sloop: task %q blocked — integration gate failed after merge%s\n", ansiRed, task.ID, ansiReset)
		return fmt.Errorf("integration gate after merge: %w\n%s", gateErr, gateOutput)
	}

	fmt.Fprintf(out, "%sloop: integration gate passed%s\n", ansiBlue, ansiReset)
	return nil
}

// getConflictFiles returns the list of files with merge conflicts in the worktree.
func getConflictFiles(workdir string) []string {
	cmd := exec.Command("git", "diff", "--name-only", "--diff-filter=U")
	cmd.Dir = workdir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var files []string
	for _, f := range lines {
		if f != "" {
			files = append(files, f)
		}
	}
	return files
}

// finishTask mirrors craft's done-flow for a completed loop task: it removes the
// transient SPEC.md, commits all worktree changes on the task branch, pushes the
// branch to origin, and opens a PR against baseBranch. It returns the PR URL.
func finishTask(workdir, branch, baseBranch, label string) (string, error) {
	out := os.Stderr

	_ = os.Remove(filepath.Join(workdir, "SPEC.md"))

	// Squash all WIP commits down to a single clean commit. The agent may have
	// made multiple intermediate commits during the build-fix loop; reset --soft
	// to the merge-base with the base branch collapses them into staged changes
	// for one commit. Doing this before git add -A also picks up any unstaged
	// work the agent left in the worktree.
	mergeBase := exec.Command("git", "merge-base", "HEAD", "origin/"+baseBranch)
	mergeBase.Dir = workdir
	mbOut, mbErr := mergeBase.Output()
	if mbErr == nil {
		mb := strings.TrimSpace(string(mbOut))
		reset := exec.Command("git", "reset", "--soft", mb)
		reset.Dir = workdir
		if err := reset.Run(); err != nil {
			return "", fmt.Errorf("git reset --soft: %w", err)
		}
	}

	add := exec.Command("git", "add", "-A")
	add.Dir = workdir
	if err := add.Run(); err != nil {
		return "", fmt.Errorf("git add: %w", err)
	}

	// Nothing staged means the agent wrote no files — treat as a real failure so
	// the task is not falsely marked done with an empty diff.
	if diff := exec.Command("git", "diff", "--cached", "--quiet"); runInDir(diff, workdir) == nil {
		return "", fmt.Errorf("no changes to commit — agent produced an empty diff")
	}

	commit := exec.Command("git", "commit", "-m", "loop: "+label)
	commit.Dir = workdir
	commit.Stdout = out
	commit.Stderr = out
	if err := commit.Run(); err != nil {
		return "", fmt.Errorf("git commit: %w", err)
	}
	fmt.Fprintf(out, "%sloop: committed on branch %s%s\n", ansiBlue, branch, ansiReset)

	push := exec.Command("git", "push", "-u", "origin", branch)
	push.Dir = workdir
	push.Stdout = out
	push.Stderr = out
	if err := push.Run(); err != nil {
		return "", fmt.Errorf("git push: %w", err)
	}
	fmt.Fprintf(out, "%sloop: pushed branch %s%s\n", ansiBlue, branch, ansiReset)

	args := []string{
		"pr", "create",
		"--base", baseBranch,
		"--head", branch,
		"--title", branch,
		"--body", fmt.Sprintf("Automated by colony loop.\n\nBranch: `%s`\nBase: `%s`", branch, baseBranch),
	}
	if repo := remoteRepo(workdir); repo != "" {
		args = append(args, "--repo", repo)
	}
	gh := exec.Command("gh", args...)
	gh.Dir = workdir
	var prURL strings.Builder
	gh.Stdout = io.MultiWriter(out, &prURL)
	gh.Stderr = out
	if err := gh.Run(); err != nil {
		return "", fmt.Errorf("gh pr create: %w", err)
	}
	return strings.TrimSpace(prURL.String()), nil
}

// runInDir runs cmd in dir and returns its error (nil = exit 0).
func runInDir(cmd *exec.Cmd, dir string) error {
	cmd.Dir = dir
	return cmd.Run()
}

// escalateTask re-dispatches a max-cycled task on the escalation role.
func escalateTask(ctx context.Context, cfg *config.Config, root string, store *storage.SQLiteStore, task *storage.Task, lang string, prevOut *mission.Output) error {
	input, err := taskInput(task)
	if err != nil {
		return err
	}
	opts := mission.BuildGateFixOpts{
		Name:      "escalation-" + missionLabel(task),
		Input:     input,
		Lang:      lang,
		MaxCycles: 1,
	}
	// Escalation uses a single-shot mission: builder → gate → output.
	// The escalation role handles the fix.
	opts.EscalationRole = mission.RoleEscalation

	m := mission.BuildGateFix(opts)
	g, err := mission.BuildGraph(m, mission.DefaultRegistry, func(role string) config.LLMConfig {
		return cfg.CommandRole("loop", role)
	})
	if err != nil {
		return fmt.Errorf("escalation build graph: %w", err)
	}

	sessID := fmt.Sprintf("escalation-%s-%s", task.ID, time.Now().Format("20060102-150405"))
	if err := store.InsertSession(storage.Session{
		ID:          sessID,
		MissionName: m.Name,
		StartedAt:   time.Now(),
		Status:      "running",
	}); err != nil {
		return err
	}

	runner := mission.NewRunner()
	_, runErr := runner.Run(ctx, m, g, sessID, store)
	_ = store.UpdateSession(sessID, "completed", time.Now())

	if runErr != nil {
		fmt.Fprintf(os.Stderr, "%sescalation failed for task %q, marking blocked%s\n", ansiRed, task.ID, ansiReset)
		_ = store.UpdateTaskState(task.ID, "blocked", extractFeedback(nil, runErr))
		return markTaskBlocked(store, task.ID)
	}

	// Escalation succeeded.
	_ = store.UpdateTaskState(task.ID, "done", "")
	return nil
}

// markTaskBlocked transitions a task to blocked state.
func markTaskBlocked(store *storage.SQLiteStore, taskID string) error {
	return store.UpdateTaskState(taskID, "blocked", "")
}

// requeueBlocked flips blocked tasks back to needs-fix so the loop picks them
// up again, continuing in their existing worktree. Returns the count re-queued.
func requeueBlocked(store *storage.SQLiteStore) (int, error) {
	blocked, err := store.QueryTasks(storage.TaskFilter{States: []string{"blocked"}})
	if err != nil {
		return 0, err
	}
	for _, t := range blocked {
		if err := store.UpdateTaskState(t.ID, "needs-fix", t.LastFeedback); err != nil {
			return 0, err
		}
	}
	return len(blocked), nil
}

// openLoopStore opens the project's missions.db.
func openLoopStore(root string) (*storage.SQLiteStore, error) {
	dbPath := storage.DefaultDBPath()
	if root != "" {
		dbPath = filepath.Join(root, ".colony", "missions.db")
	}
	return storage.Open(dbPath)
}

// resolveWorktree returns the worktree dir and branch for a task. If the task
// already has a branch whose worktree still exists, it is reused (continue);
// otherwise a fresh worktree is created. The branch slug derives from the task
// description, falling back to the spec filename or task ID so it is never empty.
func resolveWorktree(root, projectName string, task *storage.Task) (workdir, branch string, err error) {
	if task.Branch != "" {
		path := module.WorktreePath(projectName, task.Branch)
		if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
			return path, task.Branch, nil
		}
	}

	baseBranch := task.BaseBranch
	if baseBranch == "" {
		baseBranch = module.DefaultBranch()
	}
	branch = module.NewBranch(branchDesc(task))
	workdir, err = module.SetupWorktreeLocal(root, projectName, branch, baseBranch)
	return workdir, branch, err
}

// branchDesc returns a non-empty label for branch naming: the task description,
// else the spec's title (read from its contents), else the spec filename
// without extension, else the task ID.
func branchDesc(task *storage.Task) string {
	if task.Description != "" {
		return task.Description
	}
	if task.SpecPath != "" {
		base := filepath.Base(task.SpecPath)
		if data, err := os.ReadFile(task.SpecPath); err == nil {
			if d := module.ExtractTaskDesc(string(data), base); d != "" {
				return d
			}
		}
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	return task.ID
}

// missionLabel returns a readable mission-name suffix derived from the task's
// description/spec, falling back to the task ID so it is never empty.
func missionLabel(task *storage.Task) string {
	if label := module.Slugify(branchDesc(task)); label != "" {
		return label
	}
	return task.ID
}

// taskInput resolves the builder input for a task: the spec file contents when
// --file was given, the inline description otherwise, or both combined when a
// task has both. Returns an error if a referenced spec file cannot be read.
func taskInput(task *storage.Task) (string, error) {
	if task.SpecPath == "" {
		return task.Description, nil
	}
	data, err := os.ReadFile(task.SpecPath)
	if err != nil {
		return "", fmt.Errorf("read spec file %q: %w", task.SpecPath, err)
	}
	spec := string(data)
	if task.Description == "" {
		return spec, nil
	}
	return task.Description + "\n\n" + spec, nil
}

// extractFeedback returns a string representation of mission output or error
// for use in last_feedback.
func extractFeedback(out *mission.Output, runErr error) string {
	// If the error captured the last rejecting gate output (max-cycles or
	// stuck), use its feedback text — the real failing gate output.
	var last *mission.Output
	switch e := runErr.(type) {
	case *mission.ErrMaxCycles:
		last = e.LastOutput
	case *mission.ErrStuck:
		last = e.LastOutput
	}
	if last != nil {
		if txt := last.Envelope.OutputText(); txt != "" {
			return txt
		}
		if last.Raw != "" {
			return last.Raw
		}
		if last.Envelope.Feedback != "" {
			return last.Envelope.Feedback
		}
	}

	if out != nil {
		if txt := out.Envelope.OutputText(); txt != "" {
			return txt
		}
		if out.Raw != "" {
			return out.Raw
		}
		if out.Envelope.Feedback != "" {
			return out.Envelope.Feedback
		}
	}
	if runErr != nil {
		return runErr.Error()
	}
	return ""
}

// runWatchDaemon is the long-lived --watch daemon: it manages the pidfile and
// rotating log, then polls the queue continuously, running the same pass body
// (pickNextTask → processTask) as the one-shot path plus an OBSERVE phase
// (CI/PR) each cycle. It exits cleanly on SIGINT/SIGTERM or the stop sentinel.
func runWatchDaemon(ctx context.Context, cfg *config.Config, root string) error {
	colonyPath := filepath.Join(root, ".colony")
	if err := os.MkdirAll(colonyPath, 0755); err != nil {
		return fmt.Errorf("create .colony dir: %w", err)
	}

	pidPath := filepath.Join(colonyPath, "loop.pid")
	logPath := filepath.Join(colonyPath, "loop.log")

	// Pidfile lock: only one daemon per project. A stale pidfile (dead process)
	// is cleared by writePidfile.
	if err := writePidfile(pidPath); err != nil {
		return err
	}
	defer func() { _ = os.Remove(pidPath) }()

	// Clear stale stop sentinel on startup so the daemon does not self-stop.
	_ = removeSentinel(root)

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	multiOut := io.MultiWriter(os.Stderr, logFile)
	slog.SetDefault(slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo})))

	store, err := openLoopStore(root)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(multiOut, "%sloop: daemon started (pid=%d)%s\n", ansiBlue, os.Getpid(), ansiReset)
	slog.Info("daemon started", "pid", os.Getpid())

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintf(multiOut, "%sloop: daemon interrupted, exiting%s\n", ansiBlue, ansiReset)
			return nil
		default:
		}

		// Honour the stop sentinel at the top of each pass.
		if sentinelExists(root) {
			fmt.Fprintf(multiOut, "%sloop: stop sentinel detected, exiting%s\n", ansiBlue, ansiReset)
			_ = removeSentinel(root)
			return nil
		}

		// Rotate the daemon log if it has grown past the threshold.
		if err := maybeRotate(logPath, 10<<20, 3); err != nil {
			slog.Warn("log rotation failed", "error", err)
		}

		// One pass: pick the next actionable task and process it (reusing the
		// full build→gate→fix engine and done-flow).
		task, err := pickNextTask(ctx, store)
		if err != nil {
			slog.Error("pick task failed", "error", err)
		} else if task != nil {
			if err := processTask(ctx, cfg, root, store, task); err != nil {
				slog.Error("task failed", "id", task.ID, "error", err)
				_ = markTaskBlocked(store, task.ID)
			}
		}

		// OBSERVE phase: poll CI/PR for tasks with a pr_url. Runs only here, in
		// --watch mode — the one-shot path never observes.
		observeAllTasks(ctx, store)

		// Sleep before the next poll, waking early on cancellation.
		select {
		case <-ctx.Done():
			fmt.Fprintf(multiOut, "%sloop: daemon interrupted, exiting%s\n", ansiBlue, ansiReset)
			return nil
		case <-time.After(loopPollInterval):
		}
	}
}

// observeAllTasks runs the OBSERVE phase: polls CI/PR for tasks with a pr_url
// and applies any feedback via store.UpdateTaskState. Called only from the
// --watch daemon.
func observeAllTasks(ctx context.Context, store *storage.SQLiteStore) {
	results, err := mission.ObserveTasksForPR(ctx, store)
	if err != nil {
		slog.Warn("observe failed", "error", err)
		return
	}
	for _, r := range results {
		if r.Changed {
			slog.Info("task observed", "id", r.TaskID, "status", r.NewStatus, "feedback", r.NewFeedback)
		}
	}
}
