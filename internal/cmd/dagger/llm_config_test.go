package daggercmd

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/client/secretprovider"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

// useTempLLMConfig points the config file at a fresh temp directory and seeds
// it with cfg, if any.
func useTempLLMConfig(t *testing.T, cfg *llmconfig.Config) {
	t.Helper()
	origRoot, origFile := llmconfig.ConfigRoot, llmconfig.ConfigFile
	t.Cleanup(func() { llmconfig.ConfigRoot, llmconfig.ConfigFile = origRoot, origFile })
	llmconfig.ConfigRoot = filepath.Join(t.TempDir(), "dagger")
	llmconfig.ConfigFile = filepath.Join(llmconfig.ConfigRoot, llmconfig.ConfigFileName)
	if cfg != nil {
		if err := cfg.Save(); err != nil {
			t.Fatalf("Save() failed: %v", err)
		}
	}
}

// clearLLMEnv blanks the LLM environment for the test (empty counts as
// unset) and runs it from an empty directory, so neither the host's
// variables nor a stray ./.env leak into the assembled config.
func clearLLMEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN_EXPIRES_AT",
		"ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL", "ANTHROPIC_SMALL_MODEL",
		"ANTHROPIC_REASONING_EFFORT", "ANTHROPIC_CLAUDE_CODE_VERSION",
		"OPENAI_API_KEY", "OPENAI_BASE_URL", "OPENAI_MODEL", "OPENAI_SMALL_MODEL",
		"OPENAI_AZURE_VERSION", "OPENAI_DISABLE_STREAMING",
		"OPENAI_CODEX_AUTH_TOKEN", "OPENAI_CODEX_AUTH_TOKEN_EXPIRES_AT",
		"OPENAI_CODEX_MODEL", "OPENAI_CODEX_SMALL_MODEL", "OPENAI_CODEX_REASONING_EFFORT",
		"GEMINI_API_KEY", "GEMINI_BASE_URL", "GEMINI_MODEL", "GEMINI_SMALL_MODEL",
		"GEMINI_REASONING_EFFORT",
		"LOCAL_BASE_URL", "LOCAL_MODEL", "LOCAL_SMALL_MODEL", "LOCAL_API_COMPAT", "LOCAL_API_KEY",
	} {
		t.Setenv(name, "")
	}
	t.Chdir(t.TempDir())
}

// anthropicOAuthServer stands in for Anthropic's token endpoint on
// http.DefaultTransport. It accepts the refresh token "rt-0" exactly once and
// counts the grants.
func anthropicOAuthServer(t *testing.T) *atomic.Int32 {
	t.Helper()
	var grants atomic.Int32
	origTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = origTransport })
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body string
		switch req.URL.Path {
		case "/v1/oauth/token":
			var grant struct {
				RefreshToken string `json:"refresh_token"`
			}
			if err := json.NewDecoder(req.Body).Decode(&grant); err != nil {
				return nil, err
			}
			if grant.RefreshToken != "rt-0" {
				return nil, errors.New("refresh grant was spent more than once")
			}
			grants.Add(1)
			body = `{"access_token":"refreshed-token","refresh_token":"rt-1","expires_in":3600}`
		case "/api/oauth/profile":
			body = `{}`
		default:
			return nil, fmt.Errorf("unexpected OAuth request path: %s", req.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	return &grants
}

// Sending the config must not contact the provider: an expired login is
// refreshed when the engine resolves the credential, so a command that never
// uses an LLM pays nothing.
func TestLLMConfigAssemblyDoesNotRefreshOAuth(t *testing.T) {
	clearLLMEnv(t)
	useTempLLMConfig(t, &llmconfig.Config{LLM: llmconfig.LLMConfig{
		Providers: map[string]llmconfig.Provider{"openai-codex": {
			AuthType: "oauth", AuthToken: "persisted-token", RefreshToken: "refresh",
			TokenExpiresAt: time.Now().Add(-time.Hour).UnixMilli(), Enabled: true,
		}},
	}})
	origTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = origTransport })
	var requests atomic.Int32
	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("oauth endpoint unavailable")
	})

	var params client.Params
	require.NoError(t, applyWorkspaceClientParams(&params))
	require.Zero(t, requests.Load(), "assembly made OAuth requests")
	require.NotNil(t, params.LLMConfig)
	codex := params.LLMConfig.Providers["openai-codex"]
	require.Equal(t, "llmconfig://openai-codex/auth_token", codex.AuthToken)
	require.Equal(t, "llmconfig://openai-codex/auth_token_expires_at", codex.AuthTokenExpiresAt)
	for _, name := range []string{"OPENAI_CODEX_AUTH_TOKEN", "OPENAI_CODEX_AUTH_TOKEN_EXPIRES_AT"} {
		require.Empty(t, os.Getenv(name), "%s was exported", name)
	}
}

// applyWorkspaceClientParams sends the config assembled from ./.env in the
// working directory, and refuses a malformed value rather than silently
// dropping it.
func TestApplyWorkspaceClientParamsSendsLLMConfig(t *testing.T) {
	clearLLMEnv(t)
	useTempLLMConfig(t, nil)
	require.NoError(t, os.WriteFile(".env", []byte("OPENAI_MODEL=gpt-dotenv\n"), 0o600))
	t.Setenv("OPENAI_API_KEY", "sk-env")

	var params client.Params
	require.NoError(t, applyWorkspaceClientParams(&params))
	require.NotNil(t, params.LLMConfig)
	require.Equal(t, "openai", params.LLMConfig.DefaultProvider)
	require.Equal(t, "gpt-dotenv", params.LLMConfig.DefaultModel)
	require.Equal(t, "env://OPENAI_API_KEY", params.LLMConfig.Providers["openai"].APIKey)

	t.Setenv("OPENAI_DISABLE_STREAMING", "maybe")
	params = client.Params{}
	require.ErrorIs(t, applyWorkspaceClientParams(&params), llmconfig.ErrMalformed)

	// Nothing configured: nothing sent.
	t.Setenv("OPENAI_DISABLE_STREAMING", "")
	t.Setenv("OPENAI_API_KEY", "")
	require.NoError(t, os.Remove(".env"))
	params = client.Params{}
	require.NoError(t, applyWorkspaceClientParams(&params))
	require.Nil(t, params.LLMConfig)
}

// TestRemoveKeyClearsDefaultModel verifies that removing the default provider
// also clears the default model. Otherwise the stale model stays bound to
// whatever provider becomes default next and breaks every LLM call.
func TestRemoveKeyClearsDefaultModel(t *testing.T) {
	useTempLLMConfig(t, &llmconfig.Config{
		LLM: llmconfig.LLMConfig{
			DefaultProvider: "anthropic",
			DefaultModel:    "claude-sonnet-4.5",
			Providers: map[string]llmconfig.Provider{
				"anthropic": {APIKey: "sk-ant", Enabled: true},
			},
		},
	})

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

// The llmconfig:// resolver is registered for the whole CLI. A login the
// engine reports rejected is refreshed once, through the scheme, while an
// explicitly exported token travels as env:// and never reaches the resolver,
// so its rejection cannot rotate an unrelated subscription login.
func TestOAuthRejectionRefreshesManagedCredential(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit=%t", explicit), func(t *testing.T) {
			clearLLMEnv(t)
			grants := anthropicOAuthServer(t)
			useTempLLMConfig(t, &llmconfig.Config{LLM: llmconfig.LLMConfig{Providers: map[string]llmconfig.Provider{
				"anthropic": {
					AuthType: "oauth", AuthToken: "rejected-token", RefreshToken: "rt-0",
					TokenExpiresAt: time.Now().Add(time.Hour).UnixMilli(), Enabled: true,
				},
			}}})
			if explicit {
				t.Setenv("ANTHROPIC_AUTH_TOKEN", "user-override")
			}

			var params client.Params
			require.NoError(t, applyWorkspaceClientParams(&params))
			require.Zero(t, grants.Load(), "unexpired credential was refreshed at startup")
			tokenURI := params.LLMConfig.Providers["anthropic"].AuthToken
			want := "refreshed-token"
			wantURI := "llmconfig://anthropic/auth_token"
			if explicit {
				want, wantURI = "user-override", "env://ANTHROPIC_AUTH_TOKEN"
			}
			require.Equal(t, wantURI, tokenURI)

			// Resolve the credential the way the engine does, reporting the
			// value it last saw as rejected.
			current := "rejected-token"
			if explicit {
				current = "user-override"
			}
			rejected := fmt.Sprintf("%x", sha256.Sum256([]byte(current)))
			ctx := secretprovider.ContextWithRejectedSecretValue(t.Context(), rejected)
			resolve, path, err := secretprovider.ResolverForID(tokenURI)
			require.NoError(t, err)
			for range 2 {
				got, err := resolve(ctx, path)
				require.NoError(t, err)
				require.Equal(t, want, string(got))
			}
			wantGrants := int32(1)
			if explicit {
				wantGrants = 0
			}
			require.Equal(t, wantGrants, grants.Load())
		})
	}
}

// TestOAuthExpiryServedAfterRotation pins the contract the engine depends
// on: the access token's true expiry is served next to the token as RFC 3339
// UTC, re-read at every resolution, so a token another dagger process rotated
// is paired with its own expiry, never a stale one.
func TestOAuthExpiryServedAfterRotation(t *testing.T) {
	// Truncated to the second: RFC 3339 without sub-second digits is what we
	// promise to write.
	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	save := func(token string, expiresAt time.Time) {
		t.Helper()
		cfg := &llmconfig.Config{LLM: llmconfig.LLMConfig{
			DefaultProvider: "anthropic",
			Providers: map[string]llmconfig.Provider{"anthropic": {
				AuthType: "oauth", AuthToken: token, RefreshToken: "rt-0",
				TokenExpiresAt: expiresAt.UnixMilli(), Enabled: true,
			}},
		}}
		if err := cfg.Save(); err != nil {
			t.Fatalf("Save() failed: %v", err)
		}
	}
	resolve := func(uri string) string {
		t.Helper()
		r, path, err := secretprovider.ResolverForID(uri)
		require.NoError(t, err)
		v, err := r(t.Context(), path)
		require.NoError(t, err)
		return string(v)
	}

	useTempLLMConfig(t, nil)
	save("config-token", expiresAt)
	require.Equal(t, "config-token", resolve("llmconfig://anthropic/auth_token"))
	require.Equal(t, expiresAt.Format(time.RFC3339), resolve("llmconfig://anthropic/auth_token_expires_at"))

	// Another dagger process refreshes the token and rewrites the config.
	rotatedAt := expiresAt.Add(time.Hour)
	save("rotated-token", rotatedAt)
	require.Equal(t, "rotated-token", resolve("llmconfig://anthropic/auth_token"))
	require.Equal(t, rotatedAt.Format(time.RFC3339), resolve("llmconfig://anthropic/auth_token_expires_at"))

	// Unknown expiry is served as empty, which the engine reads as unknown.
	save("rotated-token", time.Time{})
	require.Empty(t, resolve("llmconfig://anthropic/auth_token_expires_at"))
}

// TestNextOAuthRefreshDelay covers the re-arming rule: the delay is derived
// from the *currently persisted* expiry every cycle, so a token another dagger
// process refreshed is respected, an unknown expiry falls back to a periodic
// check instead of hot-looping, and an overdue token can't spin the loop.
func TestNextOAuthRefreshDelay(t *testing.T) {
	useTempLLMConfig(t, nil)

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
// it starts only when a subscription provider is configured, refreshes an
// overdue login and persists the rotated token (which is what the engine's
// next llmconfig:// lookup reads), and stops when told to.
func TestStartOAuthTokenRefresher(t *testing.T) {
	useTempLLMConfig(t, nil)

	// No config at all: nothing to keep fresh, so nothing starts.
	stop := startOAuthTokenRefresher(t.Context())
	stop()

	grants := anthropicOAuthServer(t)
	cfg := &llmconfig.Config{LLM: llmconfig.LLMConfig{
		DefaultProvider: "anthropic",
		Providers: map[string]llmconfig.Provider{"anthropic": {
			AuthType: "oauth", AuthToken: "overdue-token", RefreshToken: "rt-0",
			TokenExpiresAt: time.Now().Add(-time.Hour).UnixMilli(), Enabled: true,
		}},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	origInterval, origMin := oauthRefreshUnknownInterval, oauthRefreshMinDelay
	t.Cleanup(func() {
		oauthRefreshUnknownInterval, oauthRefreshMinDelay = origInterval, origMin
	})
	oauthRefreshUnknownInterval = 5 * time.Millisecond
	oauthRefreshMinDelay = time.Millisecond

	stop = startOAuthTokenRefresher(t.Context())
	t.Cleanup(stop)

	deadline := time.Now().Add(10 * time.Second)
	for grants.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	stop()
	require.Equal(t, int32(1), grants.Load())
	loaded, err := llmconfig.Load()
	require.NoError(t, err)
	require.Equal(t, "refreshed-token", loaded.LLM.Providers["anthropic"].AuthToken)
	for _, name := range []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN_EXPIRES_AT"} {
		require.Empty(t, os.Getenv(name), "%s was exported", name)
	}
}

func TestOAuthTokenRefresherErrorScheduling(t *testing.T) {
	useTempLLMConfig(t, &llmconfig.Config{
		LLM: llmconfig.LLMConfig{
			DefaultProvider: "anthropic",
			Providers: map[string]llmconfig.Provider{
				"anthropic": {
					AuthType:       "oauth",
					AuthToken:      "stale-token",
					RefreshToken:   "invalid-refresh-token",
					TokenExpiresAt: time.Now().Add(-time.Hour).UnixMilli(),
					Enabled:        true,
				},
			},
		},
	})

	origRefresh := backgroundOAuthRefresh
	origInterval, origMin := oauthRefreshUnknownInterval, oauthRefreshMinDelay
	t.Cleanup(func() {
		backgroundOAuthRefresh = origRefresh
		oauthRefreshUnknownInterval, oauthRefreshMinDelay = origInterval, origMin
	})
	oauthRefreshUnknownInterval = time.Millisecond
	oauthRefreshMinDelay = time.Millisecond

	var calls atomic.Int32
	backgroundOAuthRefresh = func(context.Context, string) error {
		calls.Add(1)
		return llmconfig.ErrOAuthReauthenticationRequired
	}

	stop := startOAuthTokenRefresher(t.Context())
	t.Cleanup(stop)
	deadline := time.Now().Add(10 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("background refresh calls = %d, want 1", got)
	}

	// An overdue token normally re-arms at oauthRefreshMinDelay. A terminal
	// failure must retire it from the background scheduler instead.
	time.Sleep(20 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Errorf("background refresh calls after terminal error = %d, want 1", got)
	}
	stop()

	// A transient failure stays scheduled and is attempted again on the next
	// minimum-delay tick.
	calls.Store(0)
	backgroundOAuthRefresh = func(context.Context, string) error {
		calls.Add(1)
		return errors.New("temporary refresh failure")
	}
	stopTransient := startOAuthTokenRefresher(t.Context())
	t.Cleanup(stopTransient)
	deadline = time.Now().Add(10 * time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	stopTransient()
	if got := calls.Load(); got < 2 {
		t.Errorf("background transient refresh calls = %d, want at least 2", got)
	}
}
