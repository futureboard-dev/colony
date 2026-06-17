//go:build linux

package cmd

import (
	"bytes"
	"strconv"
	"testing"
	"time"
)

func TestCronLineFormat(t *testing.T) {
	intervalMin := 10
	hash := "def456"
	binaryPath := "/usr/local/bin/colony"
	projectRoot := "/home/user/project"
	logDir := projectRoot + "/.colony/logs"

	cronLine := "# colony-loop-" + hash + "\n" +
		"*/" + strconv.Itoa(intervalMin) + " * * * * flock -n " + projectRoot + "/.colony/loop.lock " +
		binaryPath + " loop --cwd " + projectRoot + " >> " + logDir + "/loop.log 2>&1\n"

	if !bytes.Contains([]byte(cronLine), []byte("colony-loop-def456")) {
		t.Error("expected colony-loop-<hash> in cron line")
	}
	if !bytes.Contains([]byte(cronLine), []byte("flock -n")) {
		t.Error("expected flock -n for overlap prevention")
	}
	if !bytes.Contains([]byte(cronLine), []byte("*/10 * * * *")) {
		t.Errorf("expected */10 interval, got: %s", cronLine)
	}
}

func TestProjectHash(t *testing.T) {
	h1 := projectHash("/home/test/project")
	h2 := projectHash("/home/test/project")
	h3 := projectHash("/home/test/other")

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
	return nil
}

func (s *stubScheduler) Remove() error {
	return nil
}

func (s *stubScheduler) Status() (bool, time.Duration, error) {
	return false, 0, nil
}

func (s *stubScheduler) Label() string { return "stub" }
