package module

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"strings"
)

// CopyFile copies src to dst, creating dst if needed.
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// Slugify converts a string to a URL-safe lowercase slug, max 40 chars.
func Slugify(s string) string {
	words := strings.Fields(strings.ToLower(s))
	if len(words) > 8 {
		words = words[:8]
	}
	var b strings.Builder
	for i, w := range words {
		if i > 0 {
			b.WriteByte('-')
		}
		for _, r := range w {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			} else {
				b.WriteByte('-')
			}
		}
	}
	result := b.String()
	// collapse double dashes
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	result = strings.Trim(result, "-")
	if len(result) > 40 {
		result = result[:40]
	}
	return result
}

// ExtractTaskDesc pulls a task description from a spec file's first heading or filename.
func ExtractTaskDesc(specContent, filename string) string {
	scanner := bufio.NewScanner(strings.NewReader(specContent))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if after, ok := strings.CutPrefix(line, "# "); ok {
			after, _ = strings.CutPrefix(after, "Plan: ")
			return after
		}
		if after, ok := strings.CutPrefix(line, "## 1."); ok {
			return strings.TrimSpace(after)
		}
	}
	base := strings.TrimSuffix(filename, ".md")
	return strings.ReplaceAll(base, "-", " ")
}

// RunShell runs a shell command string in workdir, writing output to out.
// Returns without error even on failure (caller decides if it's fatal).
func RunShell(command, workdir string, out io.Writer) error {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return nil
	}
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = workdir
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}

// GitDiff returns the diff of branch against base.
func GitDiff(projectRoot, base, branch string) (string, error) {
	cmd := exec.Command("git", "diff", base+".."+branch)
	cmd.Dir = projectRoot
	out, err := cmd.Output()
	return string(out), err
}
