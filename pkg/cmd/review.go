package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jirateep/colony/pkg/config"
	"github.com/jirateep/colony/pkg/llm"
	"github.com/jirateep/colony/pkg/module"
	"github.com/jirateep/colony/pkg/output"
	"github.com/jirateep/colony/pkg/prompt"
	"github.com/spf13/cobra"
)

type reviewFinding struct {
	Severity    string `json:"severity"`
	Lens        string `json:"lens"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Suggestion  string `json:"suggestion"`
}

type synthesizedReport struct {
	Verdict        string            `json:"verdict"`
	Findings       []reviewFinding   `json:"findings"`
	FileSummary    map[string]string `json:"file_summary"`
	OverallSummary string            `json:"overall_summary"`
}

var reviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Multi-lens AI code review for diffs and branches",
	Long: `Run an AI code review using multiple specialized lenses (bugs, slop, duplication, security).

Examples:
  colony review --branch feat/auth --base main
  colony review --diff changes.patch
  git diff main | colony review --diff -
  colony review --branch feat/auth --lens bugs,security
  colony review --branch feat/auth --ci`,
	RunE: runReview,
}

var (
	reviewBranch  string
	reviewBase    string
	reviewDiff    string
	reviewLenses  string
	reviewCI      bool
	reviewSummary bool
)

func init() {
	reviewCmd.Flags().StringVar(&reviewBranch, "branch", "", "branch to review (compares against --base)")
	reviewCmd.Flags().StringVar(&reviewBase, "base", "main", "base branch to compare against")
	reviewCmd.Flags().StringVar(&reviewDiff, "diff", "", "path to patch file, or '-' for stdin")
	reviewCmd.Flags().StringVar(&reviewLenses, "lens", "bugs,slop,duplication,security", "comma-separated list of lenses to run")
	reviewCmd.Flags().BoolVar(&reviewCI, "ci", false, "CI mode: outputs JSON and sets exit code based on verdict")
	reviewCmd.Flags().BoolVar(&reviewSummary, "summary", false, "output summary only")
}

func runReview(cmd *cobra.Command, args []string) error {
	if reviewBranch == "" && reviewDiff == "" {
		return fmt.Errorf("must specify either --branch or --diff")
	}
	if reviewBranch != "" && reviewDiff != "" {
		return fmt.Errorf("cannot specify both --branch and --diff")
	}

	cfg, root, err := loadConfig()
	if err != nil {
		return err
	}

	diffContent, err := getDiff(root)
	if err != nil {
		return err
	}

	if strings.TrimSpace(diffContent) == "" {
		if reviewCI {
			fmt.Println(`{"verdict": "PASS", "overall_summary": "Empty diff, nothing to review."}`)
			return nil
		}
		fmt.Println("Empty diff, nothing to review.")
		return nil
	}

	lenses := strings.Split(reviewLenses, ",")
	for i := range lenses {
		lenses[i] = strings.TrimSpace(lenses[i])
	}

	// Setup logging directory
	ts := time.Now().Format("20060102-150405")
	reviewDir := filepath.Join(module.LogDir(root), "reviews", "review-"+ts)
	if err := os.MkdirAll(filepath.Join(reviewDir, "raw"), 0755); err != nil {
		return err
	}

	// Write the diff
	if err := os.WriteFile(filepath.Join(reviewDir, "diff.patch"), []byte(diffContent), 0644); err != nil {
		return fmt.Errorf("write diff.patch: %w", err)
	}

	var statusLine *output.StatusLine
	var out io.Writer = os.Stdout

	if !reviewCI {
		heatmap := output.NewHeatmapWriter(os.Stdout)
		statusLine = output.NewStatusLine(os.Stdout, heatmap)
		defer statusLine.Close()
		statusLine.SetState(output.StateWorking)
		out = statusLine
	}

	if !reviewCI && !reviewSummary {
		fmt.Fprintf(out, "▶ Starting review with lenses: %s\n", strings.Join(lenses, ", "))
	}

	rawReports, lensErr := runReviewLenses(cmd.Context(), cfg, diffContent, lenses, statusLine, out, reviewCI)

	// Save raw reports regardless of error so partial state is debuggable.
	for l, r := range rawReports {
		_ = os.WriteFile(filepath.Join(reviewDir, "raw", l+".json"), []byte(r), 0644)
	}

	if lensErr != nil {
		return fmt.Errorf("lens execution failed: %w", lensErr)
	}

	synthReport, rawSynth, err := synthesizeReview(cmd.Context(), cfg, rawReports, statusLine)
	if err != nil {
		return fmt.Errorf("synthesis failed: %w\nRaw output:\n%s", err, rawSynth)
	}

	// Save final report
	_ = os.WriteFile(filepath.Join(reviewDir, "report.json"), []byte(rawSynth), 0644)
	_ = os.WriteFile(filepath.Join(reviewDir, "decision.txt"), []byte(synthReport.Verdict), 0644)

	// Format output
	if reviewCI {
		// CI mode: no statusLine was created, so direct os.Exit is safe.
		fmt.Println(rawSynth)
		switch synthReport.Verdict {
		case "FAIL":
			os.Exit(2)
		case "WARN":
			os.Exit(1)
		}
		return nil
	}

	if reviewSummary {
		fmt.Printf("%s — %s\n", synthReport.Verdict, synthReport.OverallSummary)
		return nil
	}

	// Rich terminal output
	printRichReport(out, synthReport, reviewBranch, reviewBase)

	fmt.Fprintf(out, "\nFull report saved to: %s\n", reviewDir)

	return nil
}

// runReviewLenses executes each review lens in parallel and returns the raw JSON reports.
// progress is an optional writer for human-visible status (skipped in CI).
func runReviewLenses(ctx context.Context, cfg *config.Config, diff string, lenses []string, statusLine *output.StatusLine, progress io.Writer, quiet bool) (map[string]string, error) {
	var wg sync.WaitGroup
	var mu sync.Mutex

	rawReports := make(map[string]string)
	errs := make(map[string]error)

	for _, lens := range lenses {
		wg.Add(1)
		go func(l string) {
			defer wg.Done()

			if statusLine != nil {
				statusLine.SetMessage(fmt.Sprintf("running %s lens", l))
			}

			p, err := prompt.RenderLens(l, diff)
			if err != nil {
				mu.Lock()
				errs[l] = err
				mu.Unlock()
				return
			}

			execAgent := llm.New(cfg.Role("reviewer"))
			var outBuf strings.Builder
			err = execAgent.RunHeadless(ctx, ".", p, &outBuf)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				errs[l] = err
				return
			}

			raw := extractJSONForReview(outBuf.String())
			if !json.Valid([]byte(raw)) {
				errs[l] = fmt.Errorf("lens %q returned non-JSON output (saved to raw/)", l)
				rawReports[l] = raw // keep for debugging
				return
			}
			rawReports[l] = raw
			if !quiet && progress != nil {
				fmt.Fprintf(progress, "  ✓ %s lens complete\n", l)
			}
		}(lens)
	}

	wg.Wait()

	if len(errs) > 0 {
		var errStrs []string
		for l, e := range errs {
			errStrs = append(errStrs, fmt.Sprintf("%s: %v", l, e))
		}
		sort.Strings(errStrs)
		return rawReports, fmt.Errorf("lens errors: %s", strings.Join(errStrs, ", "))
	}

	return rawReports, nil
}

// synthesizeReview merges all lens reports into a single synthesized report.
func synthesizeReview(ctx context.Context, cfg *config.Config, rawReports map[string]string, statusLine *output.StatusLine) (*synthesizedReport, string, error) {
	if statusLine != nil {
		statusLine.SetMessage("synthesizing report")
	}

	var reportsBuilder strings.Builder
	for l, r := range rawReports {
		fmt.Fprintf(&reportsBuilder, "=== Lens: %s ===\n%s\n\n", l, r)
	}

	p, err := prompt.RenderSynthesize(reportsBuilder.String())
	if err != nil {
		return nil, "", fmt.Errorf("render synthesize prompt: %w", err)
	}

	execAgent := llm.New(cfg.Role("reviewer"))
	var outBuf strings.Builder
	err = execAgent.RunHeadless(ctx, ".", p, &outBuf)
	if err != nil {
		return nil, "", fmt.Errorf("run synthesizer: %w", err)
	}

	rawOutput := extractJSONForReview(outBuf.String())
	var report synthesizedReport
	if err := json.Unmarshal([]byte(rawOutput), &report); err != nil {
		return nil, rawOutput, fmt.Errorf("parse synthesized report: %w", err)
	}

	return &report, rawOutput, nil
}

// extractJSONForReview attempts to extract a JSON block from markdown-wrapped output.
func extractJSONForReview(s string) string {
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(s, "```json"); ok {
		s = rest
	} else if rest, ok := strings.CutPrefix(s, "```"); ok {
		s = rest
	} else {
		return s
	}
	if idx := strings.LastIndex(s, "```"); idx != -1 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

func getDiff(root string) (string, error) {
	if reviewDiff != "" {
		if reviewDiff == "-" {
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				return "", fmt.Errorf("read stdin: %w", err)
			}
			return string(b), nil
		}
		b, err := os.ReadFile(reviewDiff)
		if err != nil {
			return "", fmt.Errorf("read diff file: %w", err)
		}
		return string(b), nil
	}

	// Use git: try merge-base style first, then fall back to direct diff.
	run := func(args ...string) (string, string, error) {
		c := exec.Command("git", args...)
		c.Dir = root
		var stdout, stderr bytes.Buffer
		c.Stdout = &stdout
		c.Stderr = &stderr
		err := c.Run()
		return stdout.String(), stderr.String(), err
	}

	out, stderr1, err := run("diff", reviewBase+"..."+reviewBranch)
	if err == nil {
		return out, nil
	}
	// Fallback for branches that don't share history.
	out, stderr2, err2 := run("diff", reviewBase, reviewBranch)
	if err2 != nil {
		return "", fmt.Errorf("git diff failed:\n  three-dot: %s\n  two-arg:   %s", strings.TrimSpace(stderr1), strings.TrimSpace(stderr2))
	}
	return out, nil
}

func printRichReport(w io.Writer, report *synthesizedReport, branch, base string) {
	title := "Local Diff"
	if branch != "" {
		title = fmt.Sprintf("%s → %s", branch, base)
	}

	fmt.Fprintf(w, "\n━━━ Colony Review: %s ━━━\n\n", title)

	// Group by lens (set by the synthesizer) with a deterministic order.
	lensLabels := map[string]string{
		"bugs":        "🐛 Bugs",
		"slop":        "🧹 AI Slop",
		"duplication": "📋 Duplication",
		"security":    "🔒 Security",
	}
	groupOrder := []string{"bugs", "security", "duplication", "slop"}
	grouped := make(map[string][]reviewFinding)
	for _, f := range report.Findings {
		key := strings.ToLower(strings.TrimSpace(f.Lens))
		if _, known := lensLabels[key]; !known {
			key = "general"
		}
		grouped[key] = append(grouped[key], f)
	}
	if _, ok := grouped["general"]; ok {
		groupOrder = append(groupOrder, "general")
		lensLabels["general"] = "General"
	}

	// Count severities
	crit, high, med, low := 0, 0, 0, 0
	for _, f := range report.Findings {
		switch strings.ToLower(f.Severity) {
		case "critical":
			crit++
		case "high":
			high++
		case "medium":
			med++
		case "low":
			low++
		}
	}

	for _, key := range groupOrder {
		findings, ok := grouped[key]
		if !ok {
			continue
		}
		fmt.Fprintf(w, "%s (%d)\n", lensLabels[key], len(findings))
		for _, f := range findings {
			icon := "·"
			sev := strings.ToUpper(f.Severity)
			switch sev {
			case "CRITICAL", "HIGH":
				icon = "✗"
			case "MEDIUM":
				icon = "⚠"
			}

			fmt.Fprintf(w, "  %s %s %s:%d — %s\n", icon, sev, f.File, f.Line, f.Description)
			if f.Suggestion != "" {
				fmt.Fprintf(w, "    → %s\n", f.Suggestion)
			}
			fmt.Fprintln(w)
		}
	}

	fmt.Fprintf(w, "━━━ Verdict: %s ━━━ %d critical · %d high · %d medium · %d low\n",
		report.Verdict, crit, high, med, low)
	fmt.Fprintf(w, "%s\n", report.OverallSummary)
}
