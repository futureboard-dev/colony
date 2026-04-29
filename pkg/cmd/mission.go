package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/jirateep/colony/pkg/config"
	"github.com/jirateep/colony/pkg/mission"
	"github.com/jirateep/colony/pkg/storage"
)

var missionCmd = &cobra.Command{
	Use:   "mission",
	Short: "Multi-agent mission orchestration",
}

var missionRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Execute a mission from a *.mission.yaml file",
	RunE:  runMission,
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
	missionFile   string
	auditSession  string
	auditDecision string
)

func init() {
	missionRunCmd.Flags().StringVar(&missionFile, "mission", "", "Path to *.mission.yaml file (required)")
	_ = missionRunCmd.MarkFlagRequired("mission")

	missionAuditCmd.Flags().StringVar(&auditSession, "session", "", "Filter by session ID")
	missionAuditCmd.Flags().StringVar(&auditDecision, "decision", "", "Filter steps by decision (e.g. REJECTED)")

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

	m, err := mission.LoadMission(missionFile)
	if err != nil {
		return err
	}

	g, err := mission.BuildGraph(m, mission.DefaultRegistry, func(role string) config.LLMConfig {
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
	defer store.Close()

	sessID := fmt.Sprintf("%s-%s", m.Name, time.Now().Format("20060102-150405"))
	if err := store.InsertSession(storage.Session{
		ID:          sessID,
		MissionName: m.Name,
		StartedAt:   time.Now(),
		Status:      "running",
	}); err != nil {
		return fmt.Errorf("insert session: %w", err)
	}

	runner := mission.NewRunner()
	out, runErr := runner.Run(cmd.Context(), m, g, sessID, store)

	if runErr != nil {
		_ = store.UpdateSession(sessID, "failed", time.Now())
		return runErr
	}

	_ = store.UpdateSession(sessID, "completed", time.Now())

	if out != nil {
		fmt.Println(out.Envelope.Output)
	}
	return nil
}

func runAudit(cmd *cobra.Command, args []string) error {
	dbPath := storage.DefaultDBPath()
	store, err := storage.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open missions.db: %w", err)
	}
	defer store.Close()

	if auditSession == "" && auditDecision == "" {
		// List all sessions.
		sessions, err := store.QuerySessions(storage.SessionFilter{})
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
	}
	return nil
}
