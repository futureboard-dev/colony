package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type loopSchedulerLinux struct{}

func init() {
	newLoopScheduler = func() loopScheduler { return &loopSchedulerLinux{} }
}

func (s *loopSchedulerLinux) Install(binaryPath, projectRoot string, d time.Duration) error {
	hash := projectHash(projectRoot)
	logDir := filepath.Join(projectRoot, ".colony", "logs")
	os.MkdirAll(logDir, 0755)

	intervalMin := int(d.Minutes())
	// Build the cron line with flock for overlap prevention and a comment identifier.
	cronLine := fmt.Sprintf(
		"# colony-loop-%s\n*/%d * * * * flock -n %s/.colony/loop.lock %s loop --cwd %s >> %s/loop.log 2>&1\n",
		hash, intervalMin, projectRoot, binaryPath, projectRoot, logDir,
	)

	// Read existing crontab.
	existing, _ := exec.Command("crontab", "-l").Output()

	// Remove any existing colony-loop entries for this project.
	var cleaned []string
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.Contains(line, "colony-loop-"+hash) {
			continue
		}
		cleaned = append(cleaned, line)
	}

	// Append new entry.
	cleaned = append(cleaned, cronLine)

	newCron := strings.Join(cleaned, "\n") + "\n"

	// Write temp file and load via crontab.
	tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("colony-cron-%s", hash))
	if err := os.WriteFile(tmpPath, []byte(newCron), 0644); err != nil {
		return fmt.Errorf("write temp crontab: %w", err)
	}
	defer os.Remove(tmpPath)

	if out, err := exec.Command("crontab", tmpPath).CombinedOutput(); err != nil {
		return fmt.Errorf("crontab install: %s: %w", string(out), err)
	}

	return nil
}

func (s *loopSchedulerLinux) Remove() error {
	// Read existing crontab.
	existing, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		// No crontab = nothing to remove.
		return nil
	}

	// Remove all colony-loop lines.
	var cleaned []string
	removed := false
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.Contains(line, "colony-loop-") {
			removed = true
			continue
		}
		cleaned = append(cleaned, line)
	}

	if !removed {
		return nil
	}

	newCron := strings.Join(cleaned, "\n") + "\n"

	tmpPath := filepath.Join(os.TempDir(), "colony-cron-remove")
	if err := os.WriteFile(tmpPath, []byte(newCron), 0644); err != nil {
		return fmt.Errorf("write temp crontab: %w", err)
	}
	defer os.Remove(tmpPath)

	return exec.Command("crontab", tmpPath).Run()
}

func (s *loopSchedulerLinux) Status() (bool, time.Duration, error) {
	existing, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		return false, 0, nil
	}

	for _, line := range strings.Split(string(existing), "\n") {
		if strings.Contains(line, "colony-loop-") {
			// Extract interval from the step value, e.g. "*/5 * * * *".
			fields := strings.Fields(line)
			if len(fields) > 0 {
				step := strings.TrimPrefix(fields[0], "*/")
				if mins, err := strconv.Atoi(step); err == nil && mins > 0 {
					return true, time.Duration(mins) * time.Minute, nil
				}
			}
			return true, 0, nil
		}
	}
	return false, 0, nil
}

func (s *loopSchedulerLinux) Label() string {
	return "crontab"
}

// Ensure no unused import warnings.
var _ = fmt.Sprintf
