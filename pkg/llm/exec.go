package llm

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	if err := e.cfg.ValidateKey(); err != nil {
		return err
	}
	cli := e.CLI()
	if err := checkInstalled(cli); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, cli, e.headlessArgs(prompt)...)
	cmd.Dir = workdir
	cmd.Env = os.Environ()
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}

// RunAgent runs a prompt as an autonomous coding agent with file tools.
// Output streams to out. Used for build and fix steps.
func (e *Executor) RunAgent(ctx context.Context, workdir, prompt string, out io.Writer) error {
	if err := e.cfg.ValidateKey(); err != nil {
		return err
	}
	cli := e.CLI()
	if err := checkInstalled(cli); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, cli, e.agentArgs(prompt)...)
	cmd.Dir = workdir
	cmd.Env = os.Environ()
	if out != nil {
		cmd.Stdout = out
		cmd.Stderr = out
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

// RunInteractive opens an interactive agent session. Blocks until user exits.
func (e *Executor) RunInteractive(workdir, initialPrompt string) error {
	if err := e.cfg.ValidateKey(); err != nil {
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
		args := []string{"-p", prompt, "--output-format", "text"}
		if e.cfg.Model != "" {
			args = append(args, "--model", e.cfg.Model)
		}
		return args
	default:
		args := []string{"run", "--yolo", "-q", prompt}
		if e.cfg.Model != "" {
			args = append(args, "-m", e.cfg.Model)
		}
		return args
	}
}

func (e *Executor) agentArgs(prompt string) []string {
	switch e.CLI() {
	case "claude":
		args := []string{
			"-p", prompt,
			"--output-format", "text",
			"--allowedTools", "Write,Edit,Bash,Read,Glob,Grep",
		}
		if e.cfg.Model != "" {
			args = append(args, "--model", e.cfg.Model)
		}
		return args
	default:
		args := []string{"run", "--yolo", prompt}
		if e.cfg.Model != "" {
			args = append(args, "-m", e.cfg.Model)
		}
		return args
	}
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
