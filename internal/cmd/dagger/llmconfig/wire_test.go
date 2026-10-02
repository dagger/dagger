package llmconfig

import (
	"crypto/sha256"
	"errors"
	"fmt"
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
		want        bool
	}{
		{name: "env", env: map[string]string{"OPENAI_DISABLE_STREAMING": "true"}, want: true},
		{name: "file", file: streamingFile, want: true},
		{name: "env false over file", file: streamingFile, env: map[string]string{"OPENAI_DISABLE_STREAMING": "false"}, want: false},
		{
			name: "dotenv over env",
			env:  map[string]string{"OPENAI_API_KEY": "k", "OPENAI_DISABLE_STREAMING": "1"},
			dotenv: map[string]string{
				"OPENAI_DISABLE_STREAMING": "0",
			},
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _, err := assemble(tc.file, mapEnv(tc.env), tc.dotenv)
			require.NoError(t, err)
			var got bool
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
