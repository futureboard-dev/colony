package handoff

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBoundUTF8AndLogReference(t *testing.T) {
	s := strings.Repeat("ก", 10000)
	got := Summary(s, "/logs/full.log", FeedbackLimit)
	if len(got) > FeedbackLimit || !utf8.ValidString(got) || !strings.Contains(got, "omitted") || !strings.HasSuffix(got, "/logs/full.log") {
		t.Fatal(got)
	}
	if CheckPrompt(strings.Repeat("x", PromptLimit+1)) == nil {
		t.Fatal("unbounded prompt")
	}
}
