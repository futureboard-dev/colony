// Package handoff keeps transcripts on disk and bounds repair context.
package handoff

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// FeedbackLimit caps any repair feedback handed to the next agent; the full
// text always stays in a log referenced from the summary.
const FeedbackLimit = 4 * 1024

// ResponseLimit caps an agent's final response.
const ResponseLimit = 32 * 1024

// PromptLimit caps builder/fixer prompts, which run agentically.
const PromptLimit = 192 * 1024

// ReviewLimit caps one headless review prompt. Larger reviews are batched
// with explicit coverage rather than truncated.
const ReviewLimit = 400 * 1024

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

// CheckPrompt fails before any model call when prompt exceeds limit.
func CheckPrompt(prompt string, limit int) error {
	if len(prompt) > limit {
		return fmt.Errorf("prompt exceeds %d-byte budget (%d bytes); split the task without dropping required context", limit, len(prompt))
	}
	return nil
}

// noise matches gate-output lines that never help a fixer: ORM query echoes,
// console banners, passing suites and stack frames inside dependencies.
var noise = regexp.MustCompile(`^\s*(Executing \(default\):|console\.(log|warn|info|debug)$|PASS |at .*node_modules|\(Use .node --trace)`)

// Distill reduces raw gate output to its failures: noise lines are dropped,
// repeated paragraphs (jest prints each failure twice) appear once, and a
// jest "Summary of all failing tests" replaces the per-suite chatter before it.
// Section headers ("--- test ---") are kept so the fixer knows which gate failed.
func Distill(output string) string {
	var out []string
	seen := map[string]bool{}
	for _, section := range splitSections(output) {
		if i := strings.Index(section.body, "Summary of all failing tests"); i >= 0 {
			section.body = section.body[i:]
		}
		if section.header != "" {
			out = append(out, section.header)
		}
		for _, para := range strings.Split(section.body, "\n\n") {
			var kept []string
			for _, line := range strings.Split(para, "\n") {
				if strings.TrimSpace(line) != "" && !noise.MatchString(line) {
					kept = append(kept, line)
				}
			}
			text := strings.Join(kept, "\n")
			if text == "" || seen[text] {
				continue
			}
			seen[text] = true
			out = append(out, text)
		}
	}
	return strings.Join(out, "\n\n")
}

type section struct{ header, body string }

var sectionHeader = regexp.MustCompile(`(?m)^--- \S+ ---$`)

func splitSections(output string) []section {
	locs := sectionHeader.FindAllStringIndex(output, -1)
	if len(locs) == 0 {
		return []section{{body: output}}
	}
	sections := []section{{body: output[:locs[0][0]]}}
	for i, loc := range locs {
		end := len(output)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		sections = append(sections, section{header: output[loc[0]:loc[1]], body: output[loc[1]:end]})
	}
	return sections
}
