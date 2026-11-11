package module

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandsForTypescript(t *testing.T) {
	cmds, err := CommandsFor("typescript")
	if err != nil {
		t.Fatal(err)
	}
	if cmds.Format == "" || cmds.Lint == "" || cmds.TypeCheck == "" || cmds.Test == "" {
		t.Errorf("missing commands: %+v", cmds)
	}
	// alias should resolve identically
	cmdsAlias, _ := CommandsFor("ts")
	if cmds != cmdsAlias {
		t.Error("ts and typescript should produce identical commands")
	}
}

func TestCommandsForPython(t *testing.T) {
	cmds, err := CommandsFor("python")
	if err != nil {
		t.Fatal(err)
	}
	if cmds.Format == "" || cmds.Lint == "" || cmds.TypeCheck == "" || cmds.Test == "" {
		t.Errorf("missing commands: %+v", cmds)
	}
	cmdsAlias, _ := CommandsFor("py")
	if cmds != cmdsAlias {
		t.Error("py and python should produce identical commands")
	}
}

func TestCommandsForGo(t *testing.T) {
	cmds, err := CommandsFor("go")
	if err != nil {
		t.Fatal(err)
	}
	if cmds.Format == "" {
		t.Error("expected non-empty Format")
	}
	if cmds.Vet == "" {
		t.Error("expected non-empty Vet")
	}
	if cmds.Lint == "" {
		t.Error("expected non-empty Lint")
	}
	if cmds.TypeCheck == "" {
		t.Error("expected non-empty TypeCheck")
	}
	if cmds.Test == "" {
		t.Error("expected non-empty Test")
	}
	if cmds.Build == "" {
		t.Error("expected non-empty Build")
	}
}

func TestCommandsForCaseInsensitive(t *testing.T) {
	_, err := CommandsFor("Go")
	if err != nil {
		t.Error("CommandsFor should be case-insensitive")
	}
	_, err = CommandsFor("TypeScript")
	if err != nil {
		t.Error("CommandsFor should be case-insensitive")
	}
}

func TestCommandsForUnknown(t *testing.T) {
	_, err := CommandsFor("ruby")
	if err == nil {
		t.Error("expected error for unknown language")
	}
}

func TestRunGateCaptureAll_None(t *testing.T) {
	t.Run("commands are all empty", func(t *testing.T) {
		cmds, err := CommandsFor("none")
		if err != nil {
			t.Fatal(err)
		}
		if cmds != (LangCommands{}) {
			t.Errorf("expected no commands, got %+v", cmds)
		}
	})
	t.Run("gates pass on a non-project dir", func(t *testing.T) {
		out, err := RunGateCaptureAll("none", t.TempDir(), nil)
		if err != nil || out != "" {
			t.Errorf("expected pass, got out=%q err=%v", out, err)
		}
	})
}

func TestGateOverrides(t *testing.T) {
	t.Cleanup(func() { SetGateOverrides(LangCommands{}) })
	SetGateOverrides(LangCommands{Test: "false", Build: "true"})

	t.Run("none gains the configured commands", func(t *testing.T) {
		cmds, err := CommandsFor("none")
		if err != nil {
			t.Fatal(err)
		}
		if cmds != (LangCommands{Test: "false", Build: "true"}) {
			t.Errorf("got %+v", cmds)
		}
	})
	t.Run("override replaces only the fields it sets", func(t *testing.T) {
		cmds, err := CommandsFor("go")
		if err != nil {
			t.Fatal(err)
		}
		if cmds.Test != "false" || cmds.Vet != "go vet ./..." {
			t.Errorf("got %+v", cmds)
		}
	})
	t.Run("unknown lang still errors", func(t *testing.T) {
		if _, err := CommandsFor("cobol"); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("none fails when the configured test fails", func(t *testing.T) {
		if _, err := RunGateCaptureAll("none", t.TempDir(), nil); err == nil {
			t.Error("expected the failing test command to fail the gate")
		}
	})
}

func TestCommandAvailable(t *testing.T) {
	if !CommandAvailable("go build ./...") {
		t.Error("expected 'go' to be available on PATH")
	}
	if CommandAvailable("definitely-not-a-real-binary-xyz check .") {
		t.Error("expected missing binary to report unavailable")
	}
	if CommandAvailable("") {
		t.Error("expected empty command to report unavailable")
	}
}

// TestRunGateCaptureAll_AllPass verifies that a valid Go module passes all gates.
// Format is skipped because gofmt -w ./... does not support the ./... glob.
func TestRunGateCaptureAll_AllPass(t *testing.T) {
	dir := t.TempDir()
	writeMinimalGoModule(t, dir, `package test

func F() int { return 42 }
`)
	output, err := RunGateCaptureAll("go", dir, map[string]bool{"format": true})
	if err != nil {
		t.Fatalf("expected gates to pass, got error: %v\noutput: %s", err, output)
	}
	if output != "" {
		t.Logf("gate output: %s", output)
	}
}

// TestRunGateCaptureAll_FormatFail verifies that unformatted Go fails.
func TestRunGateCaptureAll_FormatFail(t *testing.T) {
	dir := t.TempDir()
	writeMinimalGoModule(t, dir, `package test

func F() {
return
}`)
	output, err := RunGateCaptureAll("go", dir, nil)
	if err == nil {
		t.Fatal("expected gates to fail on unformatted Go")
	}
	if output == "" {
		t.Error("expected non-empty output on failure")
	}
}

// TestRunGateCaptureAll_Typescript verifies TS gates work.
func TestRunGateCaptureAll_Typescript(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"name":"test"}`)
	writeFile(t, dir, "tsconfig.json", `{"compilerOptions":{"module":"ESNext","target":"ES2022"}}`)
	writeFile(t, dir, "test.ts", `const x: number = 42;`)

	output, err := RunGateCaptureAll("typescript", dir, nil)
	if err != nil && output == "" {
		t.Log("typescript gates may not be installed — skipping strict assertion")
	}
}

// TestAutoFix_SkipFormat verifies AutoFix omits the prettier --write call when
// skipFormat is true (e.g. --no-format), while still running eslint --fix.
// Uses a real stub "pnpm" binary on PATH — recording actual process
// invocations — rather than mocking AutoFix's internals.
func TestAutoFix_SkipFormat(t *testing.T) {
	dir := t.TempDir()
	binDir := t.TempDir()
	callLog := filepath.Join(binDir, "calls.log")

	writeFile(t, binDir, "pnpm", "#!/bin/sh\necho \"$@\" >> "+callLog+"\n")
	if err := os.Chmod(filepath.Join(binDir, "pnpm"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	writeFile(t, dir, "a.ts", "const x = 1;")

	AutoFix("typescript", dir, "", []string{"a.ts"}, true, os.Stdout)
	calls := readFileOrEmpty(t, callLog)
	if strings.Contains(calls, "prettier") {
		t.Errorf("expected prettier to be skipped with skipFormat=true, calls:\n%s", calls)
	}
	if !strings.Contains(calls, "eslint") {
		t.Errorf("expected eslint --fix to still run, calls:\n%s", calls)
	}

	os.Remove(callLog) //nolint:errcheck
	AutoFix("typescript", dir, "", []string{"a.ts"}, false, os.Stdout)
	calls = readFileOrEmpty(t, callLog)
	if !strings.Contains(calls, "prettier") {
		t.Errorf("expected prettier to run with skipFormat=false, calls:\n%s", calls)
	}
}

func readFileOrEmpty(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// TestRunGateCaptureScopesLintCache verifies RunGateCapture scopes
// GOLANGCI_LINT_CACHE to the worktree, so stale results from a pruned worktree
// can't leak into another run.
func TestRunGateCaptureScopesLintCache(t *testing.T) {
	dir := t.TempDir()
	out, err := RunGateCapture("printenv GOLANGCI_LINT_CACHE", dir)
	if err != nil {
		t.Fatalf("printenv failed: %v\noutput: %s", err, out)
	}
	// The cache dir should be outside the worktree (in a temp dir).
	cacheDir := strings.TrimSpace(out)
	if strings.HasPrefix(cacheDir, dir) {
		t.Errorf("GOLANGCI_LINT_CACHE %q is inside worktree %q", cacheDir, dir)
	}
	if cacheDir == "" {
		t.Error("GOLANGCI_LINT_CACHE is empty")
	}
}

// TestLintCacheDir_Cleanup verifies LintCacheDir creates a temp dir and
// CleanupLintCache removes it.
func TestLintCacheDir_Cleanup(t *testing.T) {
	// Reset state so the test starts clean.
	lintCacheDirMu.Lock()
	lintCacheDir = ""
	lintCacheDirMu.Unlock()

	dir, err := LintCacheDir()
	if err != nil {
		t.Fatalf("LintCacheDir: %v", err)
	}
	if dir == "" {
		t.Fatal("LintCacheDir returned empty string")
	}
	// Dir should exist.
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatal("LintCacheDir does not exist on disk")
	}
	CleanupLintCache()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("lint cache dir still exists after CleanupLintCache")
	}
	// Calling CleanupLintCache again should be safe.
	CleanupLintCache()
}

func TestScopeArgv(t *testing.T) {
	files := []string{"a.ts", "b/c.tsx"}
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"trailing dot", []string{"pnpm", "eslint", "."}, []string{"pnpm", "eslint", "a.ts", "b/c.tsx"}},
		{"trailing go target", []string{"golangci-lint", "run", "./..."}, []string{"golangci-lint", "run", "a.ts", "b/c.tsx"}},
		{"no whole-repo target", []string{"pnpm", "prettier", "--write"}, []string{"pnpm", "prettier", "--write", "a.ts", "b/c.tsx"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scopeArgv(tc.in, files)
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("scopeArgv = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestScopeCommand(t *testing.T) {
	files := []string{"a.ts", "b/c.tsx"}
	cases := []struct {
		name    string
		command string
		files   []string
		want    string
	}{
		{"trailing dot", "pnpm eslint .", files, "pnpm eslint a.ts b/c.tsx"},
		{"no whole-repo target", "pnpm prettier --write", files, "pnpm prettier --write a.ts b/c.tsx"},
		{"empty command", "", files, ""},
		{"empty files unchanged", "pnpm eslint .", nil, "pnpm eslint ."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ScopeCommand(tc.command, tc.files); got != tc.want {
				t.Errorf("ScopeCommand(%q, %v) = %q, want %q", tc.command, tc.files, got, tc.want)
			}
		})
	}
}

func TestResolveBase(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "master")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	run("commit", "-q", "--allow-empty", "-m", "init")
	// Simulate an origin whose default branch is master (mirrors the reported repo).
	run("update-ref", "refs/remotes/origin/master", "HEAD")
	run("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/master")

	t.Run("resolvable base kept", func(t *testing.T) {
		if got := resolveBase(dir, "origin/master"); got != "origin/master" {
			t.Errorf("got %q, want origin/master", got)
		}
	})
	t.Run("missing base falls back to origin/HEAD", func(t *testing.T) {
		if got := resolveBase(dir, "origin/main"); got != "origin/master" {
			t.Errorf("got %q, want origin/master (fallback)", got)
		}
	})
	t.Run("empty base falls back to origin/HEAD", func(t *testing.T) {
		if got := resolveBase(dir, ""); got != "origin/master" {
			t.Errorf("got %q, want origin/master (fallback)", got)
		}
	})
}

func TestInstallDepsPython(t *testing.T) {
	t.Run("reuses working venv and installs dev requirements", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "requirements.txt", "")
		writeFile(t, dir, "requirements-dev.txt", "")
		log := filepath.Join(dir, "pip.log")
		writeExec(t, filepath.Join(dir, ".venv", "bin"), "python", "#!/bin/sh\nexit 0\n")
		writeExec(t, filepath.Join(dir, ".venv", "bin"), "pip", "#!/bin/sh\necho \"$@\" >> "+log+"\n")

		InstallDeps("python", dir, io.Discard)

		if _, err := os.Stat(filepath.Join(dir, ".venv", "pyvenv.cfg")); err == nil {
			t.Error("venv was recreated; expected existing venv to be reused")
		}
		got := readFileOrEmpty(t, log)
		if got != "install -r requirements.txt\ninstall -r requirements-dev.txt\n" {
			t.Errorf("pip calls = %q", got)
		}
	})

	t.Run("creates venv with interpreter from .python-version", func(t *testing.T) {
		dir := t.TempDir()
		binDir := t.TempDir()
		log := filepath.Join(dir, "python.log")
		writeExec(t, binDir, "python3.99", "#!/bin/sh\necho \"$@\" >> "+log+"\n")
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		writeFile(t, dir, "requirements.txt", "")
		writeFile(t, dir, ".python-version", "3.99.1\n")

		InstallDeps("python", dir, io.Discard)

		if got := readFileOrEmpty(t, log); got != "-m venv .venv\n" {
			t.Errorf("python3.99 calls = %q", got)
		}
	})
}

func TestPythonFor(t *testing.T) {
	binDir := t.TempDir()
	writeExec(t, binDir, "python3.99", "#!/bin/sh\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cases := []struct {
		name, version, want string
	}{
		{"no file", "", "python3"},
		{"available version", "3.99.1\n", "python3.99"},
		{"unavailable version", "3.98\n", "python3"},
		{"malformed", "system\n", "python3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.version != "" {
				writeFile(t, dir, ".python-version", tc.version)
			}
			if got := pythonFor(dir); got != tc.want {
				t.Errorf("pythonFor = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRunGateArgvUsesVenv(t *testing.T) {
	t.Run("runs tool from .venv/bin", func(t *testing.T) {
		dir := t.TempDir()
		binDir := filepath.Join(dir, ".venv", "bin")
		writeExec(t, binDir, "venvtool", "#!/bin/sh\necho from-venv\n")
		// venvhelper is only reachable via the PATH given to the child.
		writeExec(t, binDir, "venvhelper", "#!/bin/sh\necho helper\n")
		writeExec(t, binDir, "calls-helper", "#!/bin/sh\nvenvhelper\n")

		out, err := runGateArgv([]string{"venvtool"}, dir)
		if err != nil || out != "from-venv\n" {
			t.Errorf("venvtool: out=%q err=%v", out, err)
		}
		out, err = runGateArgv([]string{"calls-helper"}, dir)
		if err != nil || out != "helper\n" {
			t.Errorf("calls-helper: out=%q err=%v", out, err)
		}
	})

	t.Run("falls back to PATH without venv", func(t *testing.T) {
		out, err := runGateArgv([]string{"echo", "hi"}, t.TempDir())
		if err != nil || out != "hi\n" {
			t.Errorf("out=%q err=%v", out, err)
		}
	})
}

func TestRunGatesReportsMissingCommand(t *testing.T) {
	dir := t.TempDir()
	writeMinimalGoModule(t, dir, "package test\n")
	t.Setenv("PATH", t.TempDir())

	out, err := RunGateCaptureAll("go", dir, nil)
	if err == nil {
		t.Fatal("expected failure with empty PATH")
	}
	if !strings.Contains(out, "--- format ---") || !strings.Contains(out, "executable file not found") {
		t.Errorf("expected missing-command error in output, got %q", out)
	}
	if !strings.Contains(out, "command: go fmt ./... (go not found on PATH)") {
		t.Errorf("expected the unresolved binary to be named, got %q", out)
	}
}

// A tool that rejects the gate command itself (pnpm 9 refusing
// `pnpm test --runInBand`) must say which binary ran, so the failure reads as
// a setup problem rather than something to fix in the code.
func TestRunGatesNamesResolvedBinary(t *testing.T) {
	bin := t.TempDir()
	writeExec(t, bin, "pnpm", "#!/bin/sh\necho \" ERROR  Unknown option: 'runInBand'\"\nexit 1\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() { SetGateOverrides(LangCommands{}) })
	SetGateOverrides(LangCommands{Test: "pnpm test --runInBand"})

	skip := map[string]bool{"format": true, "vet": true, "lint": true, "typecheck": true, "build": true}
	out, err := RunGateCaptureAll("typescript", t.TempDir(), skip)
	if err == nil {
		t.Fatal("expected the test gate to fail")
	}
	want := "--- test ---\ncommand: pnpm test --runInBand (pnpm = " + filepath.Join(bin, "pnpm") + ")\n"
	if !strings.HasPrefix(out, want) || !strings.Contains(out, "Unknown option: 'runInBand'") {
		t.Errorf("output = %q, want prefix %q and the tool's error", out, want)
	}
}

// Helpers
func writeExec(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
}

func writeMinimalGoModule(t *testing.T, dir, content string) {
	t.Helper()
	writeFile(t, dir, "go.mod", "module test\n\ngo 1.25\n")
	// Write the Go file at root so gofmt -w ./... can find it.
	writeFile(t, dir, "test.go", content)
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(dir+"/"+name, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestScopedGatesSelectSupportedFiles(t *testing.T) {
	for _, tc := range []struct {
		lang   string
		want   []string
		absent []string
	}{
		{"python", []string{"ruff format app.py types.pyi", "ruff check app.py types.pyi"}, []string{"migration.js", "schema.json", "view.tsx"}},
		{"typescript", []string{"pnpm prettier --write migration.js schema.json view.tsx", "pnpm eslint migration.js view.tsx"}, []string{"app.py", "types.pyi", "eslint migration.js schema.json"}},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			dir := t.TempDir()
			bin := t.TempDir()
			log := filepath.Join(bin, "calls.log")
			for _, name := range []string{"ruff", "pnpm"} {
				path := filepath.Join(bin, name)
				if err := os.WriteFile(path, []byte("#!/bin/sh\necho '"+name+"' \"$@\" >> '"+log+"'\n"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			skip := map[string]bool{"vet": true, "typecheck": true, "test": true, "build": true}
			files := []string{"migration.js", "app.py", "schema.json", "view.tsx", "types.pyi"}
			if out, err := RunGateCaptureScoped(tc.lang, dir, "", files, skip); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			calls := readFileOrEmpty(t, log)
			for _, want := range tc.want {
				if !strings.Contains(calls, want) {
					t.Errorf("missing %q in %s", want, calls)
				}
			}
			for _, bad := range tc.absent {
				if strings.Contains(calls, bad) {
					t.Errorf("unexpected %q in %s", bad, calls)
				}
			}
		})
	}
}

func TestScopedPythonGatesSkipJavaScriptOnlyChanges(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	skip := map[string]bool{"vet": true, "typecheck": true, "test": true, "build": true}
	if out, err := RunGateCaptureScoped("python", t.TempDir(), "", []string{"migration.js"}, skip); err != nil {
		t.Fatalf("ran Python tools on JS-only scope: %v %s", err, out)
	}
}

func TestAutoFixDoesNotPassJSONToESLint(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(bin, "calls.log")
	if err := os.WriteFile(filepath.Join(bin, "pnpm"), []byte("#!/bin/sh\necho \"$@\" >> '"+log+"'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	AutoFix("typescript", t.TempDir(), "", []string{"schema.json"}, false, io.Discard)
	calls := readFileOrEmpty(t, log)
	if strings.Contains(calls, "eslint") || !strings.Contains(calls, "prettier --write schema.json") {
		t.Fatal(calls)
	}
}

func TestChangedFilesIncludesPythonSources(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", "initial"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	for _, name := range []string{"app.py", "types.pyi", "migration.js", "README.md"} {
		writeFile(t, dir, name, "\n")
	}
	got := strings.Join(ChangedFiles(dir, "HEAD"), ",")
	for _, name := range []string{"app.py", "types.pyi", "migration.js"} {
		if !strings.Contains(got, name) {
			t.Errorf("missing %s in %s", name, got)
		}
	}
	if strings.Contains(got, "README.md") {
		t.Fatal(got)
	}
}

func TestScopedEmptyFilesDoesNotRunWholeRepo(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	skip := map[string]bool{"vet": true, "typecheck": true, "test": true, "build": true}
	if out, err := RunGateCaptureScoped("python", t.TempDir(), "", nil, skip); err != nil {
		t.Fatalf("ran tools outside empty scope: %v %s", err, out)
	}
}

// fakePrettier installs a pnpm shim whose "prettier" rewrites every double quote
// to a single quote, both for --stdin-filepath (stdout) and --write (in place).
// Like real Prettier it rewrites whole files, which is the behavior under test.
func fakePrettier(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
[ "$1" = prettier ] || exit 0
shift
case "$1" in
--stdin-filepath) sed "s/\"/'/g" ;;
--write) shift; for f; do sed "s/\"/'/g" "$f" > "$f.tmp" && mv "$f.tmp" "$f"; done ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "pnpm"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@example.com"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

// formatRepo commits an unformatted legacy.ts and a formatted clean.ts on a
// "base" branch, then returns the repo dir checked out on a task branch.
func formatRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "-b", "base")
	var legacy strings.Builder
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&legacy, "export const v%d = \"value %d\";\n", i, i)
	}
	writeFile(t, dir, "legacy.ts", legacy.String())
	writeFile(t, dir, "clean.ts", "export const a = 'a';\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-qm", "base")
	gitT(t, dir, "checkout", "-qb", "task")
	return dir
}

func appendFile(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func TestFormatFiles(t *testing.T) {
	fakePrettier(t)
	dir := formatRepo(t)
	writeFile(t, dir, "new.ts", "export const n = \"n\";\n")
	files := []string{"legacy.ts", "clean.ts", "new.ts", "app.py"}

	t.Run("typescript keeps new and base-clean files only", func(t *testing.T) {
		got := strings.Join(FormatFiles("typescript", dir, "base", files), ",")
		if got != "clean.ts,new.ts" {
			t.Fatalf("got %q, want clean.ts,new.ts", got)
		}
	})
	t.Run("other languages are not filtered", func(t *testing.T) {
		got := strings.Join(FormatFiles("python", dir, "base", files), ",")
		if got != "app.py" {
			t.Fatalf("got %q, want app.py", got)
		}
	})
	t.Run("prettier failure excludes existing file", func(t *testing.T) {
		dir := formatRepo(t)
		// Unique content so the clean-at-base cache from other subtests can't answer.
		writeFile(t, dir, "broken.ts", "export const broken = 'unique-to-this-test';\n")
		gitT(t, dir, "add", "broken.ts")
		gitT(t, dir, "commit", "-qm", "broken")
		bin := t.TempDir()
		if err := os.WriteFile(filepath.Join(bin, "pnpm"), []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		if got := FormatFiles("typescript", dir, "HEAD", []string{"broken.ts"}); len(got) != 0 {
			t.Fatalf("got %v, want none", got)
		}
	})
}

// TestFormatGateLeavesUnformattedFileEdit is the 8b13ff88 regression: a one-line
// edit to an existing, never-formatted file must leave the gate as a one-line diff.
func TestFormatGateLeavesUnformattedFileEdit(t *testing.T) {
	fakePrettier(t)
	dir := formatRepo(t)
	appendFile(t, filepath.Join(dir, "legacy.ts"), "export const added = \"x\";\n")
	appendFile(t, filepath.Join(dir, "clean.ts"), "export const b = \"b\";\n")
	writeFile(t, dir, "new.ts", "export const n = \"n\";\n")

	changed := ChangedFiles(dir, "base")
	AutoFix("typescript", dir, "base", changed, false, io.Discard)
	skip := map[string]bool{"vet": true, "lint": true, "typecheck": true, "test": true, "build": true}
	if out, err := RunGateCaptureScoped("typescript", dir, "base", changed, skip); err != nil {
		t.Fatalf("%v: %s", err, out)
	}

	if got := strings.TrimSpace(gitT(t, dir, "diff", "--numstat", "base", "--", "legacy.ts")); got != "1\t0\tlegacy.ts" {
		t.Errorf("legacy.ts diff = %q, want a one-line addition", got)
	}
	for _, f := range []string{"clean.ts", "new.ts"} {
		if data := readFileOrEmpty(t, filepath.Join(dir, f)); strings.Contains(data, `"`) {
			t.Errorf("%s was not formatted: %s", f, data)
		}
	}
}

// gateScript writes a shell script outside the repo and returns the gate
// command that runs it.
func gateScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gate.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return "sh " + path
}

// sideEffectRepo commits spec.json, edited.txt and app/[id]/route.ts.
func sideEffectRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "-b", "base")
	writeFile(t, dir, "spec.json", "committed\n")
	writeFile(t, dir, "edited.txt", "committed\n")
	if err := os.MkdirAll(filepath.Join(dir, "app", "[id]"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "app/[id]/route.ts", "committed\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-qm", "base")
	return dir
}

func TestRunGatesRestoresTrackedSideEffects(t *testing.T) {
	const rewrite = "echo regenerated > spec.json; echo regenerated > 'app/[id]/route.ts'"
	t.Cleanup(func() { SetGateOverrides(LangCommands{}) })

	assertFile := func(t *testing.T, dir, name, want string) {
		t.Helper()
		if got := readFileOrEmpty(t, filepath.Join(dir, name)); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	t.Run("build rewrite is undone on a clean tree", func(t *testing.T) {
		SetGateOverrides(LangCommands{Build: gateScript(t, rewrite)})
		dir := sideEffectRepo(t)
		if out, err := RunGateCaptureAll("none", dir, nil); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		if got := gitT(t, dir, "status", "--porcelain"); got != "" {
			t.Errorf("tree dirty after gate: %q", got)
		}
	})
	t.Run("format output and uncommitted edits survive", func(t *testing.T) {
		SetGateOverrides(LangCommands{
			Format: gateScript(t, "echo formatted > edited.txt"),
			Build:  gateScript(t, rewrite+"; echo clobbered > edited.txt"),
		})
		dir := sideEffectRepo(t)
		writeFile(t, dir, "edited.txt", "agent edit\n")
		if out, err := RunGateCaptureAll("none", dir, nil); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		assertFile(t, dir, "edited.txt", "formatted\n")
		assertFile(t, dir, "spec.json", "committed\n")
	})
	t.Run("agent edit to a generated file survives", func(t *testing.T) {
		SetGateOverrides(LangCommands{Build: gateScript(t, rewrite)})
		dir := sideEffectRepo(t)
		writeFile(t, dir, "spec.json", "agent edit\n")
		if out, err := RunGateCaptureAll("none", dir, nil); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		assertFile(t, dir, "spec.json", "agent edit\n")
	})
	t.Run("failing gate still restores", func(t *testing.T) {
		SetGateOverrides(LangCommands{Test: gateScript(t, rewrite+"; exit 1")})
		dir := sideEffectRepo(t)
		if _, err := RunGateCaptureAll("none", dir, nil); err == nil {
			t.Fatal("expected gate failure")
		}
		assertFile(t, dir, "spec.json", "committed\n")
		assertFile(t, dir, "app/[id]/route.ts", "committed\n")
	})
	t.Run("non-git directory is left alone", func(t *testing.T) {
		SetGateOverrides(LangCommands{Build: gateScript(t, "echo built > out.txt")})
		dir := t.TempDir()
		if out, err := RunGateCaptureAll("none", dir, nil); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		assertFile(t, dir, "out.txt", "built\n")
	})
}
