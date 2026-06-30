package module

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

var (
	lintCacheDirMu sync.Mutex
	lintCacheDir   string
)

// LintCacheDir returns the current lint cache temp dir, creating it if needed.
// The caller must call CleanupLintCache when done. Multiple calls return the
// same dir until CleanupLintCache is called.
func LintCacheDir() (string, error) {
	lintCacheDirMu.Lock()
	defer lintCacheDirMu.Unlock()
	if lintCacheDir != "" {
		return lintCacheDir, nil
	}
	dir, err := os.MkdirTemp("", "golangci-lint-cache-*")
	if err != nil {
		return "", err
	}
	lintCacheDir = dir
	return dir, nil
}

// CleanupLintCache removes the lint cache temp dir.
func CleanupLintCache() {
	lintCacheDirMu.Lock()
	defer lintCacheDirMu.Unlock()
	if lintCacheDir != "" {
		os.RemoveAll(lintCacheDir) //nolint:errcheck
		lintCacheDir = ""
	}
}

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
			Format:    "go fmt ./...",
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
		_ = RunShell("pnpm install --frozen-lockfile", worktree, out)
	case "python", "py":
		if _, err := os.Stat(filepath.Join(worktree, "requirements.txt")); err != nil {
			return
		}
		// Equivalent to `python3 -m venv .venv && source .venv/bin/activate &&
		// pip install -r requirements.txt`, but RunShell has no shell so we
		// create the venv and invoke its pip by absolute path.
		fmt.Fprintf(out, "   Installing dependencies (pip into .venv)...\n")
		_ = RunShell("python3 -m venv .venv", worktree, out)
		pip := filepath.Join(worktree, ".venv", "bin", "pip")
		_ = RunShell(pip+" install -r requirements.txt", worktree, out)
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

// ChangedFiles returns the lintable files that differ between base and the
// worktree HEAD, restricted to paths that still exist on disk. Used to scope
// auto-fix and the format/lint gates to a task's own changes. Best-effort:
// returns nil on any git error.
func ChangedFiles(workdir, base string) []string {
	cmd := exec.Command("git", "diff", "--name-only", "--diff-filter=ACMR", base+"...HEAD")
	cmd.Dir = workdir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch strings.ToLower(filepath.Ext(line)) {
		case ".ts", ".tsx", ".js", ".jsx", ".cjs", ".mjs", ".json", ".css", ".scss":
		default:
			continue
		}
		if _, err := os.Stat(filepath.Join(workdir, line)); err != nil {
			continue // deleted or renamed away
		}
		files = append(files, line)
	}
	return files
}

// AutoFix runs deterministic auto-fixers on the given files before gates run, so
// formatting and mechanically-fixable lint never reach the (token-costing) fixer
// agent. Best-effort and non-fatal; a no-op when files is empty.
func AutoFix(lang, workdir string, files []string, out io.Writer) {
	if len(files) == 0 {
		return
	}
	switch strings.ToLower(lang) {
	case "typescript", "ts":
		runFix(append([]string{"pnpm", "eslint", "--fix"}, files...), workdir, out)
		runFix(append([]string{"pnpm", "prettier", "--write"}, files...), workdir, out)
	}
}

func runFix(argv []string, workdir string, out io.Writer) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = workdir
	cmd.Stdout = out
	cmd.Stderr = out
	_ = cmd.Run() // best-effort: the gate re-checks whatever couldn't be fixed
}

// RunGateCapture runs a gate command and returns (combined output, error).
func RunGateCapture(command, workdir string) (string, error) {
	return runGateArgv(strings.Fields(command), workdir)
}

// runGateArgv runs a gate as an explicit argv (no shell) and returns its
// combined output. Splitting RunGateCapture this way lets scoped gates pass a
// changed-file list as real arguments instead of re-splitting a command string.
func runGateArgv(argv []string, workdir string) (string, error) {
	if len(argv) == 0 {
		return "", nil
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = workdir
	// GOLANGCI_LINT_CACHE is set to a temp dir outside the worktree, preventing
	// stale cached results keyed by a pruned worktree's absolute paths and
	// avoiding artifact directories inside the worktree.
	lintDir, err := LintCacheDir()
	if err != nil {
		return "", fmt.Errorf("lint cache dir: %w", err)
	}
	cmd.Env = append(os.Environ(), "GOLANGCI_LINT_CACHE="+lintDir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// RunGateCaptureAll runs all quality gates for the given language in sequence:
// format, vet, lint, typecheck, test, build. Skips gates in the skip set and
// those whose commands are empty. Returns on the first non-zero exit: combined
// output + exit error. Returns empty string and nil when all gates pass.
func RunGateCaptureAll(lang string, workdir string, skip map[string]bool) (string, error) {
	return runGates(lang, workdir, nil, skip)
}

// RunGateCaptureScoped is like RunGateCaptureAll but restricts the format and
// lint gates to the given changed files. Whole-repo lint/format on a base
// branch that already carries violations would fail every integration; scoping
// keeps the gate — and its output — about the task's own changes. vet, typecheck,
// test, and build stay whole-project because they can't be scoped to a subset.
func RunGateCaptureScoped(lang string, workdir string, files []string, skip map[string]bool) (string, error) {
	return runGates(lang, workdir, files, skip)
}

func runGates(lang string, workdir string, files []string, skip map[string]bool) (string, error) {
	cmds, err := CommandsFor(lang)
	if err != nil {
		return "", err
	}
	type gateStep struct {
		name   string
		cmd    string
		scoped bool // format/lint may be restricted to changed files
	}
	steps := []gateStep{
		{"format", cmds.Format, true},
		{"vet", cmds.Vet, false},
		{"lint", cmds.Lint, true},
		{"typecheck", cmds.TypeCheck, false},
		{"test", cmds.Test, false},
		{"build", cmds.Build, false},
	}
	var combined strings.Builder
	for _, s := range steps {
		if skip[s.name] || s.cmd == "" {
			continue
		}
		argv := strings.Fields(s.cmd)
		if s.scoped && len(files) > 0 {
			argv = scopeArgv(argv, files)
		}
		out, err := runGateArgv(argv, workdir)
		if err != nil {
			fmt.Fprintf(&combined, "--- %s ---\n%s", s.name, out)
			return combined.String(), fmt.Errorf("%s failed: %w", s.name, err)
		}
	}
	return "", nil
}

// scopeArgv replaces a trailing whole-repo target (e.g. the "." in `eslint .`)
// with an explicit file list so the tool runs only on the changed files.
func scopeArgv(argv []string, files []string) []string {
	if n := len(argv); n > 0 && (argv[n-1] == "." || argv[n-1] == "./...") {
		argv = argv[:n-1]
	}
	return append(argv, files...)
}
