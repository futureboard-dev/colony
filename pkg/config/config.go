package config

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type LLMConfig struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
	// CLI pins the agent CLI: "claude", "codex" or "crush". Empty derives it
	// from Provider (anthropic → claude, everything else → crush).
	CLI string `json:"cli,omitempty"`
	// Fallbacks are tried in order only after quota or rate-limit failures.
	Fallbacks []LLMConfig `json:"fallbacks,omitempty"`
}

// KeyEnvName returns the env var name for the API key.
// APIKeyEnv in config takes precedence over the provider default.
func (c LLMConfig) KeyEnvName() string {
	if c.APIKeyEnv != "" {
		return c.APIKeyEnv
	}
	switch c.Provider {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "deepseek":
		return "DEEPSEEK_API_KEY"
	case "openrouter":
		return "OPENROUTER_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	default:
		return ""
	}
}

// ValidateKey checks that the required API key environment variable is set.
// Anthropic is skipped — the claude CLI manages its own auth and does not need
// ANTHROPIC_API_KEY; forwarding a key from env would be rejected by the CLI.
// Codex is skipped for the same reason: `codex login` owns its auth.
func (c LLMConfig) ValidateKey() error {
	if c.Provider == "anthropic" || c.CLI == "codex" {
		return nil
	}
	key := c.KeyEnvName()
	if key == "" {
		return fmt.Errorf("unknown provider %q: cannot derive API key env var — set api_key_env in config", c.Provider)
	}
	if os.Getenv(key) == "" {
		return fmt.Errorf("missing API key for provider %q: %s is not set\n  Fix: export %s=<your-key>", c.Provider, key, key)
	}
	return nil
}

// Config is the top-level .colony/config.json structure.
// Roles lets you assign different models to different agent roles.
// If a role is not specified, the top-level LLM config is used.
// Commands lets a single command (e.g. "loop") override the default LLM and
// roles for its own run, leaving every other command on the global defaults.
// WorktreeBase overrides where agent worktrees are created; empty means the
// built-in default (~/Projects/.worktrees). "~" is expanded.
// Gates overrides the quality-gate commands for this project.
// GHUser pins the gh account the loop pushes and opens PRs as; empty uses
// gh's active account.
type Config struct {
	Root         string                 `json:"root"`
	WorktreeBase string                 `json:"worktree_base,omitempty"`
	LLM          LLMConfig              `json:"llm"`
	Roles        map[string]LLMConfig   `json:"roles,omitempty"`
	Commands     map[string]ScopeConfig `json:"commands,omitempty"`
	Gates        GateCommands           `json:"gates,omitempty"`
	GHUser       string                 `json:"gh_user,omitempty"`
}

// GateCommands replaces individual quality-gate commands. A non-empty field
// wins over the language's built-in command, so a lang "none" project (one
// colony has no toolchain for, e.g. C#) can still gate on its own tests.
type GateCommands struct {
	Format    string `json:"format,omitempty"`
	Vet       string `json:"vet,omitempty"`
	Lint      string `json:"lint,omitempty"`
	TypeCheck string `json:"typecheck,omitempty"`
	Test      string `json:"test,omitempty"`
	Build     string `json:"build,omitempty"`
}

// ScopeConfig overrides model selection for a single command. An empty LLM or
// Roles falls through to the global Config, so a scope only needs to specify
// what differs.
type ScopeConfig struct {
	LLM   LLMConfig            `json:"llm"`
	Roles map[string]LLMConfig `json:"roles,omitempty"`
}

// Role returns the LLMConfig for a named role, falling back to the default LLM config.
func (c *Config) Role(name string) LLMConfig {
	if c.Roles != nil {
		if cfg, ok := c.Roles[name]; ok {
			return cfg
		}
	}
	return c.LLM
}

// CommandRole returns the LLMConfig for a role within a named command's scope.
// Precedence (most specific first):
//  1. commands[command].roles[role]
//  2. commands[command].llm  (the command's own default)
//  3. global roles[role]
//  4. global llm
//
// A config with no matching command scope resolves exactly like Role(role),
// so existing configs without a "commands" block are unaffected.
func (c *Config) CommandRole(command, role string) LLMConfig {
	if scope, ok := c.Commands[command]; ok {
		if cfg, ok := scope.Roles[role]; ok {
			return cfg
		}
		if scope.LLM.Provider != "" {
			return scope.LLM
		}
	}
	return c.Role(role)
}

// HasCommandRole reports whether a role is explicitly configured for a command
// scope or globally — i.e. resolvable to something other than the default LLM.
// Used to auto-enable optional graph nodes (e.g. the loop review gate) when the
// user has assigned a model to that role in config.json.
func (c *Config) HasCommandRole(command, role string) bool {
	if scope, ok := c.Commands[command]; ok {
		if _, ok := scope.Roles[role]; ok {
			return true
		}
	}
	_, ok := c.Roles[role]
	return ok
}

// LensRole returns the LLMConfig for a specific lens, checking "<lens>_lens" first,
// then the shared "lens_reviewer" role, then the default LLM config.
func (c *Config) LensRole(lens string) LLMConfig {
	if c.Roles != nil {
		if cfg, ok := c.Roles[lens+"_lens"]; ok {
			return cfg
		}
		if cfg, ok := c.Roles["lens_reviewer"]; ok {
			return cfg
		}
	}
	return c.LLM
}

func Load(projectRoot string) (*Config, error) {
	path := filepath.Join(projectRoot, ".colony", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no .colony/config.json — run: colony init")
		}
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Root == "" {
		cfg.Root = projectRoot
	}
	return &cfg, nil
}

func Init(projectRoot string) error {
	dir := filepath.Join(projectRoot, ".colony")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, "config.json")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf(".colony/config.json already exists")
	}
	cfg, err := defaultConfig(projectRoot)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

// defaultConfig builds the init config for whichever agent CLI is on PATH,
// preferring claude, then codex, then crush.
func defaultConfig(projectRoot string) (Config, error) {
	switch {
	case onPath("claude"):
		return claudeConfig(projectRoot), nil
	case onPath("codex"):
		// Model left empty so codex uses its own default.
		llm := LLMConfig{Provider: "openai", CLI: "codex"}
		return Config{
			Root: projectRoot,
			LLM:  llm,
			// Defining the loop review role keeps the review gate on.
			Commands: map[string]ScopeConfig{"loop": {Roles: map[string]LLMConfig{"review": llm}}},
		}, nil
	case onPath("crush"):
		llm := LLMConfig{Provider: "openrouter", Model: "deepseek/deepseek-v4.1-flash"}
		return Config{
			Root:     projectRoot,
			LLM:      llm,
			Commands: map[string]ScopeConfig{"loop": {Roles: map[string]LLMConfig{"review": llm}}},
		}, nil
	}
	return Config{}, fmt.Errorf("no agent CLI found — install one of:\n" +
		"  claude  https://claude.ai/code\n" +
		"  codex   npm install -g @openai/codex\n" +
		"  crush   brew install charmbracelet/tap/crush")
}

func onPath(cli string) bool {
	_, err := exec.LookPath(cli)
	return err == nil
}

func claudeConfig(projectRoot string) Config {
	return Config{
		Root: projectRoot,
		LLM: LLMConfig{
			Provider: "anthropic",
			Model:    "claude-sonnet-4-6",
		},
		Roles: map[string]LLMConfig{
			// lens_reviewer uses Haiku for cheaper, faster per-lens analysis.
			// synthesis (verdict + dedup) stays on Sonnet via the default LLM.
			"lens_reviewer": {
				Provider: "anthropic",
				Model:    "claude-haiku-4-5-20251001",
			},
			// bugs_lens uses Sonnet for higher-accuracy bug detection.
			"bugs_lens": {
				Provider: "anthropic",
				Model:    "claude-sonnet-4-6",
			},
		},
		Commands: map[string]ScopeConfig{
			// The loop's review gate runs before a task is committed; pin it to
			// Opus so a strong model catches stubs/unimplemented specs that a
			// cheaper builder model (e.g. deepseek) leaves behind. Defining this
			// role auto-enables the review gate — remove it to disable.
			"loop": {
				Roles: map[string]LLMConfig{
					"review": {
						Provider: "anthropic",
						Model:    "claude-opus-4-8",
					},
				},
			},
		},
	}
}
