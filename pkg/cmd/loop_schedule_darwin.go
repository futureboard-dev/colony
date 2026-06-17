package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
	"time"
)

type loopSchedulerDarwin struct{}

func init() {
	newLoopScheduler = func() loopScheduler { return &loopSchedulerDarwin{} }
}

var launchdPlistTmpl = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.colony.loop.{{.Hash}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.Binary}}</string>
        <string>loop</string>
    </array>
    <key>WorkingDirectory</key>
    <string>{{.ProjectRoot}}</string>
    <key>StartInterval</key>
    <integer>{{.Seconds}}</integer>
    <key>RunAtLoad</key>
    <true/>
    <key>StandardOutPath</key>
    <string>{{.LogDir}}/loop.stdout.log</string>
    <key>StandardErrorPath</key>
    <string>{{.LogDir}}/loop.stderr.log</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>{{.Path}}</string>
    </dict>
</dict>
</plist>
`))

type plistData struct {
	Binary      string
	ProjectRoot string
	Hash        string
	Seconds     int
	LogDir      string
	Path        string
}

func (s *loopSchedulerDarwin) Install(binaryPath, projectRoot string, d time.Duration) error {
	hash := projectHash(projectRoot)
	logDir := filepath.Join(projectRoot, ".colony", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}

	plistPath := s.plistPath(hash)

	data := plistData{
		Binary:      binaryPath,
		ProjectRoot: projectRoot,
		Hash:        hash,
		Seconds:     int(d.Seconds()),
		LogDir:      logDir,
		Path:        os.Getenv("PATH"),
	}

	var buf bytes.Buffer
	if err := launchdPlistTmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("render plist: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(plistPath), 0755); err != nil {
		return fmt.Errorf("create launchagents dir: %w", err)
	}
	if err := os.WriteFile(plistPath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}

	// Load into launchd (idempotent: unload first if already loaded).
	_ = exec.Command("launchctl", "unload", plistPath).Run()
	if out, err := exec.Command("launchctl", "load", "-w", plistPath).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl load: %s: %w", string(out), err)
	}

	return nil
}

func (s *loopSchedulerDarwin) Remove() error {
	// We need to find the plist by iterating since we don't know the hash.
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}
	launchAgents := filepath.Join(home, "Library", "LaunchAgents")
	entries, err := os.ReadDir(launchAgents)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() && len(entry.Name()) > 18 && entry.Name()[:18] == "com.colony.loop." {
			plistPath := filepath.Join(launchAgents, entry.Name())
			_ = exec.Command("launchctl", "unload", plistPath).Run()
			os.Remove(plistPath)
		}
	}
	return nil
}

func (s *loopSchedulerDarwin) Status() (bool, time.Duration, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, 0, fmt.Errorf("get home dir: %w", err)
	}
	launchAgents := filepath.Join(home, "Library", "LaunchAgents")
	entries, err := os.ReadDir(launchAgents)
	if err != nil {
		if os.IsNotExist(err) {
			return false, 0, nil
		}
		return false, 0, err
	}

	for _, entry := range entries {
		if !entry.IsDir() && len(entry.Name()) > 18 && entry.Name()[:18] == "com.colony.loop." {
			plistPath := filepath.Join(launchAgents, entry.Name())
			data, err := os.ReadFile(plistPath)
			if err != nil {
				return false, 0, nil
			}
			// Extract StartInterval from the plist (simple scan).
			interval := extractLaunchdInterval(data)
			return true, interval, nil
		}
	}
	return false, 0, nil
}

func (s *loopSchedulerDarwin) Label() string {
	return "launchd"
}

func (s *loopSchedulerDarwin) plistPath(hash string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", fmt.Sprintf("com.colony.loop.%s.plist", hash))
}

// extractLaunchdInterval does a naive scan of the plist XML for StartInterval.
func extractLaunchdInterval(data []byte) time.Duration {
	// Look for <integer>N</integer> after StartInterval.
	idx := bytes.Index(data, []byte("<integer>"))
	if idx < 0 {
		return 0
	}
	end := bytes.Index(data[idx:], []byte("</integer>"))
	if end < 0 {
		return 0
	}
	var secs int
	_, _ = fmt.Sscanf(string(data[idx+len("<integer>"):idx+end]), "%d", &secs)
	return time.Duration(secs) * time.Second
}
