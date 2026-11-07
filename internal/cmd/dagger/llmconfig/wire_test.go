package llmconfig

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/secretprovider"
	"github.com/dagger/dagger/internal/buildkit/session/secrets"
)

// mapEnv is a process environment for assemble.
func mapEnv(env map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}
}

func fileConfig(defaultProvider, defaultModel string, providers map[string]Provider) *Config {
	return &Config{LLM: LLMConfig{
		DefaultProvider: defaultProvider,
		DefaultModel:    defaultModel,
		Providers:       providers,
	}}
}

func TestAssembleFromFile(t *testing.T) {
	file := fileConfig("google", "gemini-2.5-pro", map[string]Provider{
		"anthropic": {
			AuthType: "oauth", AuthToken: "oauth-token", RefreshToken: "rt",
			SmallModel: "claude-haiku", ReasoningEffort: "high",
			ClaudeCodeVersion: "2.1.0", Enabled: true,
		},
		"openai-codex": {AuthType: "oauth", AuthToken: "codex-token", Model: "gpt-5.5", Enabled: true},
		"openrouter":   {APIKey: "or-key", Model: "anthropic/claude-sonnet-4.5", Enabled: true},
		"google":       {APIKey: "gem-key", BaseURL: "https://gemini.example", Enabled: true},
		"local":        {BaseURL: "http://localhost:11434/v1", APICompat: "openai", APIKey: "local-key", Model: "llama", Enabled: true},
		"openai":       {APIKey: "disabled-key", Model: "gpt-4.1", Enabled: false},
		"gemini":       {APIKey: "not-a-provider", Enabled: true},
	})
	cfg, literals, err := assemble(file, mapEnv(nil), nil)
	require.NoError(t, err)
	require.Empty(t, literals)

	require.Equal(t, &engine.LLMConfig{
		DefaultProvider: "google",
		DefaultModel:    "gemini-2.5-pro",
		Providers: map[string]*engine.LLMProviderConfig{
			"anthropic": {
				AuthToken:          "llmconfig://anthropic/auth_token",
				AuthTokenExpiresAt: "llmconfig://anthropic/auth_token_expires_at",
				SmallModel:         "claude-haiku",
				ReasoningEffort:    "high",
				ClaudeCodeVersion:  "2.1.0",
			},
			"openai-codex": {
				AuthToken:          "llmconfig://openai-codex/auth_token",
				AuthTokenExpiresAt: "llmconfig://openai-codex/auth_token_expires_at",
				Model:              "gpt-5.5",
			},
			"openrouter": {
				APIKey: "llmconfig://openrouter/api_key",
				Model:  "anthropic/claude-sonnet-4.5",
			},
			"google": {
				APIKey:  "llmconfig://google/api_key",
				BaseURL: "https://gemini.example",
			},
			"local": {
				APIKey:    "llmconfig://local/api_key",
				BaseURL:   "http://localhost:11434/v1",
				APICompat: "openai",
				Model:     "llama",
			},
		},
	}, cfg)
}

func TestAssembleNothingConfigured(t *testing.T) {
	cfg, _, err := assemble(nil, mapEnv(map[string]string{"UNRELATED": "x", "OPENAI_API_KEY": ""}), nil)
	require.NoError(t, err)
	require.Nil(t, cfg)

	// A disabled default contributes nothing, its model included.
	cfg, _, err = assemble(fileConfig("anthropic", "claude", map[string]Provider{
		"anthropic": {APIKey: "k", Enabled: false},
	}), mapEnv(nil), nil)
	require.NoError(t, err)
	require.Nil(t, cfg)
}

// openai and openrouter are distinct providers with their own credentials;
// both travel, and the resolver serves each one's own key.
func TestAssembleOpenAIAndOpenRouterCoexist(t *testing.T) {
	file := fileConfig("openrouter", "anthropic/claude-sonnet-4.5", map[string]Provider{
		"openai":     {APIKey: "sk-openai", Model: "gpt-openai", Enabled: true},
		"openrouter": {APIKey: "sk-openrouter", Model: "gpt-openrouter", Enabled: true},
	})
	cfg, _, err := assemble(file, mapEnv(map[string]string{
		"OPENROUTER_BASE_URL": "https://proxy.example/v1",
	}), nil)
	require.NoError(t, err)
	require.Equal(t, "openrouter", cfg.DefaultProvider)
	require.Equal(t, &engine.LLMProviderConfig{APIKey: "llmconfig://openai/api_key", Model: "gpt-openai"}, cfg.Providers["openai"])
	require.Equal(t, &engine.LLMProviderConfig{
		APIKey:  "llmconfig://openrouter/api_key",
		Model:   "gpt-openrouter",
		BaseURL: "https://proxy.example/v1",
	}, cfg.Providers["openrouter"])

	useTempConfig(t, file)
	for _, tc := range []struct{ path, want string }{
		{"openai/api_key", "sk-openai"},
		{"openrouter/api_key", "sk-openrouter"},
	} {
		got, err := ResolveSecret(t.Context(), tc.path)
		require.NoError(t, err)
		require.Equal(t, tc.want, string(got))
	}
}

// Precedence is ./.env over the process environment over the config file,
// field by field.
func TestAssemblePrecedence(t *testing.T) {
	file := fileConfig("", "", map[string]Provider{
		"anthropic": {APIKey: "file-key", BaseURL: "https://file.example", SmallModel: "file-small", Enabled: true},
	})
	env := map[string]string{
		"ANTHROPIC_API_KEY":     "env-key",
		"ANTHROPIC_SMALL_MODEL": "env-small",
		"ANTHROPIC_MODEL":       "env-model",
	}

	cfg, literals, err := assemble(file, mapEnv(env), nil)
	require.NoError(t, err)
	anthropic := cfg.Providers["anthropic"]
	require.Equal(t, "env://ANTHROPIC_API_KEY", anthropic.APIKey)
	require.Equal(t, "https://file.example", anthropic.BaseURL)
	require.Equal(t, "env-small", anthropic.SmallModel)
	require.Equal(t, "env-model", anthropic.Model)
	require.Empty(t, literals)

	dotenv := map[string]string{
		"ANTHROPIC_API_KEY": "dotenv-key",
		"ANTHROPIC_MODEL":   "dotenv-model",
	}
	cfg, literals, err = assemble(file, mapEnv(env), dotenv)
	require.NoError(t, err)
	anthropic = cfg.Providers["anthropic"]
	require.Equal(t, "llmconfig://anthropic/api_key", anthropic.APIKey)
	require.Equal(t, "dotenv-model", anthropic.Model)
	require.Equal(t, "env-small", anthropic.SmallModel)
	require.Equal(t, map[string]string{"anthropic/api_key": "dotenv-key"}, literals)
}

// A credential whose value is itself a secret reference travels verbatim from
// any source; the engine resolves it against this client as before.
func TestAssembleSecretURIPassthrough(t *testing.T) {
	file := fileConfig("", "", map[string]Provider{
		"anthropic": {APIKey: "vault://secret/anthropic", Enabled: true},
	})
	env := map[string]string{
		"OPENAI_API_KEY": "op://vault/openai/credential",
		// A base URL is routing config, not a credential: never a URI.
		"OPENAI_BASE_URL": "https://proxy.example/v1",
	}
	dotenv := map[string]string{
		"GEMINI_API_KEY": "env://MY_GEMINI_KEY",
		"LOCAL_API_KEY":  "local-literal",
	}
	cfg, literals, err := assemble(file, mapEnv(env), dotenv)
	require.NoError(t, err)
	require.Equal(t, "vault://secret/anthropic", cfg.Providers["anthropic"].APIKey)
	require.Equal(t, "op://vault/openai/credential", cfg.Providers["openai"].APIKey)
	require.Equal(t, "https://proxy.example/v1", cfg.Providers["openai"].BaseURL)
	require.Equal(t, "env://MY_GEMINI_KEY", cfg.Providers["google"].APIKey)
	require.Equal(t, "llmconfig://local/api_key", cfg.Providers["local"].APIKey)
	require.Equal(t, map[string]string{"local/api_key": "local-literal"}, literals)

	require.False(t, IsSecretURI("https://example.com"))
	require.False(t, IsSecretURI("sk-plain"))
	require.True(t, IsSecretURI("llmconfig://anthropic/api_key"))
}

// An Anthropic API key and OAuth token are alternatives: whichever the
// highest-precedence source supplies wins, and the other is dropped.
func TestAssembleAnthropicCredentialAlternatives(t *testing.T) {
	oauthFile := fileConfig("anthropic", "", map[string]Provider{
		"anthropic": {AuthType: "oauth", AuthToken: "oauth-token", Enabled: true},
	})
	keyFile := fileConfig("anthropic", "", map[string]Provider{
		"anthropic": {APIKey: "file-key", Enabled: true},
	})

	t.Run("env key over file login", func(t *testing.T) {
		cfg, _, err := assemble(oauthFile, mapEnv(map[string]string{"ANTHROPIC_API_KEY": "k"}), nil)
		require.NoError(t, err)
		p := cfg.Providers["anthropic"]
		require.Equal(t, "env://ANTHROPIC_API_KEY", p.APIKey)
		require.Empty(t, p.AuthToken)
		require.Empty(t, p.AuthTokenExpiresAt)
	})
	t.Run("env token over file key", func(t *testing.T) {
		cfg, _, err := assemble(keyFile, mapEnv(map[string]string{"ANTHROPIC_AUTH_TOKEN": "t"}), nil)
		require.NoError(t, err)
		p := cfg.Providers["anthropic"]
		require.Empty(t, p.APIKey)
		require.Equal(t, "env://ANTHROPIC_AUTH_TOKEN", p.AuthToken)
		// No expiry exported: none is sent.
		require.Empty(t, p.AuthTokenExpiresAt)
	})
	t.Run("env token with expiry over file login", func(t *testing.T) {
		cfg, _, err := assemble(oauthFile, mapEnv(map[string]string{
			"ANTHROPIC_AUTH_TOKEN":            "t",
			"ANTHROPIC_AUTH_TOKEN_EXPIRES_AT": "2030-01-01T00:00:00Z",
		}), nil)
		require.NoError(t, err)
		p := cfg.Providers["anthropic"]
		require.Equal(t, "env://ANTHROPIC_AUTH_TOKEN", p.AuthToken)
		require.Equal(t, "env://ANTHROPIC_AUTH_TOKEN_EXPIRES_AT", p.AuthTokenExpiresAt)
	})
	t.Run("codex env token", func(t *testing.T) {
		cfg, _, err := assemble(nil, mapEnv(map[string]string{
			"OPENAI_CODEX_AUTH_TOKEN":            "t",
			"OPENAI_CODEX_AUTH_TOKEN_EXPIRES_AT": "2030-01-01T00:00:00Z",
		}), nil)
		require.NoError(t, err)
		p := cfg.Providers["openai-codex"]
		require.Equal(t, "env://OPENAI_CODEX_AUTH_TOKEN", p.AuthToken)
		require.Equal(t, "env://OPENAI_CODEX_AUTH_TOKEN_EXPIRES_AT", p.AuthTokenExpiresAt)
	})
	t.Run("dotenv expiry without token", func(t *testing.T) {
		// An expiry in .env describes no token .env supplies: it must not be
		// served as the expiry of the file's login.
		cfg, literals, err := assemble(oauthFile, mapEnv(nil), map[string]string{
			"ANTHROPIC_AUTH_TOKEN_EXPIRES_AT": "2000-01-01T00:00:00Z",
		})
		require.NoError(t, err)
		p := cfg.Providers["anthropic"]
		require.Equal(t, "llmconfig://anthropic/auth_token", p.AuthToken)
		require.Equal(t, "llmconfig://anthropic/auth_token_expires_at", p.AuthTokenExpiresAt)
		require.Empty(t, literals)
	})
	t.Run("dotenv token", func(t *testing.T) {
		cfg, literals, err := assemble(oauthFile, mapEnv(nil), map[string]string{
			"ANTHROPIC_AUTH_TOKEN":            "dotenv-token",
			"ANTHROPIC_AUTH_TOKEN_EXPIRES_AT": "2030-01-01T00:00:00Z",
		})
		require.NoError(t, err)
		p := cfg.Providers["anthropic"]
		require.Equal(t, "llmconfig://anthropic/auth_token", p.AuthToken)
		require.Equal(t, "llmconfig://anthropic/auth_token_expires_at", p.AuthTokenExpiresAt)
		require.Equal(t, map[string]string{
			"anthropic/auth_token":            "dotenv-token",
			"anthropic/auth_token_expires_at": "2030-01-01T00:00:00Z",
		}, literals)
	})
}

// An explicitly configured model in the environment decides the default
// route, in the engine's legacy priority order; otherwise the file's default
// stands.
func TestAssembleDefaultRoute(t *testing.T) {
	file := fileConfig("anthropic", "claude-file", map[string]Provider{
		"anthropic": {APIKey: "k", Enabled: true},
	})
	for _, tc := range []struct {
		name         string
		env, dotenv  map[string]string
		wantProvider string
		wantModel    string
	}{
		{name: "file default", wantProvider: "anthropic", wantModel: "claude-file"},
		{
			name:         "env model beats file default",
			env:          map[string]string{"GEMINI_MODEL": "gemini-env"},
			wantProvider: "google", wantModel: "gemini-env",
		},
		{
			name:         "legacy priority",
			env:          map[string]string{"GEMINI_MODEL": "gemini-env", "ANTHROPIC_MODEL": "claude-env", "LOCAL_MODEL": "llama"},
			dotenv:       map[string]string{"OPENAI_CODEX_MODEL": "gpt-codex"},
			wantProvider: "openai-codex", wantModel: "gpt-codex",
		},
		{
			name:         "openai first",
			env:          map[string]string{"OPENAI_MODEL": "gpt-env", "OPENAI_CODEX_MODEL": "gpt-codex"},
			wantProvider: "openai", wantModel: "gpt-env",
		},
		{
			name:         "dotenv beats env for the same variable",
			env:          map[string]string{"ANTHROPIC_MODEL": "claude-env"},
			dotenv:       map[string]string{"ANTHROPIC_MODEL": "claude-dotenv"},
			wantProvider: "anthropic", wantModel: "claude-dotenv",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _, err := assemble(file, mapEnv(tc.env), tc.dotenv)
			require.NoError(t, err)
			require.Equal(t, tc.wantProvider, cfg.DefaultProvider)
			require.Equal(t, tc.wantModel, cfg.DefaultModel)
		})
	}
}

func TestAssembleDisableStreaming(t *testing.T) {
	streamingFile := fileConfig("", "", map[string]Provider{
		"openai": {APIKey: "k", DisableStreaming: true, Enabled: true},
	})
	for _, tc := range []struct {
		name        string
		file        *Config
		env, dotenv map[string]string
		want        *bool
	}{
		{name: "unset", want: nil},
		{name: "env", env: map[string]string{"OPENAI_DISABLE_STREAMING": "true"}, want: new(true)},
		{name: "file", file: streamingFile, want: new(true)},
		{name: "env false over file", file: streamingFile, env: map[string]string{"OPENAI_DISABLE_STREAMING": "false"}, want: new(false)},
		// An explicit false travels on its own, to override a true from the
		// session's main client when this one is nested.
		{name: "env false alone", env: map[string]string{"OPENAI_DISABLE_STREAMING": "false"}, want: new(false)},
		{
			name: "dotenv over env",
			env:  map[string]string{"OPENAI_API_KEY": "k", "OPENAI_DISABLE_STREAMING": "1"},
			dotenv: map[string]string{
				"OPENAI_DISABLE_STREAMING": "0",
			},
			want: new(false),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _, err := assemble(tc.file, mapEnv(tc.env), tc.dotenv)
			require.NoError(t, err)
			var got *bool
			if cfg != nil && cfg.Providers["openai"] != nil {
				got = cfg.Providers["openai"].DisableStreaming
			}
			require.Equal(t, tc.want, got)
		})
	}

	_, _, err := assemble(nil, mapEnv(map[string]string{"OPENAI_DISABLE_STREAMING": "sometimes"}), nil)
	require.ErrorIs(t, err, ErrMalformed)
	require.ErrorContains(t, err, "OPENAI_DISABLE_STREAMING")
}

// clearLLMEnv blanks every variable of the environment table for the test.
// Empty counts as unset.
func clearLLMEnv(t *testing.T) {
	t.Helper()
	for _, ev := range envVars {
		t.Setenv(ev.name, "")
	}
	t.Setenv(disableStreamingVar, "")
}

func TestAssembleReadsDotEnvInCwd(t *testing.T) {
	clearLLMEnv(t)
	useTempConfig(t, fileConfig("", "", map[string]Provider{
		"anthropic": {APIKey: "file-key", Enabled: true},
	}))
	t.Cleanup(func() {
		dotEnvSecretsMu.Lock()
		dotEnvSecrets = nil
		dotEnvSecretsMu.Unlock()
	})
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("# comment\nANTHROPIC_API_KEY=dotenv-key\nGEMINI_MODEL=\"gemini-x\"\n"), 0o600))
	t.Setenv("OPENAI_API_KEY", "env-key")

	cfg, warnings, err := Assemble(dir)
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Equal(t, "llmconfig://anthropic/api_key", cfg.Providers["anthropic"].APIKey)
	require.Equal(t, "env://OPENAI_API_KEY", cfg.Providers["openai"].APIKey)
	require.Equal(t, "gemini-x", cfg.Providers["google"].Model)

	// The .env literal outranks the file's key at resolution time too.
	got, err := ResolveSecret(t.Context(), "anthropic/api_key")
	require.NoError(t, err)
	require.Equal(t, "dotenv-key", string(got))

	// No .env: the file's key is served again.
	cfg, _, err = Assemble(t.TempDir())
	require.NoError(t, err)
	require.Equal(t, "llmconfig://anthropic/api_key", cfg.Providers["anthropic"].APIKey)
	got, err = ResolveSecret(t.Context(), "anthropic/api_key")
	require.NoError(t, err)
	require.Equal(t, "file-key", string(got))
}

// An unreadable source is left out with a warning; the others still apply.
func TestAssembleUnreadableConfigFile(t *testing.T) {
	clearLLMEnv(t)
	useTempConfig(t, nil)
	require.NoError(t, os.MkdirAll(filepath.Dir(ConfigFile), 0o755))
	require.NoError(t, os.WriteFile(ConfigFile, []byte("[[[ not toml"), 0o600))
	t.Setenv("OPENAI_API_KEY", "env-key")

	cfg, warnings, err := Assemble(t.TempDir())
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	require.Equal(t, "env://OPENAI_API_KEY", cfg.Providers["openai"].APIKey)

	t.Setenv("OPENAI_DISABLE_STREAMING", "nope")
	_, _, err = Assemble(t.TempDir())
	require.ErrorIs(t, err, ErrMalformed)
}

func TestResolveSecretErrors(t *testing.T) {
	useTempConfig(t, fileConfig("", "", map[string]Provider{
		"anthropic": {APIKey: "k", Enabled: false},
		"openai":    {APIKey: "llmconfig://openai/api_key", Enabled: true},
		"local":     {BaseURL: "http://localhost", Enabled: true},
	}))
	for _, path := range []string{"anthropic", "nope/api_key", "gemini/api_key", "openai/password"} {
		_, err := ResolveSecret(t.Context(), path)
		require.Error(t, err, path)
		require.NotErrorIs(t, err, secrets.ErrNotFound, path)
	}
	for _, path := range []string{
		"anthropic/api_key",                  // disabled
		"google/api_key",                     // absent
		"openrouter/api_key",                 // absent
		"local/api_key",                      // no key
		"openai/auth_token",                  // not OAuth
		"openai/auth_token_expires_at",       // not OAuth
		"openai-codex/auth_token_expires_at", // absent
	} {
		_, err := ResolveSecret(t.Context(), path)
		require.ErrorIs(t, err, secrets.ErrNotFound, path)
	}
	// A stored llmconfig:// reference could only loop.
	_, err := ResolveSecret(t.Context(), "openai/api_key")
	require.ErrorContains(t, err, "cannot refer to llmconfig://")
}

func TestResolveSecretAPIKey(t *testing.T) {
	t.Setenv("STORED_KEY_SOURCE", "from-env-ref")
	useTempConfig(t, fileConfig("", "", map[string]Provider{
		"google":    {APIKey: "gem-key", Enabled: true},
		"anthropic": {APIKey: "env://STORED_KEY_SOURCE", Enabled: true},
	}))
	got, err := ResolveSecret(t.Context(), "google/api_key")
	require.NoError(t, err)
	require.Equal(t, "gem-key", string(got))

	// A stored reference is followed.
	got, err = ResolveSecret(t.Context(), "anthropic/api_key")
	require.NoError(t, err)
	require.Equal(t, "from-env-ref", string(got))
}

func TestResolveSecretOAuth(t *testing.T) {
	srv := newFakeOAuthServer(t, "rt-0")
	srv.install(t)
	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	useTempConfig(t, fileConfig("anthropic", "", map[string]Provider{
		"anthropic": {
			AuthType: "oauth", AuthToken: "current-token", RefreshToken: "rt-0",
			TokenExpiresAt: expiresAt.UnixMilli(), Enabled: true,
		},
		"openai-codex": expiredOAuthProvider("rt-0"),
	}))

	// A fresh token is served as stored, with its expiry.
	got, err := ResolveSecret(t.Context(), "anthropic/auth_token")
	require.NoError(t, err)
	require.Equal(t, "current-token", string(got))
	got, err = ResolveSecret(t.Context(), "anthropic/auth_token_expires_at")
	require.NoError(t, err)
	require.Equal(t, expiresAt.Format(time.RFC3339), string(got))
	grants, _ := srv.state()
	require.Zero(t, grants)

	// An expired one is refreshed on read, and the expiry read that follows
	// sees the rotated token's expiry.
	got, err = ResolveSecret(t.Context(), "openai-codex/auth_token")
	require.NoError(t, err)
	require.Equal(t, "access-1", string(got))
	got, err = ResolveSecret(t.Context(), "openai-codex/auth_token_expires_at")
	require.NoError(t, err)
	gotExpiry, err := time.Parse(time.RFC3339, string(got))
	require.NoError(t, err)
	require.WithinDuration(t, time.Now().Add(time.Hour), gotExpiry, time.Minute)
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "access-1", cfg.LLM.Providers["openai-codex"].AuthToken)
}

// The engine reports a token it found rejected by fingerprint. The resolver
// refreshes it even though its recorded expiry is still ahead — once: the
// rotated token is what the next read gets, without spending another grant.
func TestResolveSecretRejectedToken(t *testing.T) {
	srv := newFakeOAuthServer(t, "rt-0")
	srv.install(t)
	useTempConfig(t, fileConfig("anthropic", "", map[string]Provider{
		"anthropic": {
			AuthType: "oauth", AuthToken: "rejected-token", RefreshToken: "rt-0",
			TokenExpiresAt: time.Now().Add(time.Hour).UnixMilli(), Enabled: true,
		},
	}))
	rejected := fmt.Sprintf("%x", sha256.Sum256([]byte("rejected-token")))
	ctx := secretprovider.ContextWithRejectedSecretValue(t.Context(), rejected)
	resolve, path, err := secretprovider.ResolverForID("llmconfig://anthropic/auth_token")
	require.NoError(t, err)
	secretprovider.RegisterLLMConfigResolver(ResolveSecret)
	t.Cleanup(func() { secretprovider.RegisterLLMConfigResolver(nil) })
	for range 2 {
		got, err := resolve(ctx, path)
		require.NoError(t, err)
		require.Equal(t, "access-1", string(got))
	}
	grants, _ := srv.state()
	require.Equal(t, 1, grants)
}

// A token taken from ./.env is a user-supplied bearer, not the config's
// login: serving it — even when rejected — must never rotate the login.
func TestResolveSecretDotEnvTokenDoesNotRotateLogin(t *testing.T) {
	clearLLMEnv(t)
	srv := newFakeOAuthServer(t, "rt-0")
	srv.install(t)
	useTempConfig(t, fileConfig("anthropic", "", map[string]Provider{
		"anthropic": expiredOAuthProvider("rt-0"),
	}))
	t.Cleanup(func() {
		dotEnvSecretsMu.Lock()
		dotEnvSecrets = nil
		dotEnvSecretsMu.Unlock()
	})
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("ANTHROPIC_AUTH_TOKEN=dotenv-token\n"), 0o600))
	cfg, _, err := Assemble(dir)
	require.NoError(t, err)
	require.Equal(t, "llmconfig://anthropic/auth_token", cfg.Providers["anthropic"].AuthToken)
	require.Empty(t, cfg.Providers["anthropic"].AuthTokenExpiresAt)

	rejected := fmt.Sprintf("%x", sha256.Sum256([]byte("dotenv-token")))
	got, err := ResolveSecret(secretprovider.ContextWithRejectedSecretValue(t.Context(), rejected), "anthropic/auth_token")
	require.NoError(t, err)
	require.Equal(t, "dotenv-token", string(got))
	require.Zero(t, srv.requestCount())
}

func TestResolveSecretDisabledLoginNotRefreshed(t *testing.T) {
	srv := newFakeOAuthServer(t, "rt-0")
	srv.install(t)
	disabled := expiredOAuthProvider("rt-0")
	disabled.Enabled = false
	useTempConfig(t, fileConfig("", "", map[string]Provider{"anthropic": disabled}))
	_, err := ResolveSecret(t.Context(), "anthropic/auth_token")
	require.True(t, errors.Is(err, secrets.ErrNotFound), "err = %v", err)
	require.Zero(t, srv.requestCount())
}

func TestCopilotFileAssembly(t *testing.T) {
	require.True(t, IsProvider("github-copilot"))
	file := fileConfig("github-copilot", "claude-sonnet", map[string]Provider{"github-copilot": {
		AuthType: "oauth", AuthToken: "copilot", RefreshToken: "github", TokenExpiresAt: time.Now().Add(time.Hour).UnixMilli(),
		BaseURL: "https://business.example", Model: "claude-sonnet", Enabled: true,
	}})
	cfg, literals, err := assemble(file, mapEnv(nil), nil)
	require.NoError(t, err)
	require.Empty(t, literals)
	require.Equal(t, "github-copilot", cfg.DefaultProvider)
	require.Equal(t, &engine.LLMProviderConfig{
		AuthToken: "llmconfig://github-copilot/auth_token", AuthTokenExpiresAt: "llmconfig://github-copilot/auth_token_expires_at",
		BaseURL: "https://business.example", Model: "claude-sonnet",
	}, cfg.Providers["github-copilot"])
}

func TestCopilotEnvironmentAssembly(t *testing.T) {
	cfg, literals, err := assemble(nil, mapEnv(map[string]string{
		"COPILOT_GITHUB_TOKEN": "explicit-github", "COPILOT_MODEL": "claude-sonnet",
		"COPILOT_SMALL_MODEL": "gpt-small", "COPILOT_REASONING_EFFORT": "high",
	}), nil)
	require.NoError(t, err)
	require.Empty(t, literals)
	require.Equal(t, "github-copilot", cfg.DefaultProvider)
	require.Equal(t, "claude-sonnet", cfg.DefaultModel)
	require.Equal(t, &engine.LLMProviderConfig{
		AuthToken: "llmconfig://github-copilot/auth_token", AuthTokenExpiresAt: "llmconfig://github-copilot/auth_token_expires_at",
		BaseURL: copilotAPIBaseURL, Model: "claude-sonnet", SmallModel: "gpt-small", ReasoningEffort: "high",
	}, cfg.Providers["github-copilot"])
	// API-key model variables retain their existing priority ahead of Copilot.
	for _, name := range []string{"OPENAI_MODEL", "ANTHROPIC_MODEL", "GEMINI_MODEL"} {
		cfg, _, err := assemble(nil, mapEnv(map[string]string{name: "api-model", "COPILOT_MODEL": "copilot-model"}), nil)
		require.NoError(t, err)
		require.NotEqual(t, "github-copilot", cfg.DefaultProvider)
	}
	cfg, _, err = assemble(nil, mapEnv(map[string]string{"GH_TOKEN": "gh", "GITHUB_TOKEN": "github"}), nil)
	require.NoError(t, err)
	require.Nil(t, cfg, "unrelated GitHub tokens must not configure Copilot")
}

func isolateCopilotEnv(t *testing.T) {
	t.Helper()
	for _, ev := range envVars {
		t.Setenv(ev.name, "")
	}
	t.Setenv(disableStreamingVar, "")
	dotEnvSecretsMu.Lock()
	orig := dotEnvSecrets
	dotEnvSecrets = nil
	dotEnvSecretsMu.Unlock()
	t.Cleanup(func() { dotEnvSecretsMu.Lock(); dotEnvSecrets = orig; dotEnvSecretsMu.Unlock() })
	oauthRefreshMu.Lock()
	origCache := copilotEnvCache
	copilotEnvCache.sourceFingerprint = ""
	copilotEnvCache.rejected = nil
	oauthRefreshMu.Unlock()
	t.Cleanup(func() { oauthRefreshMu.Lock(); copilotEnvCache = origCache; oauthRefreshMu.Unlock() })
}

func TestCopilotEnvResolutionAndPrecedence(t *testing.T) {
	for _, stored := range []bool{false, true} {
		for _, dotenv := range []bool{false, true} {
			t.Run(fmt.Sprintf("stored=%t/dotenv=%t", stored, dotenv), func(t *testing.T) {
				isolateCopilotEnv(t)
				s := newFakeCopilotServer(t)
				s.api = "https://env-enterprise.example"
				var file *Config
				if stored {
					p := expiredOAuthProvider("stored-github-must-not-be-used")
					p.BaseURL = "https://stored-enterprise.example"
					file = fileConfig("github-copilot", "file-model", map[string]Provider{"github-copilot": p})
				}
				useTempConfig(t, file)
				var original []byte
				if stored {
					var err error
					original, err = os.ReadFile(ConfigFile)
					require.NoError(t, err)
				}
				t.Setenv(copilotTokenEnv, "process-github")
				cwd := t.TempDir()
				githubToken := "process-github"
				if dotenv {
					githubToken = "dotenv-github"
					require.NoError(t, os.WriteFile(filepath.Join(cwd, ".env"), []byte("COPILOT_GITHUB_TOKEN=dotenv-github\n"), 0600))
				}
				cfg, warnings, err := Assemble(cwd)
				require.NoError(t, err)
				require.Empty(t, warnings)
				require.Empty(t, s.tokens(), "assembly must remain lazy")
				require.Equal(t, "llmconfig://github-copilot/auth_token", cfg.Providers["github-copilot"].AuthToken)
				require.NotEqual(t, "https://stored-enterprise.example", cfg.Providers["github-copilot"].BaseURL)
				// Proactive refresh must also respect the explicit environment credential.
				require.NoError(t, RefreshOAuthTokensIfNeeded(t.Context()))
				require.Empty(t, s.tokens())
				for range 2 {
					token, err := ResolveSecret(t.Context(), "github-copilot/auth_token")
					require.NoError(t, err)
					require.Equal(t, "copilot-1", string(token))
					expiry, err := ResolveSecret(t.Context(), "github-copilot/auth_token_expires_at")
					require.NoError(t, err)
					require.Equal(t, time.Unix(s.expiresAt, 0).UTC().Format(time.RFC3339), string(expiry))
				}
				p, err := RefreshOAuthProviderIfNeeded(t.Context(), "github-copilot")
				require.NoError(t, err)
				require.Equal(t, "https://env-enterprise.example", p.BaseURL)
				require.Equal(t, []string{"token " + githubToken}, s.tokens())
				if stored {
					current, err := os.ReadFile(ConfigFile)
					require.NoError(t, err)
					require.Equal(t, original, current, "environment exchange changed the stored login")
				} else {
					require.False(t, ConfigExists(), "environment exchange persisted a login")
				}
			})
		}
	}
}

func TestCopilotEnvCacheRejectionAndExpiry(t *testing.T) {
	isolateCopilotEnv(t)
	useTempConfig(t, nil)
	s := newFakeCopilotServer(t)
	t.Setenv(copilotTokenEnv, "env-github")
	token, err := ResolveSecret(t.Context(), "github-copilot/auth_token")
	require.NoError(t, err)
	require.Equal(t, "copilot-1", string(token))
	rejectedCtx := secretprovider.ContextWithRejectedSecretValue(t.Context(), copilotFingerprint(string(token)))
	for range 2 {
		token, err = ResolveSecret(rejectedCtx, "github-copilot/auth_token")
		require.NoError(t, err)
		require.Equal(t, "copilot-2", string(token))
	}
	oauthRefreshMu.Lock()
	copilotEnvCache.provider.TokenExpiresAt = time.Now().Add(-time.Second).UnixMilli()
	// Even inside the refresh floor, an actually expired token must be replaced.
	oauthRefreshMu.Unlock()
	token, err = ResolveSecret(t.Context(), "github-copilot/auth_token")
	require.NoError(t, err)
	require.Equal(t, "copilot-3", string(token))
	require.Equal(t, []string{"token env-github", "token env-github", "token env-github"}, s.tokens())
	// A changed environment source invalidates the credential and endpoint cache.
	t.Setenv(copilotTokenEnv, "another-github")
	s.mu.Lock()
	s.api = "https://another-env.example"
	s.mu.Unlock()
	p, ok, err := resolveCopilotEnvProvider(t.Context())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "copilot-4", p.AuthToken)
	require.Equal(t, "https://another-env.example", p.BaseURL)
	require.Equal(t, "token another-github", s.tokens()[3])
}

func TestCopilotEnvNeverReturnsRejectedToken(t *testing.T) {
	for _, status := range []int{0, http.StatusUnauthorized, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			isolateCopilotEnv(t)
			useTempConfig(t, nil)
			s := newFakeCopilotServer(t)
			s.sameToken = true
			t.Setenv(copilotTokenEnv, "env-github")
			token, err := ResolveSecret(t.Context(), "github-copilot/auth_token")
			require.NoError(t, err)
			require.Equal(t, "rejected-token", string(token))
			s.mu.Lock()
			s.status = status
			s.mu.Unlock()
			ctx := secretprovider.ContextWithRejectedSecretValue(t.Context(), copilotFingerprint(string(token)))
			for _, ctx := range []context.Context{ctx, ctx, t.Context()} {
				token, err = ResolveSecret(ctx, "github-copilot/auth_token")
				require.Error(t, err)
				require.Empty(t, token)
				if status == http.StatusUnauthorized {
					require.ErrorIs(t, err, ErrOAuthReauthenticationRequired)
				}
				require.NotContains(t, err.Error(), "credential-leak-marker")
			}
			require.Len(t, s.tokens(), 2, "repeated failures should obey the refresh floor")
		})
	}
}

func TestCopilotEnvConcurrentResolution(t *testing.T) {
	isolateCopilotEnv(t)
	useTempConfig(t, nil)
	s := newFakeCopilotServer(t)
	t.Setenv(copilotTokenEnv, "env-github")
	for _, tc := range []struct{ rejected, want string }{{"", "copilot-1"}, {copilotFingerprint("copilot-1"), "copilot-2"}} {
		ctx := secretprovider.ContextWithRejectedSecretValue(t.Context(), tc.rejected)
		results := make(chan error, 16)
		for range 16 {
			go func() {
				token, err := ResolveSecret(ctx, "github-copilot/auth_token")
				if err == nil && string(token) != tc.want {
					err = fmt.Errorf("token = %q, want %q", token, tc.want)
				}
				results <- err
			}()
		}
		for range 16 {
			require.NoError(t, <-results)
		}
	}
	require.Equal(t, []string{"token env-github", "token env-github"}, s.tokens())
}

func TestCopilotEnvShortLifetimeRefreshFloor(t *testing.T) {
	isolateCopilotEnv(t)
	useTempConfig(t, nil)
	s := newFakeCopilotServer(t)
	s.expiresAt = time.Now().Add(time.Minute).Unix()
	t.Setenv(copilotTokenEnv, "env-github")
	for range 2 {
		token, err := ResolveSecret(t.Context(), "github-copilot/auth_token")
		require.NoError(t, err)
		require.Equal(t, "copilot-1", string(token))
	}
	require.Len(t, s.tokens(), 1, "a freshly issued token inside the margin should respect the floor")
	oauthRefreshMu.Lock()
	copilotEnvCache.lastAttempt = time.Now().Add(-time.Minute)
	oauthRefreshMu.Unlock()
	token, err := ResolveSecret(t.Context(), "github-copilot/auth_token")
	require.NoError(t, err)
	require.Equal(t, "copilot-2", string(token))
	require.Len(t, s.tokens(), 2, "a token inside the safety margin is due once the floor passes")
}

func TestCopilotEnvSourceChangeDoesNotForgetRejectedBearer(t *testing.T) {
	isolateCopilotEnv(t)
	useTempConfig(t, nil)
	s := newFakeCopilotServer(t)
	s.sameToken = true
	t.Setenv(copilotTokenEnv, "first-github")
	token, err := ResolveSecret(t.Context(), "github-copilot/auth_token")
	require.NoError(t, err)
	ctx := secretprovider.ContextWithRejectedSecretValue(t.Context(), copilotFingerprint(string(token)))
	token, err = ResolveSecret(ctx, "github-copilot/auth_token")
	require.Error(t, err)
	require.Empty(t, token)
	t.Setenv(copilotTokenEnv, "second-github")
	token, err = ResolveSecret(t.Context(), "github-copilot/auth_token")
	require.ErrorContains(t, err, "previously rejected token")
	require.Empty(t, token)
	require.Equal(t, []string{"token first-github", "token first-github", "token second-github"}, s.tokens())
}
