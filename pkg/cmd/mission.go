package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/jirateep/colony/pkg/config"
	"github.com/jirateep/colony/pkg/mission/graph"
	"github.com/jirateep/colony/pkg/mission/nodes"
	"github.com/jirateep/colony/pkg/storage"
)

var missionCmd = &cobra.Command{
	Use:   "mission",
	Short: "Multi-agent mission orchestration",
}

var missionRunCmd = &cobra.Command{
	Use:          "run",
	Short:        "Execute a mission from a *.mission.yaml file",
	RunE:         runMission,
	SilenceUsage: true,
}

var missionInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new mission file (not yet implemented)",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("not yet implemented")
	},
}

var missionAuditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Query mission run history",
	RunE:  runAudit,
}

var (
	missionFile     string
	missionInput    string
	missionOutput   string
	auditSession    string
	auditDecision   string
	auditStatus     string
	auditPurge      bool
	auditShowOutput bool
)

func init() {
	missionRunCmd.Flags().StringVar(&missionFile, "mission", "", "Path to *.mission.yaml file (required)")
	missionRunCmd.Flags().StringVar(&missionInput, "input", "", "Override the mission's input field (optional)")
	missionRunCmd.Flags().StringVar(&missionOutput, "output", "", "Write final output to this file path (optional)")
	_ = missionRunCmd.MarkFlagRequired("mission")

	missionAuditCmd.Flags().StringVar(&auditSession, "session", "", "Filter by session ID")
	missionAuditCmd.Flags().StringVar(&auditDecision, "decision", "", "Filter steps by decision (e.g. REJECTED)")
	missionAuditCmd.Flags().StringVar(&auditStatus, "status", "", "Filter sessions by status (running, failed, completed)")
	missionAuditCmd.Flags().BoolVar(&auditPurge, "purge", false, "Delete matching sessions and their steps")
	missionAuditCmd.Flags().BoolVar(&auditShowOutput, "show-output", false, "Print each agent's output text below its step row")

	missionCmd.AddCommand(missionRunCmd)
	missionCmd.AddCommand(missionInitCmd)
	missionCmd.AddCommand(missionAuditCmd)
}

func runMission(cmd *cobra.Command, args []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := config.Load(wd)
	if err != nil {
		return err
	}

	m, err := graph.LoadMission(missionFile)
	if err != nil {
		return err
	}

	if missionInput != "" {
		m.Input = missionInput
	}

	reg := graph.NewRegistry()
	nodes.Register(reg)

	g, err := graph.BuildGraph(m, reg, func(role string) config.LLMConfig {
		return cfg.Role(role)
	})
	if err != nil {
		return err
	}

	dbPath := storage.DefaultDBPath()
	store, err := storage.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open missions.db: %w", err)
	}
	defer func() { _ = store.Close() }()

	sessID := fmt.Sprintf("%s-%s", m.Name, time.Now().Format("20060102-150405"))
	if err := store.InsertSession(storage.Session{
		ID:          sessID,
		MissionName: m.Name,
		StartedAt:   time.Now(),
		Status:      "running",
	}); err != nil {
		return fmt.Errorf("insert session: %w", err)
	}

	runner := graph.NewRunner()
	out, runErr := runner.Run(cmd.Context(), m, g, sessID, store)

	if runErr != nil {
		_ = store.UpdateSession(sessID, "failed", time.Now())
		if out != nil && missionOutput != "" && out.Raw != "" {
			if writeErr := os.WriteFile(missionOutput, []byte(out.Raw), 0644); writeErr == nil {
				fmt.Fprintf(os.Stderr, "raw output written to %s (run failed — JSON envelope missing)\n", missionOutput)
			}
		}
		return runErr
	}

	_ = store.UpdateSession(sessID, "completed", time.Now())

	if out != nil {
		fmt.Println(out.Envelope.OutputText())
		if missionOutput != "" {
			if err := os.WriteFile(missionOutput, []byte(out.Envelope.OutputText()), 0644); err != nil {
				return fmt.Errorf("write output file: %w", err)
			}
			fmt.Fprintf(os.Stderr, "output written to %s\n", missionOutput)
		}
	}
	return nil
}

func runAudit(cmd *cobra.Command, args []string) error {
	dbPath := storage.DefaultDBPath()
	store, err := storage.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open missions.db: %w", err)
	}
	defer func() { _ = store.Close() }()

	if auditPurge {
		n, err := store.DeleteSessions(storage.SessionFilter{
			SessionID: auditSession,
			Status:    auditStatus,
		})
		if err != nil {
			return err
		}
		fmt.Printf("deleted %d session(s)\n", n)
		return nil
	}

	if auditSession == "" && auditDecision == "" {
		// List sessions, optionally filtered by status.
		sessions, err := store.QuerySessions(storage.SessionFilter{Status: auditStatus})
		if err != nil {
			return err
		}
		if len(sessions) == 0 {
			fmt.Println("no sessions found")
			return nil
		}
		fmt.Printf("%-40s %-20s %-10s %s\n", "SESSION ID", "MISSION", "STATUS", "STARTED AT")
		for _, s := range sessions {
			fmt.Printf("%-40s %-20s %-10s %s\n", s.ID, s.MissionName, s.Status, s.StartedAt.Format(time.RFC3339))
		}
		return nil
	}

	// List steps for a session.
	steps, err := store.QuerySteps(storage.StepFilter{
		SessionID: auditSession,
		Decision:  auditDecision,
	})
	if err != nil {
		return err
	}
	if len(steps) == 0 {
		fmt.Println("no steps found")
		return nil
	}
	fmt.Printf("%-4s %-4s %-20s %-16s %-10s %s\n", "STEP", "SUB", "AGENT_ID", "ROLE", "DECISION", "DURATION_MS")
	for _, s := range steps {
		fmt.Printf("%-4d %-4s %-20s %-16s %-10s %d\n",
			s.StepNum, s.SubStep, s.AgentID, s.Role, s.Decision, s.DurationMS)
		if auditShowOutput && s.OutputJSON != "" {
			var env graph.Envelope
			if err := json.Unmarshal([]byte(s.OutputJSON), &env); err == nil && env.OutputText() != "" {
				fmt.Printf("    output: %s\n", env.OutputText())
			} else {
				fmt.Printf("    output: %s\n", s.OutputJSON)
			}
		}
	}
	return nil
}
