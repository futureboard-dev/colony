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
	if CheckPrompt(strings.Repeat("x", PromptLimit+1), PromptLimit) == nil {
		t.Fatal("unbounded prompt")
	}
}

func TestDistill(t *testing.T) {
	failure := "  ● suite › case\n\n    TypeError: Cannot read properties of undefined (reading 'create')\n\n      at Object.<anonymous> (test/features/feature.integration.spec.ts:198:41)"
	var noisy strings.Builder
	noisy.WriteString("--- lint ---\nok\n--- test ---\nFAIL test/features/feature.integration.spec.ts\n")
	for range 500 {
		noisy.WriteString("    console.log\n      Executing (default): SELECT * FROM \"Users\"\n\n      at Sequelize.log (node_modules/sequelize/src/sequelize.js:1281:15)\n\n")
	}
	noisy.WriteString(failure + "\n\nPASS test/user/user.service.spec.ts\n\nSummary of all failing tests\nFAIL test/features/feature.integration.spec.ts\n" + failure + "\n\n" + failure + "\n\nTests:       6 failed, 369 passed, 375 total\n")

	got := Distill(noisy.String())
	for _, want := range []string{"--- test ---", "--- lint ---", "reading 'create'", "feature.integration.spec.ts:198:41", "Tests:       6 failed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"Executing (default)", "node_modules", "PASS ", "console.log"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("kept noise %q in:\n%s", unwanted, got)
		}
	}
	if strings.Count(got, "reading 'create'") != 1 {
		t.Fatalf("repeated failure not deduplicated:\n%s", got)
	}
	if len(got) > 1024 {
		t.Fatalf("distilled output still %d bytes", len(got))
	}
	t.Run("no sections", func(t *testing.T) {
		if got := Distill("error TS2345: bad\n\nerror TS2345: bad"); got != "error TS2345: bad" {
			t.Fatal(got)
		}
	})
}
