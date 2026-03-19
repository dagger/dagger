package llmconfig

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
	"github.com/gofrs/flock"
	toml "github.com/pelletier/go-toml"
)

const (
	ConfigFileName = "config.toml"
)

var (
	ConfigRoot = filepath.Join(xdg.ConfigHome, "dagger")
	ConfigFile = configFilePath()
)

func configFilePath() string {
	if p := os.Getenv("DAGGER_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(ConfigRoot, ConfigFileName)
}

// Config represents the top-level dagger config file.
// Only the [llm] section is managed here; other sections are preserved as-is.
type Config struct {
	LLM LLMConfig `toml:"llm"`
}

// LLMConfig represents the [llm] section.
type LLMConfig struct {
	DefaultProvider string              `toml:"default_provider"`
	DefaultModel    string              `toml:"default_model,omitempty"`
	Providers       map[string]Provider `toml:"providers"`
}

// Provider represents a single LLM provider's configuration.
type Provider struct {
	APIKey           string `toml:"api_key"`
	BaseURL          string `toml:"base_url,omitempty"`
	Model            string `toml:"model,omitempty"`
	AzureVersion     string `toml:"azure_version,omitempty"`
	DisableStreaming bool   `toml:"disable_streaming,omitempty"`
	Enabled          bool   `toml:"enabled"`

	// OAuth fields for Claude Code subscription auth
	AuthType         string `toml:"auth_type,omitempty"`         // "oauth" for Claude Code OAuth
	AuthToken        string `toml:"auth_token,omitempty"`        // OAuth access token
	RefreshToken     string `toml:"refresh_token,omitempty"`     // OAuth refresh token
	TokenExpiry      int64  `toml:"token_expiry,omitempty"`      // Unix timestamp (ms) when access token expires
	SubscriptionType string `toml:"subscription_type,omitempty"` // "pro", "max", "team", "enterprise"

	// ThinkingMode controls extended thinking / reasoning effort.
	// Values are provider-specific reasoning levels from catwalk
	// (e.g. "low", "medium", "high", "adaptive").
	ThinkingMode string `toml:"thinking_mode,omitempty"`

	// APICompat selects which API protocol to use for custom/local endpoints.
	// Values: "openai" (OpenAI-compatible) or "anthropic" (Anthropic-compatible).
	// When set, BaseURL is used as the endpoint and the model name is passed through.
	APICompat string `toml:"api_compat,omitempty"`
}

// IsOAuth returns true if this provider uses OAuth authentication.
func (p *Provider) IsOAuth() bool {
	return p.AuthType == "oauth"
}

// Load reads config from disk, returns nil if not exists.
func Load() (*Config, error) {
	data, err := os.ReadFile(ConfigFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No config is OK
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Initialize providers map if nil
	if cfg.LLM.Providers == nil {
		cfg.LLM.Providers = make(map[string]Provider)
	}

	return &cfg, nil
}

// Save writes config to disk with proper permissions (0600).
func (c *Config) Save() error {
	// Create directory if needed
	if err := os.MkdirAll(ConfigRoot, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Lock file for atomic writes
	lockFile := ConfigFile + ".lock"
	lock := flock.New(lockFile)
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}
	defer lock.Unlock()

	// Initialize providers map if nil
	if c.LLM.Providers == nil {
		c.LLM.Providers = make(map[string]Provider)
	}

	// Marshal to TOML
	data, err := toml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write with 0600 permissions
	if err := os.WriteFile(ConfigFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// ConfigExists checks if config file exists.
func ConfigExists() bool {
	_, err := os.Stat(ConfigFile)
	return err == nil
}

// LLMConfigured checks if the config file exists and has LLM providers configured.
func LLMConfigured() bool {
	cfg, err := Load()
	if err != nil || cfg == nil {
		return false
	}
	return len(cfg.LLM.Providers) > 0
}

// Remove deletes the config file.
func Remove() error {
	if err := os.Remove(ConfigFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove config file: %w", err)
	}
	return nil
}

// RefreshOAuthTokensIfNeeded checks all OAuth providers in the config and
// refreshes any expired tokens. This should be called client-side before
// connecting to the engine.
func RefreshOAuthTokensIfNeeded() error {
	cfg, err := Load()
	if err != nil || cfg == nil {
		return nil // No config or error loading is OK
	}

	var changed bool
	for name, provider := range cfg.LLM.Providers {
		if !provider.IsOAuth() {
			continue
		}
		if !IsTokenExpired(&provider) {
			continue
		}
		var refreshed *Provider
		switch name {
		case "openai-codex":
			refreshed, err = RefreshOpenAIOAuthToken(&provider)
		default:
			// Anthropic and other providers use the standard refresh
			refreshed, err = RefreshOAuthToken(&provider)
		}
		if err != nil {
			return fmt.Errorf("failed to refresh OAuth token for %s: %w", name, err)
		}
		cfg.LLM.Providers[name] = *refreshed
		changed = true
	}

	if changed {
		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save refreshed tokens: %w", err)
		}
	}

	return nil
}
