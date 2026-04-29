package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type LLMConfig struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
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
func (c LLMConfig) ValidateKey() error {
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
type Config struct {
	Root  string               `json:"root"`
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
	cfg := Config{
		Root: projectRoot,
		LLM: LLMConfig{
			Provider: "anthropic",
			Model:    "claude-sonnet-4-6",
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}
