package tui

import (
	"fmt"
	"strings"
	"time"
)

// timeAgo renders a compact relative timestamp for activity feeds.
func timeAgo(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// humanDuration renders a session duration as Xm YYs.
func humanDuration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// sessionDuration returns a session's elapsed time, using now for live runs.
func sessionDuration(startedAt time.Time, finishedAt *time.Time) time.Duration {
	if finishedAt != nil {
		return finishedAt.Sub(startedAt)
	}
	if startedAt.IsZero() {
		return 0
	}
	return time.Since(startedAt)
}

// wrapText soft-wraps a paragraph to a width, preserving existing newlines.
func wrapText(s string, w int) []string {
	if w <= 0 {
		return nil
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		if para == "" {
			out = append(out, "")
			continue
		}
		line := ""
		for _, word := range strings.Fields(para) {
			switch {
			case line == "":
				line = word
			case len(line)+1+len(word) <= w:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		out = append(out, line)
	}
	return out
}

// bar renders a proportional block bar of the given cell width.
func bar(value, max, width int) string {
	if max <= 0 || width <= 0 {
		return ""
	}
	filled := value * width / max
	if value > 0 && filled == 0 {
		filled = 1
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}
