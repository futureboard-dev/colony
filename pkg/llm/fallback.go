package llm

import (
	"bytes"
	"context"
	"fmt"
	"github.com/futureboard-dev/colony/pkg/handoff"
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
	result, err := e.RunHeadlessResult(ctx, workdir, prompt)
	if err == nil && out != nil {
		_, err = io.WriteString(out, result.Text)
	}
	return err
}

func (e *Executor) RunAgent(ctx context.Context, workdir, prompt string, out io.Writer) error {
	_, err := e.RunAgentResult(ctx, workdir, prompt, out)
	return err
}

// Result separates the final response from the complete execution transcript.
type Result struct{ Text, LogPath string }

func (e *Executor) RunAgentResult(ctx context.Context, workdir, prompt string, progress io.Writer) (Result, error) {
	return e.runResult(ctx, workdir, prompt, progress, false)
}
func (e *Executor) RunHeadlessResult(ctx context.Context, workdir, prompt string) (Result, error) {
	return e.runResult(ctx, workdir, prompt, io.Discard, true)
}

// cappedResponse fails closed for judges; every byte is also retained in the log.
type cappedResponse struct {
	bytes.Buffer
	overflow bool
}

func (b *cappedResponse) Write(p []byte) (int, error) {
	n := len(p)
	remaining := handoff.ResponseLimit - b.Len()
	if len(p) > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func (e *Executor) runResult(ctx context.Context, workdir, prompt string, progress io.Writer, headless bool) (Result, error) {
	log, err := handoff.Create(workdir, "execution")
	if err != nil {
		return Result{}, err
	}
	result := Result{LogPath: log.Name()}
	defer log.Close()
	if err = handoff.CheckPrompt(prompt); err != nil {
		return result, fmt.Errorf("%w; full log: %s", err, log.Name())
	}
	if progress == nil {
		progress = io.Discard
	}
	err = e.withFallback(ctx, func(attempt *Executor, diagnostics io.Writer) error {
		fmt.Fprintf(log, "\nProvider: %s; model: %s\n", attempt.CLI(), attempt.cfg.Model)
		var response cappedResponse
		var callErr error
		if headless {
			callErr = attempt.runHeadless(ctx, workdir, prompt, io.MultiWriter(&response, log, diagnostics), io.MultiWriter(log, diagnostics))
		} else {
			callErr = attempt.runAgent(ctx, workdir, prompt, progress, io.MultiWriter(log, diagnostics), io.MultiWriter(&response, log))
		}
		if callErr != nil {
			return callErr
		}
		if response.overflow && headless {
			return fmt.Errorf("final response exceeds %d bytes; review cannot approve incomplete output", handoff.ResponseLimit)
		}
		result.Text = response.String()
		if response.overflow {
			result.Text = handoff.Bound(result.Text, handoff.FeedbackLimit) + "\n[Final response truncated; see full log]"
		}
		return nil
	})
	if err != nil {
		return result, fmt.Errorf("%w; full log: %s", err, log.Name())
	}
	return result, nil
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
