package llm

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// streamEvent is the subset of the claude stream-json event schema that
// streamClaude needs. Unknown fields are ignored.
type streamEvent struct {
	Type    string `json:"type"`
	IsError bool   `json:"is_error"`
	Message struct {
		Content []streamBlock `json:"content"`
	} `json:"message"`
}

type streamBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type toolInput struct {
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Command      string `json:"command"`
	Pattern      string `json:"pattern"`
	Path         string `json:"path"`
}

// streamClaude reads NDJSON events from r — the output of
// `claude -p --output-format stream-json --verbose` — and writes a compact,
// one-line-per-action view to out. Lines that aren't valid JSON events are
// passed through unchanged so nothing is silently swallowed (e.g. stderr text).
func streamClaude(r io.Reader, out io.Writer) error {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if s := strings.TrimSpace(line); s != "" {
			renderEvent(s, out)
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func renderEvent(line string, out io.Writer) {
	var ev streamEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		// Not a JSON event — pass through so output is never lost.
		fmt.Fprintln(out, line)
		return
	}
	switch ev.Type {
	case "assistant":
		for _, b := range ev.Message.Content {
			switch b.Type {
			case "text":
				if t := strings.TrimSpace(b.Text); t != "" {
					fmt.Fprintln(out, t)
				}
			case "tool_use":
				fmt.Fprintf(out, "  %s\n", formatTool(b.Name, b.Input))
			}
		}
	case "result":
		if ev.IsError {
			fmt.Fprintln(out, "  ⚠ agent run ended with an error")
		}
	}
}

// formatTool renders a tool_use block as a single compact line.
func formatTool(name string, raw json.RawMessage) string {
	var in toolInput
	_ = json.Unmarshal(raw, &in)
	switch name {
	case "Write":
		return "→ Write " + in.FilePath
	case "Edit", "MultiEdit":
		return "✎ Edit " + in.FilePath
	case "NotebookEdit":
		return "✎ Edit " + in.NotebookPath
	case "Bash":
		return "→ Bash " + truncate(collapseSpaces(in.Command), 80)
	case "Read":
		return "· Read " + in.FilePath
	case "Glob":
		return "· Glob " + firstNonEmpty(in.Pattern, in.Path)
	case "Grep":
		return "· Grep " + firstNonEmpty(in.Pattern, in.Path)
	default:
		return "→ " + name
	}
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
