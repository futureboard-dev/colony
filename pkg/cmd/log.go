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
	"github.com/jirateep/colony/pkg/storage"
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
		showProjectRuns(filepath.Join(root, ".colony", "missions.db"))

		worktreeBase := module.WorktreeBase()
		entries, _ := os.ReadDir(worktreeBase)
		for _, e := range entries {
			if !e.IsDir() || e.Name() == project {
				continue
			}
			projDB := filepath.Join(worktreeBase, "..", e.Name(), ".colony", "missions.db")
			if _, err := os.Stat(projDB); err == nil {
				fmt.Printf("\n  📁 %s\n", e.Name())
				miniDivider()
				showProjectRuns(projDB)
			}
		}
	} else {
		// ── PROJECT MODE ───────────────────────────────────────────────────────
		fmt.Printf("\n")
		divider()
		fmt.Printf("  🤖 AGENT HISTORY — %s\n", project)
		divider()
		showProjectRuns(filepath.Join(root, ".colony", "missions.db"))
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

func showProjectRuns(dbPath string) {
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Printf("  No runs yet. Try: colony blueprint --spec SPEC.md --lang typescript\n")
		return
	}
	store, err := storage.Open(dbPath)
	if err != nil {
		fmt.Printf("  Could not open run history: %v\n", err)
		return
	}
	defer store.Close()

	runs, err := store.QueryRuns(storage.RunFilter{})
	if err != nil {
		fmt.Printf("  Could not read run history: %v\n", err)
		return
	}
	if len(runs) == 0 {
		fmt.Printf("  No runs yet. Try: colony blueprint --spec SPEC.md --lang typescript\n")
		return
	}

	for _, r := range runs {
		ts := r.StartedAt.Local().Format("2006-01-02 15:04")
		switch r.Kind {
		case "swarm":
			status := "○ BUILT"
			if r.Rejected > 0 {
				status = "⚠ NEEDS REVIEW"
			} else if r.Approved > 0 {
				status = "✓ APPROVED"
			}
			fmt.Printf("\n  🐝 SWARM  %s  [%s · %s]  %s\n", ts, r.Mode, r.Language, status)
			fmt.Printf("    Approved: %d  Rejected: %d\n", r.Approved, r.Rejected)
			fmt.Printf("    Logs: %s\n", r.LogPath)
		case "blueprint":
			status := "? INCOMPLETE"
			switch r.Status {
			case "complete":
				status = "✓ COMPLETE"
			case "blocked":
				status = "✗ BLOCKED"
			}
			fmt.Printf("\n  ⚙  BLUEPRINT  %s  [%s · %s]  %s\n", ts, r.Language, r.Model, status)
			if r.Branch != "" {
				fmt.Printf("    Branch: %s\n", r.Branch)
			}
			fmt.Printf("    Log: %s\n", r.LogPath)
		}
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

func divider() {
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
}

func miniDivider() {
	fmt.Printf("────────────────────────────────────────────────\n")
}
