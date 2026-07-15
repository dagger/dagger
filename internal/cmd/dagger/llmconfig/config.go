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

	// ReasoningEffort is the reasoning level for the provider's model, taken
	// from the model's catwalk reasoning_levels (e.g. "low", "medium", "high").
	// Empty disables reasoning.
	ReasoningEffort string `toml:"reasoning_effort,omitempty"`

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

// Save writes the [llm] section to disk with proper permissions (0600). The
// config file is shared with other subsystems, so the section is merged into
// the existing document rather than replacing the whole file.
func (c *Config) Save() error {
	// Create directory if needed
	if err := os.MkdirAll(ConfigRoot, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Lock file for atomic read-modify-write
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

	doc, err := loadDocument()
	if err != nil {
		return err
	}

	// Marshal the [llm] section and graft it onto the existing document.
	data, err := toml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	llmDoc, err := toml.LoadBytes(data)
	if err != nil {
		return fmt.Errorf("failed to reparse config: %w", err)
	}
	doc.Set("llm", llmDoc.Get("llm"))

	out, err := doc.ToTomlString()
	if err != nil {
		return fmt.Errorf("failed to serialize config: %w", err)
	}

	// Write with 0600 permissions
	if err := os.WriteFile(ConfigFile, []byte(out), 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// loadDocument parses the existing config file into a TOML document,
// returning an empty document if the file does not exist.
func loadDocument() (*toml.Tree, error) {
	data, err := os.ReadFile(ConfigFile)
	if err != nil {
		if os.IsNotExist(err) {
			data = nil
		} else {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
	}
	doc, err := toml.LoadBytes(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}
	return doc, nil
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

// Remove deletes the [llm] section from the config file. Other sections are
// preserved; the file itself is only removed once nothing else remains.
func Remove() error {
	if _, err := os.Stat(ConfigFile); os.IsNotExist(err) {
		return nil
	}

	lockFile := ConfigFile + ".lock"
	lock := flock.New(lockFile)
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}
	defer lock.Unlock()

	doc, err := loadDocument()
	if err != nil {
		return err
	}
	if doc.Has("llm") {
		if err := doc.Delete("llm"); err != nil {
			return fmt.Errorf("failed to delete llm section: %w", err)
		}
	}

	if len(doc.Keys()) == 0 {
		if err := os.Remove(ConfigFile); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove config file: %w", err)
		}
		return nil
	}

	out, err := doc.ToTomlString()
	if err != nil {
		return fmt.Errorf("failed to serialize config: %w", err)
	}
	if err := os.WriteFile(ConfigFile, []byte(out), 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	return nil
}

// RefreshOAuthTokensIfNeeded checks all OAuth providers in the config and
// refreshes any expired tokens. This should be called client-side before
// connecting to the engine.
func RefreshOAuthTokensIfNeeded() error {
	cfg, err := Load()
	if err != nil || cfg == nil {
		return nil //nolint:nilerr // a missing or unreadable config is non-fatal here
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
