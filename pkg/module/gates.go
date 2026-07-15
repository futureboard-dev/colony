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

// resolveBase returns base if it resolves to a revision inside workdir, otherwise
// falls back to the worktree's own origin/HEAD (e.g. "origin/master"). Callers
// derive base from module.DefaultBranch(), which reads git state from the process
// CWD — not the worktree — so it can produce a name (commonly "origin/main") that
// doesn't exist in a repo whose default branch is "master". An unresolved base
// makes the git diff error out and the scoped gate silently fall back to whole-repo
// lint. Re-resolving against the worktree's real default branch prevents that.
func resolveBase(workdir, base string) string {
	if base != "" {
		if err := runGit(workdir, "rev-parse", "--verify", "--quiet", base); err == nil {
			return base
		}
	}
	out, err := gitOutput(workdir, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		return base // nothing better; caller's git diff will fail gracefully
	}
	ref := strings.TrimSpace(out) // refs/remotes/origin/<branch>
	return "origin/" + ref[strings.LastIndex(ref, "/")+1:]
}

func runGit(workdir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = workdir
	return cmd.Run()
}

func gitOutput(workdir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = workdir
	out, err := cmd.Output()
	return string(out), err
}

// ChangedFiles returns the lintable files that differ between base and the
// worktree's current working tree, restricted to paths that still exist on disk.
// Used to scope auto-fix and the format/lint gates to a task's own changes.
// Best-effort: returns nil on any git error.
//
// The diff is against the working tree (git diff base, no HEAD), not committed
// HEAD: craft --resume runs gates before committing, so the task's edits are still
// unstaged — a base..HEAD diff would report zero files and the gate would fall back
// to whole-repo lint. Untracked-but-not-ignored files are added separately (git
// diff never lists them). Committed changes still show, so the loop path is
// unaffected. The comparison is two-dot equivalent (base vs worktree), never
// three-dot, so a stale merge-base cannot balloon the list with unrelated base
// churn.
//
// base is re-resolved against the worktree (see resolveBase) so a base name that
// doesn't exist there falls back to the worktree's real default branch instead of
// erroring into a whole-repo lint.
func ChangedFiles(workdir, base string) []string {
	base = resolveBase(workdir, base)
	// Tracked changes vs base (committed + unstaged), then untracked new files.
	diffOut, err := gitOutput(workdir, "diff", "--name-only", "--diff-filter=ACMR", base)
	if err != nil {
		return nil
	}
	untrackedOut, _ := gitOutput(workdir, "ls-files", "--others", "--exclude-standard")

	var files []string
	seen := map[string]bool{}
	for _, block := range []string{diffOut, untrackedOut} {
		for _, line := range strings.Split(strings.TrimSpace(block), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || seen[line] {
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
			seen[line] = true
			files = append(files, line)
		}
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

// ScopeCommand is the string-command form of scopeArgv: it takes a shell-style
// gate command (e.g. "pnpm eslint .") and returns it with any trailing whole-repo
// target replaced by the explicit file list. Empty command or empty files returns
// the command unchanged. Used by craft/swarm, which pass gate commands as strings.
func ScopeCommand(command string, files []string) string {
	if command == "" || len(files) == 0 {
		return command
	}
	return strings.Join(scopeArgv(strings.Fields(command), files), " ")
}
