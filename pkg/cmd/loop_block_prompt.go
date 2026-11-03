package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/futureboard-dev/colony/pkg/fix"
	"github.com/futureboard-dev/colony/pkg/module"
	"github.com/futureboard-dev/colony/pkg/storage"
	"github.com/mattn/go-isatty"
)

// loopHasTerminal reports whether a person can answer a prompt and use an
// agent session. Overridable in tests.
var loopHasTerminal = func() bool {
	for _, f := range []*os.File{os.Stdin, os.Stdout, os.Stderr} {
		if !isatty.IsTerminal(f.Fd()) {
			return false
		}
	}
	return true
}

// offerBlockRecovery shows the recovery menu if the task just ended up blocked.
// Only single-task foreground runs call it: parallel workers would compete for
// the terminal, and runs without one must never prompt, so those tasks stay
// blocked for the TUI or --retry-blocked.
func offerBlockRecovery(ctx context.Context, root string, store *storage.SQLiteStore, taskID string) {
	if !loopHasTerminal() || ctx.Err() != nil {
		return
	}
	tasks, err := store.QueryTasks(storage.TaskFilter{ID: taskID})
	if err != nil || len(tasks) == 0 || tasks[0].State != "blocked" {
		return
	}
	p := blockPrompt{root: root, store: store, in: bufio.NewReader(os.Stdin), out: os.Stderr}
	p.run(ctx, &tasks[0])
}

// blockPrompt is the terminal version of the TUI's block flow.
type blockPrompt struct {
	root  string
	store *storage.SQLiteStore
	in    *bufio.Reader
	out   io.Writer
}

func (p *blockPrompt) run(ctx context.Context, task *storage.Task) {
	req := fix.Request{
		TaskID:      task.ID,
		Description: task.Description,
		Reason:      task.LastFeedback,
		Worktree:    p.worktree(task),
		Flow:        "fix",
		Choice:      fix.Choice{Tool: fix.Tool(task.BlockTool)},
	}

	cause, nextStep := task.LastFeedback, ""
	if expl, err := fix.Explain(ctx, req); err == nil {
		cause, nextStep = expl.Cause, expl.NextStep
	}
	fmt.Fprintf(p.out, "\n%s⚠ Task %s blocked:%s %s\n", ansiYellow, task.ID, ansiReset, cause)
	if nextStep != "" {
		fmt.Fprintf(p.out, "  Next: %s\n", nextStep)
	}

	for {
		fmt.Fprintf(p.out, "\n  [f] Fix with agent   [t] Tell it what to do\n  [o] Open shell       [s] Skip\n")
		answer, ok := p.ask(ctx, "Choice [f]: ")
		if !ok {
			return
		}
		switch strings.ToLower(answer) {
		case "", "f":
			if p.fixWithAgent(ctx, req) {
				p.offerResume(ctx, req)
				return
			}
		case "t":
			hint, ok := p.ask(ctx, "Tell it what to do: ")
			if !ok {
				return
			}
			if hint != "" {
				req.Hint = hint
				p.retry(ctx, req)
				return
			}
		case "o":
			p.openShell(req)
			p.offerResume(ctx, req)
			return
		case "s":
			fmt.Fprintf(p.out, "Task %s skipped — still blocked (use --retry-blocked to resume)\n", task.ID)
			return
		default:
			fmt.Fprintf(p.out, "Unknown choice %q\n", answer)
		}
	}
}

// fixWithAgent picks a tool and model, then opens the session. It reports
// whether a session ran; a failure returns to the menu instead of becoming a
// new block, since a bad model choice says nothing about the task.
func (p *blockPrompt) fixWithAgent(ctx context.Context, req fix.Request) bool {
	tools, err := fix.InstalledTools(ctx)
	if err != nil {
		fmt.Fprintf(p.out, "%s%v%s\n", ansiRed, err, ansiReset)
		return false
	}
	choice, err := fix.ResolveChoice(ctx, req)
	if err != nil {
		fmt.Fprintf(p.out, "%s%v%s\n", ansiRed, err, ansiReset)
		return false
	}

	if len(tools) > 1 {
		names := make([]string, len(tools))
		for i, t := range tools {
			names[i] = string(t)
		}
		answer, ok := p.ask(ctx, fmt.Sprintf("Fix with (%s) [%s]: ", strings.Join(names, "/"), choice.Tool))
		if !ok {
			return false
		}
		if answer != "" && fix.Tool(answer) != choice.Tool {
			if !slices.Contains(tools, fix.Tool(answer)) {
				fmt.Fprintf(p.out, "%q is not installed\n", answer)
				return false
			}
			req.Choice.Tool = fix.Tool(answer)
			if choice, err = fix.ResolveChoice(ctx, req); err != nil {
				fmt.Fprintf(p.out, "%s%v%s\n", ansiRed, err, ansiReset)
				return false
			}
		}
	}

	currentModel := choice.Model
	if currentModel == "" {
		currentModel = "default"
	}
	model, ok := p.ask(ctx, fmt.Sprintf("Model [%s]: ", currentModel))
	if !ok {
		return false
	}
	if model != "" {
		choice.Model = model
	}

	if err := p.store.UpdateTaskBlockTool(req.TaskID, string(choice.Tool)); err != nil {
		fmt.Fprintf(p.out, "%sloop: record block tool: %v%s\n", ansiYellow, err, ansiReset)
	}
	req.Choice = choice
	if err := fix.OpenSession(ctx, req); err != nil {
		fmt.Fprintf(p.out, "%sfix session failed: %v%s\n", ansiRed, err, ansiReset)
		return false
	}
	return true
}

func (p *blockPrompt) openShell(req fix.Request) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell)
	cmd.Dir = p.root
	if info, err := os.Stat(req.Worktree); err == nil && info.IsDir() {
		cmd.Dir = req.Worktree
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(p.out, "%sshell exited: %v%s\n", ansiYellow, err, ansiReset)
	}
}

func (p *blockPrompt) offerResume(ctx context.Context, req fix.Request) {
	answer, ok := p.ask(ctx, fmt.Sprintf("Resume task %s? [Y/n]: ", req.TaskID))
	if !ok {
		return
	}
	if a := strings.ToLower(answer); a == "n" || a == "no" {
		fmt.Fprintf(p.out, "Task %s stays blocked (use --retry-blocked to resume)\n", req.TaskID)
		return
	}
	p.retry(ctx, req)
}

func (p *blockPrompt) retry(ctx context.Context, req fix.Request) {
	if err := fix.Retry(ctx, req); err != nil {
		fmt.Fprintf(p.out, "%sresume failed: %v%s\n", ansiRed, err, ansiReset)
		return
	}
	fmt.Fprintf(p.out, "%sloop: task %q re-queued in the same worktree%s\n", ansiBlue, req.TaskID, ansiReset)
}

// worktree mirrors the TUI's lookup: the path may not exist yet, which the
// session and shell both tolerate.
func (p *blockPrompt) worktree(task *storage.Task) string {
	if task.Branch == "" {
		return ""
	}
	return module.WorktreePath(module.ProjectName(p.root), task.Branch)
}

// ask reads one line, giving up when ctx is cancelled so Ctrl-C at a prompt
// still exits the loop. Nothing reads stdin between prompts, because the agent
// session or shell needs all of it.
func (p *blockPrompt) ask(ctx context.Context, question string) (string, bool) {
	fmt.Fprint(p.out, question)

	type result struct {
		line string
		err  error
	}
	read := make(chan result, 1)
	go func() {
		line, err := p.in.ReadString('\n')
		read <- result{line, err}
	}()

	select {
	case <-ctx.Done():
		fmt.Fprintln(p.out)
		return "", false
	case r := <-read:
		if r.err != nil && r.line == "" {
			return "", false
		}
		return strings.TrimSpace(r.line), true
	}
}
