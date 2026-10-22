package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// --- CommandRole tests ---

// A command scope's own llm becomes the default for that command, while every
// other command keeps using the global default.
func TestCommandRoleScopedDefault(t *testing.T) {
	cfg := Config{
		LLM: LLMConfig{Provider: "anthropic", Model: "claude-sonnet-4-6"},
		Commands: map[string]ScopeConfig{
			"loop": {LLM: LLMConfig{Provider: "deepseek", Model: "deepseek-chat"}},
		},
	}
	if got := cfg.CommandRole("loop", "engineer"); got.Provider != "deepseek" {
		t.Errorf("loop should use scoped deepseek default, got %+v", got)
	}
	if got := cfg.CommandRole("swarm", "engineer"); got.Provider != "anthropic" {
		t.Errorf("other commands should keep global default, got %+v", got)
	}
}

// A role inside a command scope wins over the scope's own llm default.
func TestCommandRoleScopedRoleOverride(t *testing.T) {
	cfg := Config{
		LLM: LLMConfig{Provider: "anthropic", Model: "claude-sonnet-4-6"},
		Commands: map[string]ScopeConfig{
			"loop": {
				LLM:   LLMConfig{Provider: "deepseek", Model: "deepseek-chat"},
				Roles: map[string]LLMConfig{"escalation": {Provider: "anthropic", Model: "claude-sonnet-4-6"}},
			},
		},
	}
	if got := cfg.CommandRole("loop", "escalation"); got.Provider != "anthropic" {
		t.Errorf("escalation role should override scope default, got %+v", got)
	}
	if got := cfg.CommandRole("loop", "builder"); got.Provider != "deepseek" {
		t.Errorf("unscoped role should fall to scope default, got %+v", got)
	}
}

// Backwards compatibility: a config with no commands block resolves exactly
// like Role — existing deployed configs are unaffected.
func TestCommandRoleNoScopeFallsBackToRole(t *testing.T) {
	cfg := Config{
		LLM:   LLMConfig{Provider: "anthropic", Model: "claude-sonnet-4-6"},
		Roles: map[string]LLMConfig{"engineer": {Provider: "openai", Model: "gpt-4o"}},
	}
	if got := cfg.CommandRole("loop", "engineer"); got.Provider != "openai" {
		t.Errorf("no scope should defer to global role, got %+v", got)
	}
	if got := cfg.CommandRole("loop", "reviewer"); got.Provider != "anthropic" {
		t.Errorf("no scope, unknown role should fall to global default, got %+v", got)
	}
}

// An old config JSON without a "commands" key loads cleanly.
func TestLoadConfigWithoutCommandsBlock(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".colony"), 0755); err != nil {
		t.Fatal(err)
	}
	legacyJSON := `{
		"llm": {"provider": "anthropic", "model": "claude-sonnet-4-6"},
		"roles": {"engineer": {"provider": "openai", "model": "gpt-4o"}}
	}`
	if err := os.WriteFile(filepath.Join(tmp, ".colony", "config.json"), []byte(legacyJSON), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Commands != nil {
		t.Errorf("expected nil Commands for legacy config, got %+v", cfg.Commands)
	}
	if got := cfg.CommandRole("loop", "engineer"); got.Provider != "openai" {
		t.Errorf("legacy config should resolve via global role, got %+v", got)
	}
}

func TestLoadGates(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".colony"), 0755); err != nil {
		t.Fatal(err)
	}
	data := `{
		"llm": {"provider": "anthropic", "model": "claude-sonnet-4-6"},
		"gates": {"test": "dotnet test tests/A", "build": "dotnet build"}
	}`
	if err := os.WriteFile(filepath.Join(tmp, ".colony", "config.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	want := GateCommands{Test: "dotnet test tests/A", Build: "dotnet build"}
	if cfg.Gates != want {
		t.Errorf("got %+v, want %+v", cfg.Gates, want)
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

// fakePath points PATH at a dir holding empty executables named clis, so
// init's CLI detection sees exactly those and nothing from the host.
func fakePath(t *testing.T, clis ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, cli := range clis {
		if err := os.WriteFile(filepath.Join(dir, cli), []byte("#!/bin/sh\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func TestInitCreatesFile(t *testing.T) {
	fakePath(t, "claude", "codex", "crush")
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
	fakePath(t, "claude")
	tmp := t.TempDir()
	Init(tmp) //nolint:errcheck
	if err := Init(tmp); err == nil {
		t.Fatal("expected error on double init")
	}
}

func TestInitPicksCLIFromPath(t *testing.T) {
	cases := []struct {
		name     string
		clis     []string
		wantCLI  string
		provider string
		model    string
	}{
		{"codex when no claude", []string{"codex", "crush"}, "codex", "openai", ""},
		{"crush when only crush", []string{"crush"}, "", "openrouter", "deepseek/deepseek-v4.1-flash"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakePath(t, tc.clis...)
			tmp := t.TempDir()
			if err := Init(tmp); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(tmp)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.LLM.CLI != tc.wantCLI || cfg.LLM.Provider != tc.provider || cfg.LLM.Model != tc.model {
				t.Errorf("got llm %+v, want cli=%q provider=%q model=%q", cfg.LLM, tc.wantCLI, tc.provider, tc.model)
			}
			if !cfg.HasCommandRole("loop", "review") {
				t.Error("expected loop review role so the review gate stays on")
			}
			if got := cfg.CommandRole("loop", "review"); !reflect.DeepEqual(got, cfg.LLM) {
				t.Errorf("loop review role should match default llm, got %+v", got)
			}
		})
	}
}

func TestInitFailsWithNoAgentCLI(t *testing.T) {
	fakePath(t)
	tmp := t.TempDir()
	err := Init(tmp)
	if err == nil {
		t.Fatal("expected error when no agent CLI is installed")
	}
	for _, cli := range []string{"claude", "codex", "crush"} {
		if !strings.Contains(err.Error(), cli) {
			t.Errorf("error should mention %s: %v", cli, err)
		}
	}
	if _, statErr := os.Stat(filepath.Join(tmp, ".colony", "config.json")); !os.IsNotExist(statErr) {
		t.Error("config.json must not be written when no agent CLI is found")
	}
}

func TestValidateKeySkipsCodex(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	if err := (LLMConfig{Provider: "openai", CLI: "codex"}).ValidateKey(); err != nil {
		t.Errorf("codex manages its own auth, expected no error: %v", err)
	}
	if err := (LLMConfig{Provider: "openai"}).ValidateKey(); err == nil {
		t.Error("crush with openai still requires OPENAI_API_KEY")
	}
}

// --- KeyEnvName tests ---

func TestKeyEnvNameAnthropic(t *testing.T) {
	c := LLMConfig{Provider: "anthropic"}
	if got := c.KeyEnvName(); got != "ANTHROPIC_API_KEY" {
		t.Errorf("expected ANTHROPIC_API_KEY, got %s", got)
	}
}

func TestKeyEnvNameOpenRouter(t *testing.T) {
	c := LLMConfig{Provider: "openrouter"}
	if got := c.KeyEnvName(); got != "OPENROUTER_API_KEY" {
		t.Errorf("expected OPENROUTER_API_KEY, got %s", got)
	}
}

func TestKeyEnvNameOpenAI(t *testing.T) {
	c := LLMConfig{Provider: "openai"}
	if got := c.KeyEnvName(); got != "OPENAI_API_KEY" {
		t.Errorf("expected OPENAI_API_KEY, got %s", got)
	}
}

func TestKeyEnvNameUnknownProvider(t *testing.T) {
	c := LLMConfig{Provider: "unknown-llm"}
	if got := c.KeyEnvName(); got != "" {
		t.Errorf("expected empty string for unknown provider, got %s", got)
	}
}

func TestKeyEnvNameAPIKeyEnvOverride(t *testing.T) {
	c := LLMConfig{Provider: "anthropic", APIKeyEnv: "MY_CUSTOM_KEY"}
	if got := c.KeyEnvName(); got != "MY_CUSTOM_KEY" {
		t.Errorf("expected MY_CUSTOM_KEY override, got %s", got)
	}
}

// --- ValidateKey tests ---

func TestValidateKeySuccess(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-value")
	c := LLMConfig{Provider: "anthropic"}
	if err := c.ValidateKey(); err != nil {
		t.Errorf("expected nil error when key is set, got: %v", err)
	}
}

func TestValidateKeyAnthropicSkipsKeyCheck(t *testing.T) {
	os.Unsetenv("ANTHROPIC_API_KEY")
	c := LLMConfig{Provider: "anthropic"}
	if err := c.ValidateKey(); err != nil {
		t.Errorf("anthropic should not require API key (claude CLI manages auth), got: %v", err)
	}
}

func TestValidateKeyMissingEnvVar(t *testing.T) {
	os.Unsetenv("OPENAI_API_KEY")
	c := LLMConfig{Provider: "openai"}
	err := c.ValidateKey()
	if err == nil {
		t.Fatal("expected error when key is unset for non-anthropic provider")
	}
}

func TestValidateKeyUnknownProvider(t *testing.T) {
	c := LLMConfig{Provider: "bogus-provider"}
	err := c.ValidateKey()
	if err == nil {
		t.Fatal("expected error for unknown provider with no api_key_env")
	}
}

// --- Init generates correct model and no api_key_env ---

func TestInitGeneratesCorrectDefaults(t *testing.T) {
	fakePath(t, "claude")
	tmp := t.TempDir()
	if err := Init(tmp); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(tmp, ".colony", "config.json"))
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}

	llm, ok := m["llm"].(map[string]any)
	if !ok {
		t.Fatal("llm key missing")
	}
	if model, _ := llm["model"].(string); model != "claude-sonnet-4-6" {
		t.Errorf("expected model claude-sonnet-4-6, got %s", model)
	}
	if _, hasAPIKeyEnv := llm["api_key_env"]; hasAPIKeyEnv {
		t.Error("api_key_env must not be present in generated config")
	}
}
