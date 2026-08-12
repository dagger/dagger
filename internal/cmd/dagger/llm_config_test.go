package daggercmd

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/client/secretprovider"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
)

// TestRemoveKeyClearsDefaultModel verifies that removing the default provider
// also clears the default model. Otherwise the stale model stays bound to
// whatever provider becomes default next, so applyLLMConfigEnv would export
// e.g. OPENAI_MODEL=claude-sonnet-4.5 and break every LLM call.
func TestRemoveKeyClearsDefaultModel(t *testing.T) {
	tempDir := t.TempDir()
	origConfigRoot := llmconfig.ConfigRoot
	origConfigFile := llmconfig.ConfigFile
	t.Cleanup(func() {
		llmconfig.ConfigRoot = origConfigRoot
		llmconfig.ConfigFile = origConfigFile
	})
	llmconfig.ConfigRoot = filepath.Join(tempDir, "dagger")
	llmconfig.ConfigFile = filepath.Join(llmconfig.ConfigRoot, llmconfig.ConfigFileName)

	cfg := &llmconfig.Config{
		LLM: llmconfig.LLMConfig{
			DefaultProvider: "anthropic",
			DefaultModel:    "claude-sonnet-4.5",
			Providers: map[string]llmconfig.Provider{
				"anthropic": {APIKey: "sk-ant", Enabled: true},
			},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	llmRemoveKeyCmd.SetOut(io.Discard)
	if err := llmRemoveKeyCmd.RunE(llmRemoveKeyCmd, []string{"anthropic"}); err != nil {
		t.Fatalf("remove-key failed: %v", err)
	}

	loaded, err := llmconfig.Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if loaded.LLM.DefaultProvider != "" {
		t.Errorf("DefaultProvider = %q, want empty after removing the default provider", loaded.LLM.DefaultProvider)
	}
	if loaded.LLM.DefaultModel != "" {
		t.Errorf("DefaultModel = %q, want empty after removing the default provider", loaded.LLM.DefaultModel)
	}
}

// TestExplicitAuthTokenNotClobbered verifies that a user-exported
// ANTHROPIC_AUTH_TOKEN survives both the startup export and the on-demand
// refresher hook. applyLLMConfigEnv already deferred to explicit env vars, but
// the hook then overwrote the variable with the config's token on every secret
// resolution — even when nothing was refreshed.
func TestExplicitAuthTokenNotClobbered(t *testing.T) {
	tempDir := t.TempDir()
	origConfigRoot := llmconfig.ConfigRoot
	origConfigFile := llmconfig.ConfigFile
	t.Cleanup(func() {
		llmconfig.ConfigRoot = origConfigRoot
		llmconfig.ConfigFile = origConfigFile
	})
	llmconfig.ConfigRoot = filepath.Join(tempDir, "dagger")
	llmconfig.ConfigFile = filepath.Join(llmconfig.ConfigRoot, llmconfig.ConfigFileName)

	cfg := &llmconfig.Config{
		LLM: llmconfig.LLMConfig{
			DefaultProvider: "anthropic",
			Providers: map[string]llmconfig.Provider{
				"anthropic": {
					AuthType:  "oauth",
					AuthToken: "config-token",
					// Far enough out that nothing tries to refresh it.
					TokenExpiry:  time.Now().Add(time.Hour).UnixMilli(),
					RefreshToken: "rt-0",
					Enabled:      true,
				},
			},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	t.Setenv("ANTHROPIC_AUTH_TOKEN", "user-token")

	applyLLMConfigEnv()
	if got := os.Getenv("ANTHROPIC_AUTH_TOKEN"); got != "user-token" {
		t.Fatalf("after applyLLMConfigEnv, ANTHROPIC_AUTH_TOKEN = %q, want the explicit %q", got, "user-token")
	}

	// Resolve the secret the way the engine does, which runs the refresher
	// hook registered in this package's init.
	resolver, name, err := secretprovider.ResolverForID("env://ANTHROPIC_AUTH_TOKEN")
	if err != nil {
		t.Fatalf("ResolverForID() failed: %v", err)
	}
	val, err := resolver(t.Context(), name)
	if err != nil {
		t.Fatalf("resolving env://ANTHROPIC_AUTH_TOKEN failed: %v", err)
	}
	if string(val) != "user-token" {
		t.Errorf("resolved token = %q, want the explicit %q", val, "user-token")
	}
	if got := os.Getenv("ANTHROPIC_AUTH_TOKEN"); got != "user-token" {
		t.Errorf("refresher hook overwrote ANTHROPIC_AUTH_TOKEN with %q", got)
	}
}

// TestOAuthExpiryEnvExported pins the env contract the engine depends on: the
// access token's true expiry travels next to the token as RFC 3339 UTC, and
// the refresher hook updates both variables whichever one the engine asks for
// — it resolves the token first and the expiry second within one credential
// resolution, so updating only the requested variable would pair a fresh token
// with a stale deadline.
func TestOAuthExpiryEnvExported(t *testing.T) {
	tempDir := t.TempDir()
	origConfigRoot := llmconfig.ConfigRoot
	origConfigFile := llmconfig.ConfigFile
	t.Cleanup(func() {
		llmconfig.ConfigRoot = origConfigRoot
		llmconfig.ConfigFile = origConfigFile
	})
	llmconfig.ConfigRoot = filepath.Join(tempDir, "dagger")
	llmconfig.ConfigFile = filepath.Join(llmconfig.ConfigRoot, llmconfig.ConfigFileName)

	for _, key := range []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN_EXPIRES_AT"} {
		if val, ok := os.LookupEnv(key); ok {
			t.Cleanup(func() { os.Setenv(key, val) })
			os.Unsetenv(key)
		} else {
			t.Cleanup(func() { os.Unsetenv(key) })
		}
	}

	// Truncated to the second: RFC 3339 without sub-second digits is what we
	// promise to write.
	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	save := func(token string, expiresAt time.Time) {
		t.Helper()
		cfg := &llmconfig.Config{
			LLM: llmconfig.LLMConfig{
				DefaultProvider: "anthropic",
				Providers: map[string]llmconfig.Provider{
					"anthropic": {
						AuthType:       "oauth",
						AuthToken:      token,
						RefreshToken:   "rt-0",
						TokenExpiresAt: expiresAt.UnixMilli(),
						Enabled:        true,
					},
				},
			},
		}
		if err := cfg.Save(); err != nil {
			t.Fatalf("Save() failed: %v", err)
		}
	}

	save("config-token", expiresAt)
	applyLLMConfigEnv()

	if got := os.Getenv("ANTHROPIC_AUTH_TOKEN"); got != "config-token" {
		t.Errorf("ANTHROPIC_AUTH_TOKEN = %q, want %q", got, "config-token")
	}
	if got, want := os.Getenv("ANTHROPIC_AUTH_TOKEN_EXPIRES_AT"), expiresAt.Format(time.RFC3339); got != want {
		t.Errorf("ANTHROPIC_AUTH_TOKEN_EXPIRES_AT = %q, want %q", got, want)
	}

	// Another dagger process refreshes the token and rewrites the config.
	rotatedAt := expiresAt.Add(time.Hour)
	save("rotated-token", rotatedAt)

	// The engine asks for the expiry variable; the hook must update both.
	resolver, name, err := secretprovider.ResolverForID("env://ANTHROPIC_AUTH_TOKEN_EXPIRES_AT")
	if err != nil {
		t.Fatalf("ResolverForID() failed: %v", err)
	}
	val, err := resolver(t.Context(), name)
	if err != nil {
		t.Fatalf("resolving env://ANTHROPIC_AUTH_TOKEN_EXPIRES_AT failed: %v", err)
	}
	if want := rotatedAt.Format(time.RFC3339); string(val) != want {
		t.Errorf("resolved expiry = %q, want %q", val, want)
	}
	if got := os.Getenv("ANTHROPIC_AUTH_TOKEN"); got != "rotated-token" {
		t.Errorf("ANTHROPIC_AUTH_TOKEN = %q, want the hook to have updated it to %q", got, "rotated-token")
	}
}

// TestNextOAuthRefreshDelay covers the re-arming rule: the delay is derived
// from the *currently persisted* expiry every cycle, so a token another dagger
// process refreshed is respected, an unknown expiry falls back to a periodic
// check instead of hot-looping, and an overdue token can't spin the loop.
func TestNextOAuthRefreshDelay(t *testing.T) {
	tempDir := t.TempDir()
	origConfigRoot := llmconfig.ConfigRoot
	origConfigFile := llmconfig.ConfigFile
	t.Cleanup(func() {
		llmconfig.ConfigRoot = origConfigRoot
		llmconfig.ConfigFile = origConfigFile
	})
	llmconfig.ConfigRoot = filepath.Join(tempDir, "dagger")
	llmconfig.ConfigFile = filepath.Join(llmconfig.ConfigRoot, llmconfig.ConfigFileName)

	save := func(p llmconfig.Provider) {
		t.Helper()
		cfg := &llmconfig.Config{
			LLM: llmconfig.LLMConfig{
				DefaultProvider: "anthropic",
				Providers:       map[string]llmconfig.Provider{"anthropic": p},
			},
		}
		if err := cfg.Save(); err != nil {
			t.Fatalf("Save() failed: %v", err)
		}
	}
	oauth := func(expiresAt int64) llmconfig.Provider {
		return llmconfig.Provider{
			AuthType:       "oauth",
			AuthToken:      "config-token",
			RefreshToken:   "rt-0",
			TokenExpiresAt: expiresAt,
			Enabled:        true,
		}
	}

	providers := []string{"anthropic"}

	save(oauth(time.Now().Add(time.Hour).UnixMilli()))
	got := nextOAuthRefreshDelay(providers)
	if want := time.Hour - oauthRefreshLead; got > want || got < want-time.Minute {
		t.Errorf("delay for a token expiring in an hour = %v, want about %v", got, want)
	}

	// A refresh elsewhere pushed the expiry out; the next cycle must see it.
	save(oauth(time.Now().Add(3 * time.Hour).UnixMilli()))
	if got := nextOAuthRefreshDelay(providers); got < 2*time.Hour {
		t.Errorf("delay after the config was rewritten = %v, want it re-armed from the new expiry", got)
	}

	save(oauth(0))
	if got := nextOAuthRefreshDelay(providers); got != oauthRefreshUnknownInterval {
		t.Errorf("delay for an unknown expiry = %v, want the periodic %v", got, oauthRefreshUnknownInterval)
	}

	save(oauth(time.Now().Add(-time.Hour).UnixMilli()))
	if got := nextOAuthRefreshDelay(providers); got != oauthRefreshMinDelay {
		t.Errorf("delay for an overdue token = %v, want the floor %v", got, oauthRefreshMinDelay)
	}

	disabled := oauth(time.Now().Add(time.Hour).UnixMilli())
	disabled.Enabled = false
	save(disabled)
	if got := nextOAuthRefreshDelay(providers); got != oauthRefreshUnknownInterval {
		t.Errorf("delay once every provider is disabled = %v, want the periodic %v", got, oauthRefreshUnknownInterval)
	}
	if names := enabledOAuthProviders(); len(names) != 0 {
		t.Errorf("enabledOAuthProviders() = %v, want none once the provider is disabled", names)
	}
}

// TestStartOAuthTokenRefresher exercises the background refresher end to end:
// it starts only when a subscription provider is configured, keeps the
// exported variables in step with the persisted config across cycles (which is
// what makes an idle `dagger shell` survive a token rotation), and stops when
// its context is cancelled.
func TestStartOAuthTokenRefresher(t *testing.T) {
	tempDir := t.TempDir()
	origConfigRoot := llmconfig.ConfigRoot
	origConfigFile := llmconfig.ConfigFile
	t.Cleanup(func() {
		llmconfig.ConfigRoot = origConfigRoot
		llmconfig.ConfigFile = origConfigFile
	})
	llmconfig.ConfigRoot = filepath.Join(tempDir, "dagger")
	llmconfig.ConfigFile = filepath.Join(llmconfig.ConfigRoot, llmconfig.ConfigFileName)

	for _, key := range []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN_EXPIRES_AT"} {
		if val, ok := os.LookupEnv(key); ok {
			t.Cleanup(func() { os.Setenv(key, val) })
			os.Unsetenv(key)
		} else {
			t.Cleanup(func() { os.Unsetenv(key) })
		}
	}

	// No config at all: nothing to keep fresh, so nothing starts.
	stop := startOAuthTokenRefresher(t.Context())
	stop()

	save := func(token string) {
		t.Helper()
		cfg := &llmconfig.Config{
			LLM: llmconfig.LLMConfig{
				DefaultProvider: "anthropic",
				Providers: map[string]llmconfig.Provider{
					"anthropic": {
						AuthType:     "oauth",
						AuthToken:    token,
						RefreshToken: "rt-0",
						// Unknown expiry: the loop polls on its fallback
						// interval and never needs the token endpoint.
						Enabled: true,
					},
				},
			},
		}
		if err := cfg.Save(); err != nil {
			t.Fatalf("Save() failed: %v", err)
		}
	}

	origInterval, origMin := oauthRefreshUnknownInterval, oauthRefreshMinDelay
	t.Cleanup(func() {
		oauthRefreshUnknownInterval, oauthRefreshMinDelay = origInterval, origMin
	})
	oauthRefreshUnknownInterval = 5 * time.Millisecond
	oauthRefreshMinDelay = time.Millisecond

	save("first-token")
	stop = startOAuthTokenRefresher(t.Context())
	t.Cleanup(stop)

	waitForEnv := func(key, want string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if os.Getenv(key) == want {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("%s = %q, want %q", key, os.Getenv(key), want)
	}
	waitForEnv("ANTHROPIC_AUTH_TOKEN", "first-token")

	// Another dagger process rotates the token; the refresher picks it up on
	// its next cycle without being told.
	save("second-token")
	waitForEnv("ANTHROPIC_AUTH_TOKEN", "second-token")

	stop()
	save("third-token")
	time.Sleep(50 * time.Millisecond)
	if got := os.Getenv("ANTHROPIC_AUTH_TOKEN"); got != "second-token" {
		t.Errorf("ANTHROPIC_AUTH_TOKEN = %q after stop, want the refresher to have stopped at %q", got, "second-token")
	}
}

// TestApplyLLMConfigEnvKeepsDefaultModel guards the interaction between the
// default-model export and the provider loop: the provider's own model field
// is usually empty, and exporting "nothing" for it must not undo the default
// model exported moments earlier.
func TestApplyLLMConfigEnvKeepsDefaultModel(t *testing.T) {
	tempDir := t.TempDir()
	origConfigRoot := llmconfig.ConfigRoot
	origConfigFile := llmconfig.ConfigFile
	t.Cleanup(func() {
		llmconfig.ConfigRoot = origConfigRoot
		llmconfig.ConfigFile = origConfigFile
	})
	llmconfig.ConfigRoot = filepath.Join(tempDir, "dagger")
	llmconfig.ConfigFile = filepath.Join(llmconfig.ConfigRoot, llmconfig.ConfigFileName)

	for _, key := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_API_KEY"} {
		if val, ok := os.LookupEnv(key); ok {
			t.Cleanup(func() { os.Setenv(key, val) })
			os.Unsetenv(key)
		} else {
			t.Cleanup(func() { os.Unsetenv(key) })
		}
	}

	cfg := &llmconfig.Config{
		LLM: llmconfig.LLMConfig{
			DefaultProvider: "anthropic",
			DefaultModel:    "claude-sonnet-4.5",
			Providers: map[string]llmconfig.Provider{
				// No Model of its own, which is the common case.
				"anthropic": {APIKey: "sk-ant", Enabled: true},
			},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	applyLLMConfigEnv()

	if got := os.Getenv("ANTHROPIC_MODEL"); got != "claude-sonnet-4.5" {
		t.Errorf("ANTHROPIC_MODEL = %q, want the configured default %q", got, "claude-sonnet-4.5")
	}
}

// TestApplyLLMConfigEnvOpenAISlot verifies that when both openai and
// openrouter are enabled, the shared OPENAI_* variables are owned by exactly
// one provider, chosen deterministically rather than by map iteration order.
func TestApplyLLMConfigEnvOpenAISlot(t *testing.T) {
	for _, tc := range []struct {
		name            string
		defaultProvider string
		wantKey         string
		wantBaseURL     string
	}{
		{
			name:            "default openrouter wins the slot",
			defaultProvider: "openrouter",
			wantKey:         "sk-openrouter",
			wantBaseURL:     "https://openrouter.ai/api/v1",
		},
		{
			name:            "openai wins when default is neither",
			defaultProvider: "anthropic",
			wantKey:         "sk-openai",
			wantBaseURL:     "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			origConfigRoot := llmconfig.ConfigRoot
			origConfigFile := llmconfig.ConfigFile
			t.Cleanup(func() {
				llmconfig.ConfigRoot = origConfigRoot
				llmconfig.ConfigFile = origConfigFile
			})
			llmconfig.ConfigRoot = filepath.Join(tempDir, "dagger")
			llmconfig.ConfigFile = filepath.Join(llmconfig.ConfigRoot, llmconfig.ConfigFileName)

			for _, key := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL", "OPENAI_MODEL", "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL"} {
				if val, ok := os.LookupEnv(key); ok {
					t.Cleanup(func() { os.Setenv(key, val) })
					os.Unsetenv(key)
				} else {
					t.Cleanup(func() { os.Unsetenv(key) })
				}
			}

			cfg := &llmconfig.Config{
				LLM: llmconfig.LLMConfig{
					DefaultProvider: tc.defaultProvider,
					Providers: map[string]llmconfig.Provider{
						"openai":     {APIKey: "sk-openai", Enabled: true},
						"openrouter": {APIKey: "sk-openrouter", Enabled: true},
						"anthropic":  {APIKey: "sk-anthropic", Enabled: true},
					},
				},
			}
			if err := cfg.Save(); err != nil {
				t.Fatalf("Save() failed: %v", err)
			}

			applyLLMConfigEnv()

			if got := os.Getenv("OPENAI_API_KEY"); got != tc.wantKey {
				t.Errorf("OPENAI_API_KEY = %q, want %q", got, tc.wantKey)
			}
			if got := os.Getenv("OPENAI_BASE_URL"); got != tc.wantBaseURL {
				t.Errorf("OPENAI_BASE_URL = %q, want %q", got, tc.wantBaseURL)
			}
		})
	}
}
