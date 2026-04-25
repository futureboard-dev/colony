package module

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

type LangCommands struct {
	Format    string
	TypeCheck string
	Test      string
}

func CommandsFor(lang string) (LangCommands, error) {
	switch strings.ToLower(lang) {
	case "typescript", "ts":
		return LangCommands{
			Format:    "pnpm prettier --write .",
			TypeCheck: "pnpm tsc --noEmit",
			Test:      "pnpm build",
		}, nil
	case "python", "py":
		return LangCommands{
			Format:    "ruff format .",
			TypeCheck: "mypy . --ignore-missing-imports",
			Test:      "pytest --tb=short",
		}, nil
	case "go":
		return LangCommands{
			Format:    "gofmt -w ./...",
			TypeCheck: "go build ./...",
			Test:      "go test ./... -count=1",
		}, nil
	default:
		return LangCommands{}, fmt.Errorf("unknown language %q — use: typescript, python, go", lang)
	}
}

// RunFormat runs the format command, treating failures as non-fatal warnings.
func RunFormat(command, workdir string, out io.Writer) {
	if err := runCmd(command, workdir, out); err != nil {
		fmt.Fprintf(out, "⚠ format had warnings (non-fatal)\n")
	}
}

// RunGateCapture runs a gate command and returns (combined output, error).
func RunGateCapture(command, workdir string) (string, error) {
	parts := strings.Fields(command)
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runCmd(command, workdir string, out io.Writer) error {
	parts := strings.Fields(command)
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = workdir
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}
