//go:build darwin

package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLaunchdPlistRendering(t *testing.T) {
	data := plistData{
		Binary:      "/usr/local/bin/colony",
		ProjectRoot: "/Users/test/project",
		Hash:        "abc123",
		Seconds:     600,
		LogDir:      "/Users/test/project/.colony/logs",
		Path:        "/usr/bin:/bin",
	}

	var buf bytes.Buffer
	if err := launchdPlistTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute plist template: %v", err)
	}

	output := buf.String()
	checks := []string{
		"com.colony.loop.abc123",
		"/usr/local/bin/colony",
		"<integer>600</integer>",
		"/Users/test/project",
		"/Users/test/project/.colony/logs/loop.stdout.log",
		"/Users/test/project/.colony/logs/loop.stderr.log",
		"<key>PATH</key>",
		"/usr/bin:/bin",
	}
	for _, c := range checks {
		if !bytes.Contains([]byte(output), []byte(c)) {
			t.Errorf("expected plist to contain %q", c)
		}
	}
}

func TestExtractLaunchdInterval(t *testing.T) {
	plist := []byte(`<key>StartInterval</key><integer>300</integer>`)
	d := extractLaunchdInterval(plist)
	if d != 300*time.Second {
		t.Errorf("expected 300s, got %s", d)
	}

	d = extractLaunchdInterval([]byte(`<key>StartInterval</key><integer>0</integer>`))
	if d != 0 {
		t.Errorf("expected 0s, got %s", d)
	}

	d = extractLaunchdInterval([]byte(`no integer here`))
	if d != 0 {
		t.Errorf("expected 0s for missing, got %s", d)
	}
}

func TestProjectHash(t *testing.T) {
	h1 := projectHash("/Users/test/project")
	h2 := projectHash("/Users/test/project")
	h3 := projectHash("/Users/test/other")

	if h1 != h2 {
		t.Error("same path should produce same hash")
	}
	if h1 == h3 {
		t.Error("different paths should produce different hashes")
	}
}

func TestScheduleRoundTrip(t *testing.T) {
	s := &stubScheduler{dir: t.TempDir()}

	installed, _, err := s.Status()
	if err != nil {
		t.Fatal(err)
	}
	if installed {
		t.Error("expected not installed initially")
	}

	bin := "/usr/bin/colony"
	proj := t.TempDir()
	if err := s.Install(bin, proj, 5*time.Minute); err != nil {
		t.Fatal(err)
	}

	installed, interval, err := s.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Error("expected installed after install")
	}
	if interval != 5*time.Minute {
		t.Errorf("expected 5m interval, got %s", interval)
	}

	if err := s.Remove(); err != nil {
		t.Fatal(err)
	}

	installed, _, err = s.Status()
	if err != nil {
		t.Fatal(err)
	}
	if installed {
		t.Error("expected not installed after remove")
	}
}

// stubScheduler implements loopScheduler against a temp directory for testing.
type stubScheduler struct {
	dir string
}

func (s *stubScheduler) Install(binaryPath, projectRoot string, d time.Duration) error {
	hash := projectHash(projectRoot)
	plistPath := filepath.Join(s.dir, fmt.Sprintf("com.colony.loop.%s.plist", hash))
	return os.WriteFile(plistPath, []byte(fmt.Sprintf(`<integer>%d</integer>`, int(d.Seconds()))), 0644)
}

func (s *stubScheduler) Remove() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "com.colony.loop.") {
			os.Remove(filepath.Join(s.dir, e.Name()))
		}
	}
	return nil
}

func (s *stubScheduler) Status() (bool, time.Duration, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, 0, nil
		}
		return false, 0, err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "com.colony.loop.") {
			data, _ := os.ReadFile(filepath.Join(s.dir, e.Name()))
			interval := extractLaunchdInterval(data)
			return true, interval, nil
		}
	}
	return false, 0, nil
}

func (s *stubScheduler) Label() string { return "stub" }
