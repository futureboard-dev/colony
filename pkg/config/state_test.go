package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadStateDefaultsWhenMissing(t *testing.T) {
	tmp := t.TempDir()
	st, err := LoadState(tmp)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(st.Flows) != 0 {
		t.Errorf("expected no flows, got %+v", st.Flows)
	}
	if _, ok := st.ChoiceForFlow("fix"); ok {
		t.Error("expected no saved choice for an unknown flow")
	}
}

func TestSaveAndLoadStateRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	st := &State{}
	st.SetChoiceForFlow("fix", FlowChoice{Tool: "claude", Model: "default"})

	if err := SaveState(tmp, st); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	got, err := LoadState(tmp)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	choice, ok := got.ChoiceForFlow("fix")
	if !ok {
		t.Fatal("expected saved choice for flow 'fix'")
	}
	if choice.Tool != "claude" || choice.Model != "default" {
		t.Errorf("choice = %+v, want {claude default}", choice)
	}
}

func TestStatePerFlowValuesAreIndependent(t *testing.T) {
	tmp := t.TempDir()
	st := &State{}
	st.SetChoiceForFlow("fix", FlowChoice{Tool: "claude", Model: "default"})
	st.SetChoiceForFlow("review", FlowChoice{Tool: "codex", Model: "o4-mini"})

	if err := SaveState(tmp, st); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	got, err := LoadState(tmp)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	fix, ok := got.ChoiceForFlow("fix")
	if !ok || fix.Tool != "claude" {
		t.Errorf("fix choice = %+v, ok=%v", fix, ok)
	}
	review, ok := got.ChoiceForFlow("review")
	if !ok || review.Tool != "codex" || review.Model != "o4-mini" {
		t.Errorf("review choice = %+v, ok=%v", review, ok)
	}
	if _, ok := got.ChoiceForFlow("craft"); ok {
		t.Error("expected no saved choice for 'craft'")
	}
}

func TestSetChoiceForFlowOverwritesExisting(t *testing.T) {
	st := &State{}
	st.SetChoiceForFlow("fix", FlowChoice{Tool: "claude", Model: "default"})
	st.SetChoiceForFlow("fix", FlowChoice{Tool: "crush", Model: "deepseek/deepseek-v4.1-flash"})

	choice, ok := st.ChoiceForFlow("fix")
	if !ok {
		t.Fatal("expected a saved choice")
	}
	if choice.Tool != "crush" || choice.Model != "deepseek/deepseek-v4.1-flash" {
		t.Errorf("choice = %+v, want overwritten crush choice", choice)
	}
}

func TestLoadStateMalformedJSON(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".colony"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(StatePath(tmp), []byte("{bad json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(tmp); err == nil {
		t.Fatal("expected error for malformed state.json")
	}
}

func TestStatePathUnderColonyDir(t *testing.T) {
	got := StatePath("/proj")
	want := filepath.Join("/proj", ".colony", "state.json")
	if got != want {
		t.Errorf("StatePath = %q, want %q", got, want)
	}
}
