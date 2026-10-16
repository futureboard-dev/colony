package llm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/futureboard-dev/colony/pkg/config"
)

// diagnosticTail bounds memory even for long coding runs.
type diagnosticTail struct {
	mu   sync.Mutex
	text string
}

func (d *diagnosticTail) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	const limit = 64 * 1024
	if len(p) > limit {
		d.text = string(p[len(p)-limit:])
	} else {
		d.text += string(p)
		if len(d.text) > limit {
			d.text = d.text[len(d.text)-limit:]
		}
	}
	return len(p), nil
}

func quotaFailure(text string) bool {
	text = strings.ToLower(text)
	for _, marker := range []string{"you've hit your", "usage limit", "session limit", "rate limit", "rate_limit", "too many requests", "insufficient_quota", "quota exceeded"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// RunHeadless publishes only the successful attempt's response so a failed
// provider's error text cannot corrupt structured output consumed by callers.
func (e *Executor) RunHeadless(ctx context.Context, workdir, prompt string, out io.Writer) error {
	return e.withFallback(ctx, func(attempt *Executor, diagnostics io.Writer) error {
		var result bytes.Buffer
		err := attempt.runHeadless(ctx, workdir, prompt, io.MultiWriter(&result, diagnostics), diagnostics)
		if err == nil && out != nil {
			_, err = out.Write(result.Bytes())
		}
		return err
	})
}

// RunAgent preserves the worktree between attempts. Conversation state belongs
// to each CLI and is not transferred to the next provider.
func (e *Executor) RunAgent(ctx context.Context, workdir, prompt string, out io.Writer) error {
	return e.withFallback(ctx, func(attempt *Executor, diagnostics io.Writer) error {
		return attempt.runAgent(ctx, workdir, prompt, out, diagnostics)
	})
}

func (e *Executor) withFallback(ctx context.Context, run func(*Executor, io.Writer) error) error {
	candidates := append([]config.LLMConfig{e.cfg}, e.cfg.Fallbacks...)
	for i, cfg := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		var diagnostics diagnosticTail
		err := run(New(cfg), &diagnostics)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if i == len(candidates)-1 || !quotaFailure(diagnostics.text+" "+err.Error()) {
			return err
		}
		fmt.Fprintf(os.Stderr, "colony: %s quota/rate limit reached; trying fallback %s\n", New(cfg).CLI(), New(candidates[i+1]).CLI())
	}
	return nil
}
