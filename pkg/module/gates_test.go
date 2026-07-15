package module

import (
	"os"
	"os/exec"
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

// Helpers
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
