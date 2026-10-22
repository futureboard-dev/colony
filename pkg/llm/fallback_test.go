package llm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/futureboard-dev/colony/pkg/config"
)

func TestFallbackPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, diagnostic string
		cancel           bool
		want             int
	}{
		{"quota", "You've hit your session limit", false, 2},
		{"rate", "rate_limit_error", false, 2},
		{"ordinary failure", "build failed", false, 1},
		{"cancelled", "usage limit", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e := New(config.LLMConfig{Provider: "anthropic", Fallbacks: []config.LLMConfig{{Provider: "openai", CLI: "codex"}}})
			calls := 0
			err := e.withFallback(ctx, func(attempt *Executor, out io.Writer) error {
				calls++
				if calls == 2 {
					if attempt.CLI() != "codex" {
						t.Fatal("wrong fallback")
					}
					return nil
				}
				fmt.Fprint(out, tc.diagnostic)
				if tc.cancel {
					cancel()
				}
				return errors.New("exit status 1")
			})
			if calls != tc.want {
				t.Fatalf("calls=%d want %d", calls, tc.want)
			}
			if tc.want == 2 && err != nil {
				t.Fatal(err)
			}
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestFallbackExecution(t *testing.T) {
	dir := t.TempDir()
	for name, script := range map[string]string{
		"claude": "#!/bin/sh\necho \"You've hit your session limit\"\necho partial > partial.txt\nexit 1\n",
		"codex":  "#!/bin/sh\nif [ ! -f partial.txt ]; then exit 2; fi\necho success\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	e := New(config.LLMConfig{Provider: "anthropic", Fallbacks: []config.LLMConfig{{Provider: "openai", CLI: "codex"}}})
	var out bytes.Buffer
	if err := e.RunHeadless(context.Background(), dir, "prompt", &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "success\n" {
		t.Fatalf("failed output leaked: %q", out.String())
	}
	out.Reset()
	if err := e.RunAgent(context.Background(), dir, "prompt", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "success") {
		t.Fatal(out.String())
	}
}

func TestFallbackExhaustion(t *testing.T) {
	e := New(config.LLMConfig{Fallbacks: []config.LLMConfig{{CLI: "codex"}}})
	calls := 0
	failure := errors.New("quota exceeded")
	err := e.withFallback(context.Background(), func(_ *Executor, _ io.Writer) error { calls++; return failure })
	if err != failure || calls != 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestFallbackClaudeErrorResult(t *testing.T) {
	dir := t.TempDir()
	scripts := map[string]string{
		"claude": "#!/bin/sh\necho '{\"type\":\"result\",\"is_error\":true,\"result\":\"session limit\"}'\n",
		"codex":  "#!/bin/sh\necho recovered\n",
	}
	for name, script := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	e := New(config.LLMConfig{Provider: "anthropic", Fallbacks: []config.LLMConfig{{Provider: "openai", CLI: "codex"}}})
	var out bytes.Buffer
	if err := e.RunAgent(context.Background(), dir, "prompt", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "recovered") {
		t.Fatal(out.String())
	}
}
