// Package handoff keeps transcripts on disk and bounds repair context.
package handoff

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const FeedbackLimit = 12 * 1024
const ResponseLimit = 32 * 1024
const PromptLimit = 192 * 1024
const ReviewLimit = 160 * 1024

// Bound preserves the beginning and end, with an explicit omission marker.
// It must never be used to truncate required review evidence.
func Bound(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	marker := "\n[... omitted; see full log ...]\n"
	n := (limit - len(marker)) / 2
	if n < 0 {
		return ""
	}
	head, tail := s[:n], s[len(s)-n:]
	for !utf8.ValidString(head) && len(head) > 0 {
		head = head[:len(head)-1]
	}
	for !utf8.ValidString(tail) && len(tail) > 0 {
		tail = tail[1:]
	}
	return head + marker + tail
}

func Create(workdir, kind string) (*os.File, error) {
	dir, err := filepath.Abs(filepath.Join(workdir, ".colony", "logs"))
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return os.CreateTemp(dir, kind+"-*.log")
}

func Save(workdir, kind, text string) (string, error) {
	f, err := Create(workdir, kind)
	if err != nil {
		return "", err
	}
	_, err = f.WriteString(text)
	closeErr := f.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	return f.Name(), nil
}

func Summary(text, path string, limit int) string {
	suffix := "\nFull log: " + path
	if len(text)+len(suffix) > limit {
		suffix = fmt.Sprintf("\nContent SHA256: %x", sha256.Sum256([]byte(text))) + suffix
	}
	return Bound(strings.TrimSpace(text), limit-len(suffix)) + suffix
}

func CheckPrompt(prompt string) error {
	if len(prompt) > PromptLimit {
		return fmt.Errorf("prompt exceeds %d-byte budget (%d bytes); split the task without dropping required context", PromptLimit, len(prompt))
	}
	return nil
}
