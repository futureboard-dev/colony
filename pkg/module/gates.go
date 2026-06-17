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
	Vet       string
	Lint      string
	TypeCheck string
	Test      string
	Build     string
}

func CommandsFor(lang string) (LangCommands, error) {
	switch strings.ToLower(lang) {
	case "typescript", "ts":
		return LangCommands{
			Format:    "pnpm prettier --write .",
			Vet:       "pnpm tsc --noEmit",
			Lint:      "pnpm eslint .",
			TypeCheck: "pnpm tsc --noEmit",
			Test:      "pnpm build",
			Build:     "pnpm build",
		}, nil
	case "python", "py":
		return LangCommands{
			Format:    "ruff format .",
			Vet:       "mypy . --ignore-missing-imports",
			Lint:      "ruff check .",
			TypeCheck: "mypy . --ignore-missing-imports",
			Test:      "pytest --tb=short",
			Build:     "",
		}, nil
	case "go":
		return LangCommands{
			Format:    "gofmt -w ./...",
			Vet:       "go vet ./...",
			Lint:      "golangci-lint run ./...",
			TypeCheck: "go build ./...",
			Test:      "go test ./... -count=1",
			Build:     "go build ./...",
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
	// Scope golangci-lint's cache to this worktree. Colony runs gates in
	// throwaway worktrees; a shared cache retains results keyed by the old
	// worktree's absolute paths, so once that worktree is pruned the linter
	// reports phantom errors against files it can no longer read. A per-worktree
	// cache dir is torn down with the worktree and never leaks across runs.
	cmd.Env = append(os.Environ(), "GOLANGCI_LINT_CACHE="+filepath.Join(workdir, ".golangci-cache"))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// RunGateCaptureAll runs all quality gates for the given language in sequence:
// format, vet, lint, typecheck, test, build. Skips gates in the skip set and
// those whose commands are empty. Returns on the first non-zero exit: combined
// output + exit error. Returns empty string and nil when all gates pass.
func RunGateCaptureAll(lang string, workdir string, skip map[string]bool) (string, error) {
	cmds, err := CommandsFor(lang)
	if err != nil {
		return "", err
	}
	type gateStep struct {
		name string
		cmd  string
	}
	steps := []gateStep{
		{"format", cmds.Format},
		{"vet", cmds.Vet},
		{"lint", cmds.Lint},
		{"typecheck", cmds.TypeCheck},
		{"test", cmds.Test},
		{"build", cmds.Build},
	}
	var combined strings.Builder
	for _, s := range steps {
		if skip[s.name] || s.cmd == "" {
			continue
		}
		out, err := RunGateCapture(s.cmd, workdir)
		if err != nil {
			fmt.Fprintf(&combined, "--- %s ---\n%s", s.name, out)
			return combined.String(), fmt.Errorf("%s failed: %w", s.name, err)
		}
	}
	return "", nil
}
