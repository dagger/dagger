package llmconfig

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/adrg/xdg"
	"github.com/gofrs/flock"
	toml "github.com/pelletier/go-toml"
)

// oauthRefreshMu serializes OAuth load→refresh→persist sequences so
// concurrent secret resolutions can't double-spend a one-time refresh
// token (providers rotate them), or clobber another provider's freshly
// rotated token via Save's whole-section rewrite.
var oauthRefreshMu sync.Mutex

// oauthRefreshFloor bounds how often a single provider is refreshed in this
// process, whatever its persisted expiry says. A token whose whole lifetime is
// shorter than the safety margin sits inside that margin from the moment it is
// issued, so the expiry check alone would refresh — and rotate the single-use
// refresh token — on every secret resolution, twice per credential.
const oauthRefreshFloor = 30 * time.Second

// lastOAuthRefresh records when each provider was last refreshed. Keyed by
// config file as well as provider name, so pointing DAGGER_CONFIG at a
// different credential store starts from a clean slate. Guarded by
// oauthRefreshMu.
var lastOAuthRefresh = map[string]time.Time{}

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
	AuthType     string `toml:"auth_type,omitempty"`     // "oauth" for Claude Code OAuth
	AuthToken    string `toml:"auth_token,omitempty"`    // OAuth access token
	RefreshToken string `toml:"refresh_token,omitempty"` // OAuth refresh token
	// TokenExpiresAt is when the access token truly expires, in unix
	// milliseconds, with no safety margin baked in. The margin is applied when
	// the value is checked (IsTokenExpired), so an absent or short expires_in
	// can't persist an already-expired value.
	TokenExpiresAt int64 `toml:"token_expires_at,omitempty"`
	// TokenExpiry is the legacy expiry: the same instant with the safety margin
	// already subtracted. Still written so that an older CLI sharing this config
	// file keeps working — it reads only this field and treats 0 as expired,
	// which would put it in a refresh loop rotating the token out from under
	// this one. TokenExpiresAt wins when both are present.
	TokenExpiry      int64  `toml:"token_expiry,omitempty"`
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
	// Create the directory the config file actually lives in, which is not
	// ConfigRoot when DAGGER_CONFIG points somewhere else.
	if err := os.MkdirAll(filepath.Dir(ConfigFile), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Lock file for atomic read-modify-write
	lockFile := ConfigFile + ".lock"
	lock := flock.New(lockFile)
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}
	defer lock.Unlock()

	return c.write()
}

// write merges the [llm] section into the config document on disk and rewrites
// it atomically. The caller must already hold the cross-process lock: flock is
// per open file description, so re-taking it here would deadlock a caller that
// holds it (withConfigLock).
func (c *Config) write() error {
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

	// Write atomically with 0600 permissions so a concurrent cross-process
	// reader (Load, called on every dagger command) never observes a truncated
	// or partially written file.
	if err := atomicWriteFile(ConfigFile, []byte(out), 0600); err != nil {
		return err
	}

	return nil
}

// withConfigLock runs fn against the config as it exists on disk *right now* —
// re-read after the cross-process lock is taken — and persists the result
// before releasing the lock, so load→modify→persist is a single critical
// section.
//
// Re-reading is the point. OAuth refresh tokens are single-use and rotating:
// a process that loaded the config before another process refreshed would
// otherwise spend a dead refresh token (invalid_grant, i.e. a permanent
// logout) and then write its stale snapshot back over the winner's rotated
// token. fn reports whether it changed anything; nothing is written when it
// did not.
func withConfigLock(fn func(cfg *Config) (changed bool, err error)) error {
	if err := os.MkdirAll(filepath.Dir(ConfigFile), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	lock := flock.New(ConfigFile + ".lock")
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}
	defer lock.Unlock()

	cfg, err := Load()
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = &Config{LLM: LLMConfig{Providers: make(map[string]Provider)}}
	}

	changed, fnErr := fn(cfg)
	if !changed {
		return fnErr
	}
	if err := cfg.write(); err != nil {
		return errors.Join(fnErr, err)
	}
	return fnErr
}

// UpdateFile applies fn to the current config file contents under the
// cross-process lock and writes the result back atomically with 0600
// permissions. fn receives nil when the file does not exist yet. The file is
// shared between subsystems ([llm], [workspaces], ...), so fn must preserve
// sections it does not own.
func UpdateFile(fn func(existing []byte) ([]byte, error)) error {
	if err := os.MkdirAll(filepath.Dir(ConfigFile), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	lock := flock.New(ConfigFile + ".lock")
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}
	defer lock.Unlock()

	data, err := os.ReadFile(ConfigFile)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to read config file: %w", err)
		}
		data = nil
	}

	updated, err := fn(data)
	if err != nil {
		return err
	}
	return atomicWriteFile(ConfigFile, updated, 0600)
}

// atomicWriteFile writes data to path atomically by writing to a temporary
// file in the same directory and renaming it into place (rename is atomic on
// POSIX). The temp file is removed if the write or rename fails.
func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp config file: %w", err)
	}
	tmpName := tmp.Name()
	// Clean up the temp file unless it was successfully renamed into place.
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("failed to set config file permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
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
	if err := atomicWriteFile(ConfigFile, []byte(out), 0600); err != nil {
		return err
	}
	return nil
}

// refreshProviderToken refreshes an expired OAuth provider in place, dispatching
// to the provider-specific refresh flow. It returns the (possibly updated)
// provider and whether it changed.
func refreshProviderToken(ctx context.Context, name string, provider Provider) (Provider, bool, error) {
	// A disabled provider's credentials are never exported (applyLLMConfigEnv
	// skips it), so refreshing it would spend its single-use grant for nothing
	// — and rotate a refresh token the user still expects to work when they
	// re-enable the provider.
	if !provider.Enabled || !provider.IsOAuth() || !IsTokenExpired(&provider) {
		return provider, false, nil
	}
	// Rate-limit per provider, so a pathologically short-lived token can't turn
	// every resolution into another rotation. The caller holds oauthRefreshMu.
	floorKey := ConfigFile + "\x00" + name
	if time.Since(lastOAuthRefresh[floorKey]) < oauthRefreshFloor {
		return provider, false, nil
	}
	lastOAuthRefresh[floorKey] = time.Now()

	var refreshed *Provider
	var err error
	switch name {
	case "openai-codex":
		refreshed, err = RefreshOpenAIOAuthToken(ctx, &provider)
	default:
		// Anthropic and other providers use the standard refresh
		refreshed, err = RefreshOAuthToken(ctx, &provider)
	}
	if err != nil {
		return provider, false, fmt.Errorf("failed to refresh OAuth token for %s: %w", name, err)
	}
	return *refreshed, true, nil
}

// RefreshOAuthTokensIfNeeded checks all OAuth providers in the config and
// refreshes any expired tokens. This should be called client-side before
// connecting to the engine.
func RefreshOAuthTokensIfNeeded(ctx context.Context) error {
	oauthRefreshMu.Lock()
	defer oauthRefreshMu.Unlock()

	// Nothing to refresh, and no reason to create the config directory (or its
	// lock file) for a user who has no config at all.
	if !ConfigExists() {
		return nil
	}

	return withConfigLock(func(cfg *Config) (bool, error) {
		var changed bool
		var errs []error
		for name, provider := range cfg.LLM.Providers {
			refreshed, didChange, err := refreshProviderToken(ctx, name, provider)
			if err != nil {
				// Keep going: a refresh grant is single-use, so a provider
				// that already spent its own must not lose the result because
				// a later provider failed — and which provider that is was
				// decided by Go's randomized map order.
				errs = append(errs, err)
				continue
			}
			if didChange {
				cfg.LLM.Providers[name] = refreshed
				changed = true
			}
		}
		return changed, errors.Join(errs...)
	})
}

// RefreshOAuthProviderIfNeeded refreshes a single OAuth provider by name if its
// token has expired, persisting the result. It returns the provider as it now
// stands (refreshed or not), or nil if it is absent, disabled, or not an OAuth
// provider. Used to keep a long-running session's bearer token fresh: the
// client re-resolves the token on demand rather than only at startup.
func RefreshOAuthProviderIfNeeded(ctx context.Context, name string) (*Provider, error) {
	oauthRefreshMu.Lock()
	defer oauthRefreshMu.Unlock()

	if !ConfigExists() {
		return nil, nil
	}

	var current *Provider
	err := withConfigLock(func(cfg *Config) (bool, error) {
		provider, ok := cfg.LLM.Providers[name]
		if !ok || !provider.IsOAuth() || !provider.Enabled {
			return false, nil
		}
		refreshed, changed, err := refreshProviderToken(ctx, name, provider)
		if err != nil {
			return false, err
		}
		current = &refreshed
		if changed {
			cfg.LLM.Providers[name] = refreshed
		}
		return changed, nil
	})
	if err != nil {
		return nil, err
	}
	return current, nil
}
