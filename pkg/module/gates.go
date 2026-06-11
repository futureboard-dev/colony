package module

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type LangCommands struct {
	Format    string
	Lint      string
	TypeCheck string
	Test      string
}

func CommandsFor(lang string) (LangCommands, error) {
	switch strings.ToLower(lang) {
	case "typescript", "ts":
		return LangCommands{
			Format:    "pnpm prettier --write .",
			Lint:      "pnpm eslint .",
			TypeCheck: "pnpm tsc --noEmit",
			Test:      "pnpm build",
		}, nil
	case "python", "py":
		return LangCommands{
			Format:    "ruff format .",
			Lint:      "ruff check .",
			TypeCheck: "mypy . --ignore-missing-imports",
			Test:      "pytest --tb=short",
		}, nil
	case "go":
		return LangCommands{
			Format:    "gofmt -w ./...",
			Lint:      "golangci-lint run ./...",
			TypeCheck: "go build ./...",
			Test:      "go test ./... -count=1",
		}, nil
	default:
		return LangCommands{}, fmt.Errorf("unknown language %q — use: typescript, python, go", lang)
	}
}

// CommandAvailable reports whether the first token of command resolves to a
// binary on PATH. Used to skip optional gates (e.g. lint) when the underlying
// tool isn't installed, mirroring the CLI quality-gate hook's behavior.
func CommandAvailable(command string) bool {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return false
	}
	_, err := exec.LookPath(parts[0])
	return err == nil
}

// InstallDeps installs project dependencies in the worktree for the given
// language. It is best-effort: failures are non-fatal so the agent can still
// run, and language-specific manifests (requirements.txt, go.mod) are skipped
// when absent.
func InstallDeps(lang, worktree string, out io.Writer) {
	switch strings.ToLower(lang) {
	case "typescript", "ts":
		fmt.Fprintf(out, "   Installing dependencies (pnpm)...\n")
		RunShell("pnpm install --frozen-lockfile", worktree, out) //nolint:errcheck
	case "python", "py":
		if _, err := os.Stat(filepath.Join(worktree, "requirements.txt")); err != nil {
			return
		}
		// Equivalent to `python3 -m venv .venv && source .venv/bin/activate &&
		// pip install -r requirements.txt`, but RunShell has no shell so we
		// create the venv and invoke its pip by absolute path.
		fmt.Fprintf(out, "   Installing dependencies (pip into .venv)...\n")
		RunShell("python3 -m venv .venv", worktree, out) //nolint:errcheck
		pip := filepath.Join(worktree, ".venv", "bin", "pip")
		RunShell(pip+" install -r requirements.txt", worktree, out) //nolint:errcheck
	case "go":
		if _, err := os.Stat(filepath.Join(worktree, "go.mod")); err != nil {
			return
		}
		fmt.Fprintf(out, "   Installing dependencies (go mod download)...\n")
		RunShell("go mod download", worktree, out) //nolint:errcheck
	}
}

// RunFormat runs the format command, treating failures as non-fatal warnings.
func RunFormat(command, workdir string, out io.Writer) {
	if err := RunShell(command, workdir, out); err != nil {
		fmt.Fprintf(out, "⚠ format had warnings (non-fatal)\n")
	}
}

// RunGateCapture runs a gate command and returns (combined output, error).
func RunGateCapture(command, workdir string) (string, error) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "", nil
	}
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
