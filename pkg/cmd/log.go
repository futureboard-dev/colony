package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jirateep/colony/pkg/module"
	"github.com/spf13/cobra"
)

var logCmd = &cobra.Command{
	Use:   "log",
	Short: "Show agent run history for the current project",
	Long: `Displays blueprint and swarm run history from .colony/logs/.

Flags:
  --all      show runs across all projects
  --live     tail live agent activity (commands + file changes)
  --session  show what the current/last session touched`,
	RunE: runLog,
}

var (
	logAll     bool
	logLive    bool
	logSession bool
)

func init() {
	logCmd.Flags().BoolVar(&logAll, "all", false, "show runs across all projects")
	logCmd.Flags().BoolVar(&logLive, "live", false, "tail live agent activity")
	logCmd.Flags().BoolVar(&logSession, "session", false, "show last session summary")
}

func runLog(cmd *cobra.Command, args []string) error {
	cfg, root, err := loadConfig()
	_ = cfg
	if err != nil {
		return err
	}

	logDir := module.LogDir(root)
	project := module.ProjectName(root)

	// ── LIVE MODE ──────────────────────────────────────────────────────────────
	if logLive {
		cmdLog := filepath.Join(logDir, "commands.log")
		fileLog := filepath.Join(logDir, "file-changes.log")
		for _, f := range []string{cmdLog, fileLog} {
			os.MkdirAll(filepath.Dir(f), 0755) //nolint:errcheck
			if _, err := os.Stat(f); os.IsNotExist(err) {
				os.WriteFile(f, nil, 0644) //nolint:errcheck
			}
		}
		// follow every blueprint log touched recently — one per active agent
		tailFiles := []string{cmdLog, fileLog}
		active := activeBlueprints(logDir, 15*time.Minute)
		tailFiles = append(tailFiles, active...)
		fmt.Printf("\n📡 LIVE — %s\n", project)
		if len(active) > 0 {
			fmt.Printf("Following %d active agent log(s) + commands + file changes. Ctrl+C to stop.\n", len(active))
		} else {
			fmt.Printf("Watching commands + file changes (no active agents yet). Ctrl+C to stop.\n")
		}
		divider()
		// -F keeps following across truncation/rotation
		c := exec.Command("tail", append([]string{"-F"}, tailFiles...)...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	}

	// ── SESSION MODE ───────────────────────────────────────────────────────────
	if logSession {
		sessionLog := filepath.Join(logDir, "sessions.log")
		fileLog := filepath.Join(logDir, "file-changes.log")
		cmdLog := filepath.Join(logDir, "commands.log")

		lastSession := lastSessionID(sessionLog)
		fmt.Printf("\n🔍 LAST SESSION — %s\n", project)
		fmt.Printf("Session: %s\n", lastSession)
		divider()

		if lastSession == "" {
			fmt.Println("No session found yet.")
			return nil
		}

		fmt.Printf("\nFILES TOUCHED:\n")
		printSessionLines(fileLog, lastSession, "file=", "  ")

		fmt.Printf("\nCOMMANDS RUN:\n")
		printSessionLines(cmdLog, lastSession, "cmd=", "  ")
		divider()
		return nil
	}

	// ── ALL MODE ───────────────────────────────────────────────────────────────
	if logAll {
		fmt.Printf("\n")
		divider()
		fmt.Printf("  🤖 AGENT HISTORY — ALL PROJECTS\n")
		divider()
		fmt.Printf("\n  📁 %s\n", project)
		miniDivider()
		showProjectRuns(logDir)

		worktreeBase := module.WorktreeBase()
		entries, _ := os.ReadDir(worktreeBase)
		for _, e := range entries {
			if !e.IsDir() || e.Name() == project {
				continue
			}
			projLog := filepath.Join(worktreeBase, "..", e.Name(), ".colony", "logs")
			if _, err := os.Stat(projLog); err == nil {
				fmt.Printf("\n  📁 %s\n", e.Name())
				miniDivider()
				showProjectRuns(projLog)
			}
		}
	} else {
		// ── PROJECT MODE ───────────────────────────────────────────────────────
		fmt.Printf("\n")
		divider()
		fmt.Printf("  🤖 AGENT HISTORY — %s\n", project)
		divider()
		showProjectRuns(logDir)
	}

	fmt.Println()
	divider()
	fmt.Printf("  colony log --live     watch live activity\n")
	fmt.Printf("  colony log --session  last session summary\n")
	fmt.Printf("  colony log --all      all projects\n")
	divider()
	fmt.Println()
	return nil
}

func showProjectRuns(logDir string) {
	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		fmt.Printf("  No logs yet.\n")
		return
	}

	entries, _ := os.ReadDir(logDir)
	found := false

	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "swarm-") {
			continue
		}
		found = true
		ts := strings.TrimPrefix(e.Name(), "swarm-")
		swarmLog := filepath.Join(logDir, e.Name(), "swarm.log")
		mode := logField(swarmLog, "Mode:")
		lang := logField(swarmLog, "Language:")

		approved := countDecision(filepath.Join(logDir, e.Name(), "reviews"), "APPROVED")
		rejected := countDecision(filepath.Join(logDir, e.Name(), "reviews"), "REJECTED")

		status := "○ BUILT"
		if rejected > 0 {
			status = "⚠ NEEDS REVIEW"
		} else if approved > 0 {
			status = "✓ APPROVED"
		}
		fmt.Printf("\n  🐝 SWARM  %s  [%s · %s]  %s\n", formatTS(ts), mode, lang, status)
		fmt.Printf("    Approved: %d  Rejected: %d\n", approved, rejected)
		fmt.Printf("    Logs: %s\n", filepath.Join(logDir, e.Name()))
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "blueprint-") {
			continue
		}
		found = true
		ts := strings.TrimPrefix(strings.TrimSuffix(e.Name(), ".log"), "blueprint-")
		bpLog := filepath.Join(logDir, e.Name())
		lang := logField(bpLog, "Language:")
		model := logField(bpLog, "Model:")

		status := "? INCOMPLETE"
		branch := "—"
		if grepFile(bpLog, "Committed on branch") {
			status = "✓ COMPLETE"
			branch = lastMatch(bpLog, "Committed on branch")
		} else if grepFile(bpLog, "BLUEPRINT BLOCKED") {
			status = "✗ BLOCKED"
		}

		fmt.Printf("\n  ⚙  BLUEPRINT  %s  [%s · %s]  %s\n", formatTS(ts), lang, model, status)
		if branch != "—" {
			fmt.Printf("    Branch: %s\n", branch)
		}
		fmt.Printf("    Log: %s\n", bpLog)
	}

	if !found {
		fmt.Printf("  No runs yet. Try: colony blueprint --spec SPEC.md --lang typescript\n")
	}
}

// activeBlueprints returns blueprint logs modified within the given window,
// sorted oldest→newest, so live mode follows every agent currently running.
func activeBlueprints(logDir string, window time.Duration) []string {
	entries, _ := os.ReadDir(logDir)
	type bp struct {
		path string
		mod  time.Time
	}
	var found []bp
	cutoff := time.Now().Add(-window)
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "blueprint-") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().Before(cutoff) {
			continue
		}
		found = append(found, bp{filepath.Join(logDir, e.Name()), info.ModTime()})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].mod.Before(found[j].mod) })
	paths := make([]string, len(found))
	for i, b := range found {
		paths[i] = b.path
	}
	return paths
}

func logField(file, field string) string {
	data, err := os.ReadFile(file)
	if err != nil {
		return "?"
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, field) {
			return strings.TrimSpace(strings.TrimPrefix(line, field))
		}
	}
	return "?"
}

func grepFile(file, pattern string) bool {
	data, err := os.ReadFile(file)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), pattern)
}

func lastMatch(file, pattern string) string {
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	last := ""
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, pattern) {
			parts := strings.Fields(line)
			if len(parts) > 0 {
				last = parts[len(parts)-1]
			}
		}
	}
	return last
}

func countDecision(reviewDir, decision string) int {
	entries, _ := os.ReadDir(reviewDir)
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".decision") {
			data, _ := os.ReadFile(filepath.Join(reviewDir, e.Name()))
			if strings.TrimSpace(string(data)) == decision {
				n++
			}
		}
	}
	return n
}

func lastSessionID(sessionLog string) string {
	data, err := os.ReadFile(sessionLog)
	if err != nil {
		return ""
	}
	last := ""
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "SESSION_START") {
			if _, after, ok := strings.Cut(line, "session="); ok {
				last = strings.Fields(after)[0]
			}
		}
	}
	return last
}

func printSessionLines(file, session, prefix, indent string) {
	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Printf("%snone\n", indent)
		return
	}
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "session="+session) {
			if _, after, ok := strings.Cut(line, prefix); ok {
				fmt.Printf("%s%s\n", indent, strings.Fields(after)[0])
				found = true
			}
		}
	}
	if !found {
		fmt.Printf("%snone\n", indent)
	}
}

func formatTS(ts string) string {
	// 20260425-143022 → 2026-04-25 14:30
	if len(ts) < 13 {
		return ts
	}
	d := ts[:8]
	t := ts[9:13]
	return fmt.Sprintf("%s-%s-%s %s:%s", d[:4], d[4:6], d[6:8], t[:2], t[2:4])
}

func divider() {
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
}

func miniDivider() {
	fmt.Printf("────────────────────────────────────────────────\n")
}
