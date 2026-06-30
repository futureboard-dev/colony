package cmd

import (
	"strings"
	"testing"
)

func TestCapGateOutput(t *testing.T) {
	t.Run("under cap is unchanged", func(t *testing.T) {
		in := "a few lint errors"
		if got := capGateOutput(in); got != in {
			t.Errorf("capGateOutput modified short input: %q", got)
		}
	})
	t.Run("over cap is truncated", func(t *testing.T) {
		in := strings.Repeat("x", maxFixerOutput+5000)
		got := capGateOutput(in)
		if len(got) >= len(in) {
			t.Errorf("capGateOutput did not shrink oversized input: %d >= %d", len(got), len(in))
		}
		if !strings.Contains(got, "truncated") {
			t.Error("truncated output missing marker")
		}
	})
}
