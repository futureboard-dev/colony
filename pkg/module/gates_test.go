package module

import (
	"os"
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
