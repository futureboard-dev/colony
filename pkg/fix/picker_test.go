package fix

import (
	"context"
	"errors"
	"testing"

	"github.com/futureboard-dev/colony/pkg/config"
)

// withInstalled stubs onPath so InstalledTools reports exactly the given
// tools, regardless of what's actually on PATH in the test environment.
func withInstalled(t *testing.T, tools ...Tool) {
	t.Helper()
	installed := make(map[string]bool, len(tools))
	for _, tl := range tools {
		installed[string(tl)] = true
	}
	orig := onPath
	onPath = func(cli string) bool { return installed[cli] }
	t.Cleanup(func() { onPath = orig })
}

// withConfig stubs loadConfig to return cfg (or an error) without touching disk.
func withConfig(t *testing.T, cfg *config.Config, err error) {
	t.Helper()
	orig := loadConfig
	loadConfig = func() (*config.Config, error) { return cfg, err }
	t.Cleanup(func() { loadConfig = orig })
}

// --- InstalledTools / tool install cases ---

func TestInstalledToolsNone(t *testing.T) {
	withInstalled(t)
	got, err := (picker{}).InstalledTools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no installed tools, got %v", got)
	}
}

func TestInstalledToolsOne(t *testing.T) {
	withInstalled(t, ToolCodex)
	got, err := (picker{}).InstalledTools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != ToolCodex {
		t.Errorf("expected [codex], got %v", got)
	}
}

func TestInstalledToolsSeveral(t *testing.T) {
	withInstalled(t, ToolClaude, ToolCrush)
	got, err := (picker{}).InstalledTools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != ToolClaude || got[1] != ToolCrush {
		t.Errorf("expected [claude crush] in PATH order, got %v", got)
	}
}

// --- ResolveChoice: tool selection across the three install cases ---

func TestResolveChoiceNoToolInstalled(t *testing.T) {
	withInstalled(t)
	withConfig(t, nil, errors.New("no config"))
	_, err := (picker{}).ResolveChoice(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected an error when no tool is installed")
	}
}

func TestResolveChoiceOneToolInstalledUsedWithoutAsking(t *testing.T) {
	withInstalled(t, ToolCodex)
	withConfig(t, nil, errors.New("no config"))
	got, err := (picker{}).ResolveChoice(context.Background(), Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Tool != ToolCodex {
		t.Errorf("expected the sole installed tool to be used, got %+v", got)
	}
}

func TestResolveChoiceSeveralInstalledPreselectsColonyDefault(t *testing.T) {
	withInstalled(t, ToolClaude, ToolCodex, ToolCrush)
	withConfig(t, &config.Config{LLM: config.LLMConfig{Provider: "openai", CLI: "codex"}}, nil)
	got, err := (picker{}).ResolveChoice(context.Background(), Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Tool != ToolCodex {
		t.Errorf("expected Colony's configured tool preselected, got %+v", got)
	}
}

func TestResolveChoiceSeveralInstalledNoConfigFallsBackToFirst(t *testing.T) {
	withInstalled(t, ToolClaude, ToolCrush)
	withConfig(t, nil, errors.New("no config"))
	got, err := (picker{}).ResolveChoice(context.Background(), Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Tool != ToolClaude {
		t.Errorf("expected first installed tool as fallback, got %+v", got)
	}
}

// --- Preselected stuck tool / one-off tool switch ---

func TestResolveChoicePreselectedStuckToolWins(t *testing.T) {
	withInstalled(t, ToolClaude, ToolCodex, ToolCrush)
	withConfig(t, &config.Config{LLM: config.LLMConfig{Provider: "anthropic"}}, nil)
	got, err := (picker{}).ResolveChoice(context.Background(), Request{Choice: Choice{Tool: ToolCrush}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Tool != ToolCrush {
		t.Errorf("expected preselected stuck tool crush, got %+v", got)
	}
}

func TestResolveChoicePreselectedToolNotInstalledIgnored(t *testing.T) {
	withInstalled(t, ToolClaude)
	withConfig(t, nil, errors.New("no config"))
	got, err := (picker{}).ResolveChoice(context.Background(), Request{Choice: Choice{Tool: ToolCrush}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Tool != ToolClaude {
		t.Errorf("expected fallback to the only installed tool, got %+v", got)
	}
}

// --- Model precedence: one-off > Colony setting > tool default ---

func TestResolveModelOneOffWins(t *testing.T) {
	withInstalled(t, ToolClaude)
	withConfig(t, &config.Config{
		LLM: config.LLMConfig{Provider: "anthropic", Model: "claude-sonnet-4-6"},
		Fix: config.FixConfig{Models: map[string]string{"claude": "claude-opus-4-8"}},
	}, nil)
	got, err := (picker{}).ResolveChoice(context.Background(), Request{Choice: Choice{Model: "claude-haiku-4-5"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Model != "claude-haiku-4-5" {
		t.Errorf("expected one-off model to win, got %+v", got)
	}
}

func TestResolveModelFlowSettingWinsOverToolSetting(t *testing.T) {
	withInstalled(t, ToolClaude)
	withConfig(t, &config.Config{
		LLM: config.LLMConfig{Provider: "anthropic", Model: "claude-sonnet-4-6"},
		Fix: config.FixConfig{
			Models: map[string]string{"claude": "claude-opus-4-8"},
			Flow:   map[string]string{"loop": "claude-haiku-4-5"},
		},
	}, nil)
	got, err := (picker{}).ResolveChoice(context.Background(), Request{Flow: "loop"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Model != "claude-haiku-4-5" {
		t.Errorf("expected flow setting to win over tool setting, got %+v", got)
	}
}

func TestResolveModelToolSettingWinsOverColonyDefault(t *testing.T) {
	withInstalled(t, ToolClaude)
	withConfig(t, &config.Config{
		LLM: config.LLMConfig{Provider: "anthropic", Model: "claude-sonnet-4-6"},
		Fix: config.FixConfig{Models: map[string]string{"claude": "claude-opus-4-8"}},
	}, nil)
	got, err := (picker{}).ResolveChoice(context.Background(), Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Model != "claude-opus-4-8" {
		t.Errorf("expected per-tool Colony setting to win, got %+v", got)
	}
}

func TestResolveModelFallsBackToColonyDefaultLLMModel(t *testing.T) {
	withInstalled(t, ToolClaude)
	withConfig(t, &config.Config{LLM: config.LLMConfig{Provider: "anthropic", Model: "claude-sonnet-4-6"}}, nil)
	got, err := (picker{}).ResolveChoice(context.Background(), Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Model != "claude-sonnet-4-6" {
		t.Errorf("expected the saved default LLM model, got %+v", got)
	}
}

func TestResolveModelFallsBackToToolDefaultWhenNothingSaved(t *testing.T) {
	withInstalled(t, ToolCrush)
	withConfig(t, nil, errors.New("no config"))
	got, err := (picker{}).ResolveChoice(context.Background(), Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Model != "" {
		t.Errorf("expected empty model (tool default), got %+v", got)
	}
}

func TestResolveModelDefaultLLMModelOnlyAppliesToItsOwnTool(t *testing.T) {
	// cfg's default LLM is claude, but the resolved tool here is codex (the
	// only one installed) — the claude-specific default model must not leak
	// onto codex.
	withInstalled(t, ToolCodex)
	withConfig(t, &config.Config{LLM: config.LLMConfig{Provider: "anthropic", Model: "claude-sonnet-4-6"}}, nil)
	got, err := (picker{}).ResolveChoice(context.Background(), Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Model != "" {
		t.Errorf("expected empty model for codex, got %+v", got)
	}
}

// --- Invalid model: surfaces the tool's error, returns to the picker ---

func TestResolveChoiceExcludingSkipsRejectedOneOffModel(t *testing.T) {
	withInstalled(t, ToolClaude)
	withConfig(t, &config.Config{LLM: config.LLMConfig{Provider: "anthropic", Model: "claude-sonnet-4-6"}}, nil)
	got, err := ResolveChoiceExcluding(context.Background(), Request{Choice: Choice{Model: "bogus-model"}}, "bogus-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Model != "claude-sonnet-4-6" {
		t.Errorf("expected fall-through to Colony default after excluding the bad one-off model, got %+v", got)
	}
	if got.Tool != ToolClaude {
		t.Errorf("expected tool unchanged after a bad model, got %+v", got)
	}
}

func TestResolveChoiceExcludingSkipsRejectedColonyDefault(t *testing.T) {
	withInstalled(t, ToolClaude)
	withConfig(t, &config.Config{LLM: config.LLMConfig{Provider: "anthropic", Model: "claude-sonnet-4-6-stale"}}, nil)
	got, err := ResolveChoiceExcluding(context.Background(), Request{}, "claude-sonnet-4-6-stale")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Model != "" {
		t.Errorf("expected fall-through to tool default after excluding a stale saved model, got %+v", got)
	}
}

func TestResolveChoiceExcludingNeverErrorsOnBadModel(t *testing.T) {
	// A bad model must never surface as "no tool installed" / a new block —
	// it always resolves to *something* usable (here, the empty tool default).
	withInstalled(t, ToolCrush)
	withConfig(t, nil, errors.New("no config"))
	got, err := ResolveChoiceExcluding(context.Background(), Request{Choice: Choice{Model: "nope"}}, "nope")
	if err != nil {
		t.Fatalf("expected no error recovering from a bad model, got: %v", err)
	}
	if got.Tool != ToolCrush || got.Model != "" {
		t.Errorf("expected {crush, \"\"}, got %+v", got)
	}
}

// --- IsInvalidModel classification ---

func TestIsInvalidModel(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"unrelated error", errors.New("connection refused"), false},
		{"unknown model", errors.New("Error: model 'claude-opus-99' not found"), true},
		{"invalid model", errors.New("invalid model specified"), true},
		{"unavailable model", errors.New("model is unavailable for this account"), true},
		{"model mentioned but not an error about validity", errors.New("loaded model config from disk"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsInvalidModel(tc.err); got != tc.want {
				t.Errorf("IsInvalidModel(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// --- Registration ---

func TestPickerRegisteredAsResolver(t *testing.T) {
	// init() in picker.go must have called SetResolver; a sibling package's
	// contracts_test already proves ErrNotConfigured is returned pre-
	// registration, so by the time this test package runs, InstalledTools
	// should delegate successfully instead of erroring.
	withInstalled(t, ToolClaude)
	tools, err := InstalledTools(context.Background())
	if err != nil {
		t.Fatalf("expected the package-registered resolver to answer, got err: %v", err)
	}
	if len(tools) != 1 || tools[0] != ToolClaude {
		t.Errorf("expected [claude], got %v", tools)
	}
}
