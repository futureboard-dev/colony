package llm

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jirateep/colony/pkg/config"
)

// Executor delegates agent execution to claude (Anthropic) or crush (all other providers).
// Colony owns orchestration; these CLIs own the agent loop, MCP, and tool ecosystems.
type Executor struct {
	cfg config.LLMConfig
}

func New(cfg config.LLMConfig) *Executor {
	return &Executor{cfg: cfg}
}

func (e *Executor) CLI() string {
	if e.cfg.Provider == "anthropic" {
		return "claude"
	}
	return "crush"
}

// RunHeadless runs a prompt non-interactively (text generation, no file tools).
// Output is written to out. Used for coordinator, scout, reviewer roles.
func (e *Executor) RunHeadless(ctx context.Context, workdir, prompt string, out io.Writer) error {
	if err := e.validateKeyIfNeeded(); err != nil {
		return err
	}
	cli := e.CLI()
	if err := checkInstalled(cli); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, cli, e.headlessArgs(prompt)...)
	cmd.Dir = workdir
	cmd.Env = cleanEnvForClaude()
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RunAgent runs a prompt as an autonomous coding agent with file tools.
// Output streams to out. Used for build and fix steps.
//
// For claude, stdout is the stream-json event feed and is rendered through
// streamClaude into a compact one-line-per-action view. For crush (which has
// no structured stream format) output is passed through raw with --verbose.
func (e *Executor) RunAgent(ctx context.Context, workdir, prompt string, out io.Writer) error {
	if err := e.validateKeyIfNeeded(); err != nil {
		return err
	}
	cli := e.CLI()
	if err := checkInstalled(cli); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, cli, e.agentArgs(prompt)...)
	cmd.Dir = workdir
	cmd.Env = cleanEnvForClaude()

	stdoutW, stderrW := out, out
	if out == nil {
		stdoutW, stderrW = os.Stdout, os.Stderr
	}

	if cli == "claude" {
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		cmd.Stderr = stderrW
		if err := cmd.Start(); err != nil {
			return err
		}
		streamErr := streamClaude(stdout, stdoutW)
		if waitErr := cmd.Wait(); waitErr != nil {
			return waitErr
		}
		return streamErr
	}

	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW
	return cmd.Run()
}

// RunInteractive opens an interactive agent session. Blocks until user exits.
func (e *Executor) RunInteractive(workdir, initialPrompt string) error {
	if err := e.validateKeyIfNeeded(); err != nil {
		return err
	}
	cli := e.CLI()
	if err := checkInstalled(cli); err != nil {
		return err
	}
	var args []string
	switch cli {
	case "claude":
		args = []string{"--add-dir", workdir, initialPrompt}
	default:
		args = []string{initialPrompt}
	}
	cmd := exec.Command(cli, args...)
	cmd.Dir = workdir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	return cmd.Run()
}

// Preflight verifies that crush run can execute tool calls (known flaky issue).
// Skipped for anthropic — claude is reliable.
func (e *Executor) Preflight() error {
	if e.cfg.Provider == "anthropic" {
		return nil
	}
	if err := checkInstalled("crush"); err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "colony-preflight-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	testFile := filepath.Join(tmp, "preflight.txt")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := e.RunAgent(ctx, tmp,
		fmt.Sprintf("Write the text 'ok' to the file %s. Do nothing else.", testFile),
		io.Discard,
	); err != nil {
		return fmt.Errorf("crush preflight run failed: %w\n"+
			"  See: https://github.com/charmbracelet/crush/issues/1322\n"+
			"  Fix: brew upgrade crush", err)
	}
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		return fmt.Errorf("crush run tool-calling is broken — file was not written\n" +
			"  See: https://github.com/charmbracelet/crush/issues/1322\n" +
			"  Fix: brew upgrade crush\n" +
			"  Or set provider=\"anthropic\" in .colony/config.json")
	}
	return nil
}

func (e *Executor) headlessArgs(prompt string) []string {
	switch e.CLI() {
	case "claude":
		// Headless = pure text generation. Disallow every tool so the model
		// can't write files, run shell, or return a chat-style "Done — saved
		// to X" summary instead of the JSON envelope the parser expects.
		args := []string{
			"-p", prompt,
			"--output-format", "text",
			"--disallowedTools", "Write,Edit,Bash,Read,Glob,Grep,WebFetch,WebSearch,Task,NotebookEdit",
		}
		if e.cfg.Model != "" {
			args = append(args, "--model", e.cfg.Model)
		}
		return args
	default:
		args := []string{"run", "-q"}
		if e.cfg.Model != "" {
			args = append(args, "-m", e.cfg.Model)
		}
		args = append(args, prompt)
		return args
	}
}

func (e *Executor) agentArgs(prompt string) []string {
	switch e.CLI() {
	case "claude":
		// stream-json emits an event per message/tool call as it happens;
		// --verbose is required by claude to enable it under -p.
		args := []string{
			"-p", prompt,
			"--output-format", "stream-json",
			"--verbose",
			"--allowedTools", "Write,Edit,Bash,Read,Glob,Grep",
		}
		if e.cfg.Model != "" {
			args = append(args, "--model", e.cfg.Model)
		}
		return args
	default:
		// crush has no structured stream format; --verbose surfaces its
		// activity logs so the run isn't a silent black box.
		args := []string{"run", "--verbose"}
		if e.cfg.Model != "" {
			args = append(args, "-m", e.cfg.Model)
		}
		args = append(args, prompt)
		return args
	}
}

// validateKeyIfNeeded skips key validation for anthropic — the claude CLI
// manages its own authentication and does not require ANTHROPIC_API_KEY in env.
func (e *Executor) validateKeyIfNeeded() error {
	if e.cfg.Provider == "anthropic" {
		return nil
	}
	return e.cfg.ValidateKey()
}

// cleanEnvForClaude returns os.Environ() prepared for spawning the claude CLI.
// Two vars are stripped so claude always authenticates with its own native
// credentials (the keychain OAuth / subscription set up via `claude` login):
//
//   - CLAUDE_CODE_CHILD_SESSION: tells claude it's a tool subprocess inside
//     Claude Code and should use API-key auth, which fails under a subscription.
//   - ANTHROPIC_API_KEY: when present, claude prefers it (API billing) over the
//     subscription. We drop it so colony defaults to the user's subscription,
//     matching whatever auth the user's claude CLI itself uses.
func cleanEnvForClaude() []string {
	drop := map[string]bool{
		"CLAUDE_CODE_CHILD_SESSION": true,
		"ANTHROPIC_API_KEY":         true,
	}
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, e := range env {
		key, _, _ := strings.Cut(e, "=")
		if !drop[key] {
			out = append(out, e)
		}
	}
	return out
}

func checkInstalled(cli string) error {
	if _, err := exec.LookPath(cli); err != nil {
		switch cli {
		case "claude":
			return fmt.Errorf("claude not installed\n  Install: https://claude.ai/code")
		case "crush":
			return fmt.Errorf("crush not installed\n  Install: brew install charmbracelet/tap/crush\n  Or: https://github.com/charmbracelet/crush")
		}
		return fmt.Errorf("%s not found in PATH", cli)
	}
	return nil
}
