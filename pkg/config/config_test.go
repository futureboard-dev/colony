package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoleFallbackToDefault(t *testing.T) {
	cfg := Config{
		LLM: LLMConfig{Provider: "anthropic", Model: "claude-opus-4-7"},
	}
	got := cfg.Role("engineer")
	if got.Provider != "anthropic" || got.Model != "claude-opus-4-7" {
		t.Errorf("expected default LLM, got %+v", got)
	}
}

func TestRoleReturnsOverride(t *testing.T) {
	cfg := Config{
		LLM: LLMConfig{Provider: "anthropic", Model: "claude-opus-4-7"},
		Roles: map[string]LLMConfig{
			"engineer": {Provider: "openai", Model: "gpt-4o"},
		},
	}
	got := cfg.Role("engineer")
	if got.Provider != "openai" || got.Model != "gpt-4o" {
		t.Errorf("expected openai engineer, got %+v", got)
	}
}

func TestRoleUnknownFallsBack(t *testing.T) {
	cfg := Config{
		LLM: LLMConfig{Provider: "openrouter", Model: "google/gemini-2.5-pro"},
		Roles: map[string]LLMConfig{
			"engineer": {Provider: "openai", Model: "gpt-4o"},
		},
	}
	got := cfg.Role("reviewer") // not in Roles
	if got.Provider != "openrouter" {
		t.Errorf("unknown role should fall back to default, got %+v", got)
	}
}

func TestLoadMissingConfig(t *testing.T) {
	tmp := t.TempDir()
	_, err := Load(tmp)
	if err == nil {
		t.Fatal("expected error for missing config")
	}
}

func TestLoadMalformedConfig(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, ".colony"), 0755)
	os.WriteFile(filepath.Join(tmp, ".colony", "config.json"), []byte("{bad json"), 0644)
	_, err := Load(tmp)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestLoadSetsRootWhenEmpty(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, ".colony"), 0755)
	os.WriteFile(filepath.Join(tmp, ".colony", "config.json"), []byte(`{
		"llm": {"provider": "anthropic", "model": "claude-opus-4-7"}
	}`), 0644)
	cfg, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Root != tmp {
		t.Errorf("expected root=%s, got %s", tmp, cfg.Root)
	}
}

func TestInitCreatesFile(t *testing.T) {
	tmp := t.TempDir()
	if err := Init(tmp); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Provider != "anthropic" {
		t.Errorf("expected anthropic default, got %s", cfg.LLM.Provider)
	}
	if cfg.LLM.Model == "" {
		t.Error("expected non-empty default model")
	}
}

func TestInitFailsIfAlreadyExists(t *testing.T) {
	tmp := t.TempDir()
	Init(tmp) //nolint:errcheck
	if err := Init(tmp); err == nil {
		t.Fatal("expected error on double init")
	}
}
