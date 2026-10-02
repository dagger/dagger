package llmconfig

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/joho/godotenv"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/secretprovider"
	"github.com/dagger/dagger/internal/buildkit/session/secrets"
)

// This file builds the engine.LLMConfig the CLI sends in its client metadata,
// and serves the llmconfig:// secrets that config refers to. See
// hack/designs/llm-config-transport.md.

// Credential fields: the path components of llmconfig://<provider>/<field>,
// named after the engine.LLMProviderConfig fields they fill.
const (
	FieldAPIKey             = "api_key"
	FieldAuthToken          = "auth_token"
	FieldAuthTokenExpiresAt = "auth_token_expires_at" //nolint:gosec // a field name, not a credential
)

// providers are the provider names, shared by the config file's
// [llm.providers.<name>] keys and the engine's LLMProvider values.
var providers = []string{"anthropic", "openai", "openai-codex", "openrouter", "google", "local"}

// IsProvider reports whether name is a provider the engine can route to.
func IsProvider(name string) bool {
	return slices.Contains(providers, name)
}

// enabledProvider returns the named provider's file config when it is present
// and enabled.
func (c *Config) enabledProvider(name string) (Provider, bool) {
	if c == nil {
		return Provider{}, false
	}
	p, ok := c.LLM.Providers[name]
	if !ok || !p.Enabled {
		return Provider{}, false
	}
	return p, true
}

// envVar is one variable of the environment table: process env and ./.env
// share it.
type envVar struct {
	name     string
	provider string // wire name
	set      func(p *engine.LLMProviderConfig, v string)
	// credential names the llmconfig:// field for a secret variable; its value
	// travels as a secret URI, never as itself.
	credential string
}

// envVars is the environment table, the same names the engine used to probe.
// OPENAI_DISABLE_STREAMING is a bool and handled apart.
var envVars = []envVar{
	{name: "ANTHROPIC_API_KEY", provider: "anthropic", credential: FieldAPIKey},
	{name: "ANTHROPIC_AUTH_TOKEN", provider: "anthropic", credential: FieldAuthToken},
	{name: "ANTHROPIC_AUTH_TOKEN_EXPIRES_AT", provider: "anthropic", credential: FieldAuthTokenExpiresAt},
	{name: "ANTHROPIC_BASE_URL", provider: "anthropic", set: func(p *engine.LLMProviderConfig, v string) { p.BaseURL = v }},
	{name: "ANTHROPIC_MODEL", provider: "anthropic", set: func(p *engine.LLMProviderConfig, v string) { p.Model = v }},
	{name: "ANTHROPIC_SMALL_MODEL", provider: "anthropic", set: func(p *engine.LLMProviderConfig, v string) { p.SmallModel = v }},
	{name: "ANTHROPIC_REASONING_EFFORT", provider: "anthropic", set: func(p *engine.LLMProviderConfig, v string) { p.ReasoningEffort = v }},
	{name: "ANTHROPIC_CLAUDE_CODE_VERSION", provider: "anthropic", set: func(p *engine.LLMProviderConfig, v string) { p.ClaudeCodeVersion = v }},

	{name: "OPENAI_API_KEY", provider: "openai", credential: FieldAPIKey},
	{name: "OPENAI_BASE_URL", provider: "openai", set: func(p *engine.LLMProviderConfig, v string) { p.BaseURL = v }},
	{name: "OPENAI_MODEL", provider: "openai", set: func(p *engine.LLMProviderConfig, v string) { p.Model = v }},
	{name: "OPENAI_SMALL_MODEL", provider: "openai", set: func(p *engine.LLMProviderConfig, v string) { p.SmallModel = v }},
	{name: "OPENAI_AZURE_VERSION", provider: "openai", set: func(p *engine.LLMProviderConfig, v string) { p.AzureVersion = v }},

	{name: "OPENAI_CODEX_AUTH_TOKEN", provider: "openai-codex", credential: FieldAuthToken},
	{name: "OPENAI_CODEX_AUTH_TOKEN_EXPIRES_AT", provider: "openai-codex", credential: FieldAuthTokenExpiresAt},
	{name: "OPENAI_CODEX_MODEL", provider: "openai-codex", set: func(p *engine.LLMProviderConfig, v string) { p.Model = v }},
	{name: "OPENAI_CODEX_SMALL_MODEL", provider: "openai-codex", set: func(p *engine.LLMProviderConfig, v string) { p.SmallModel = v }},
	{name: "OPENAI_CODEX_REASONING_EFFORT", provider: "openai-codex", set: func(p *engine.LLMProviderConfig, v string) { p.ReasoningEffort = v }},

	{name: "OPENROUTER_API_KEY", provider: "openrouter", credential: FieldAPIKey},
	{name: "OPENROUTER_BASE_URL", provider: "openrouter", set: func(p *engine.LLMProviderConfig, v string) { p.BaseURL = v }},
	{name: "OPENROUTER_MODEL", provider: "openrouter", set: func(p *engine.LLMProviderConfig, v string) { p.Model = v }},
	{name: "OPENROUTER_SMALL_MODEL", provider: "openrouter", set: func(p *engine.LLMProviderConfig, v string) { p.SmallModel = v }},
	{name: "OPENROUTER_REASONING_EFFORT", provider: "openrouter", set: func(p *engine.LLMProviderConfig, v string) { p.ReasoningEffort = v }},

	{name: "GEMINI_API_KEY", provider: "google", credential: FieldAPIKey},
	{name: "GEMINI_BASE_URL", provider: "google", set: func(p *engine.LLMProviderConfig, v string) { p.BaseURL = v }},
	{name: "GEMINI_MODEL", provider: "google", set: func(p *engine.LLMProviderConfig, v string) { p.Model = v }},
	{name: "GEMINI_SMALL_MODEL", provider: "google", set: func(p *engine.LLMProviderConfig, v string) { p.SmallModel = v }},
	{name: "GEMINI_REASONING_EFFORT", provider: "google", set: func(p *engine.LLMProviderConfig, v string) { p.ReasoningEffort = v }},

	{name: "LOCAL_API_KEY", provider: "local", credential: FieldAPIKey},
	{name: "LOCAL_BASE_URL", provider: "local", set: func(p *engine.LLMProviderConfig, v string) { p.BaseURL = v }},
	{name: "LOCAL_MODEL", provider: "local", set: func(p *engine.LLMProviderConfig, v string) { p.Model = v }},
	{name: "LOCAL_SMALL_MODEL", provider: "local", set: func(p *engine.LLMProviderConfig, v string) { p.SmallModel = v }},
	{name: "LOCAL_API_COMPAT", provider: "local", set: func(p *engine.LLMProviderConfig, v string) { p.APICompat = v }},
}

const disableStreamingVar = "OPENAI_DISABLE_STREAMING"

// tokenVarFor names the OAuth token variable an *_AUTH_TOKEN_EXPIRES_AT
// variable belongs to.
func tokenVarFor(provider string) string {
	for _, ev := range envVars {
		if ev.provider == provider && ev.credential == FieldAuthToken {
			return ev.name
		}
	}
	return ""
}

// defaultModelVars is the engine's legacy default-route priority: when any of
// these is set in the environment or ./.env, the first one set decides the
// default route.
var defaultModelVars = []struct{ provider, name string }{
	{"openai", "OPENAI_MODEL"},
	{"openai-codex", "OPENAI_CODEX_MODEL"},
	{"openrouter", "OPENROUTER_MODEL"},
	{"anthropic", "ANTHROPIC_MODEL"},
	{"google", "GEMINI_MODEL"},
	{"local", "LOCAL_MODEL"},
}

// ErrMalformed marks an assembly error caused by a value that cannot be
// parsed. Unlike an unreadable source, sending the config without it would
// silently misconfigure the router.
var ErrMalformed = errors.New("malformed LLM configuration")

// Assemble builds the LLM configuration to send to the engine from, lowest to
// highest precedence: the config file's [llm] section, the process
// environment, and cwd/.env.
//
// A source that cannot be read (an unparseable config file or .env) is left
// out and reported in warnings; the rest still applies, as it did when the
// engine read these itself. A value that cannot be parsed is an error
// wrapping ErrMalformed, and no config is returned.
//
// Literal credentials read from .env are remembered for ResolveSecret, which
// serves them as llmconfig:// secrets; the last Assemble call wins.
func Assemble(cwd string) (cfg *engine.LLMConfig, warnings []error, err error) {
	file, err := Load()
	if err != nil {
		warnings = append(warnings, err)
		file = nil
	}
	dotenv, err := readDotEnv(filepath.Join(cwd, ".env"))
	if err != nil {
		warnings = append(warnings, err)
		dotenv = nil
	}
	cfg, literals, err := assemble(file, os.LookupEnv, dotenv)
	if err != nil {
		return nil, warnings, err
	}
	dotEnvSecretsMu.Lock()
	dotEnvSecrets = literals
	dotEnvSecretsMu.Unlock()
	return cfg, warnings, nil
}

func readDotEnv(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	env, err := godotenv.UnmarshalBytes(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return env, nil
}

// assemble is Assemble over explicit sources. It returns the config, and the
// literal credentials taken from dotenv keyed by "<provider>/<field>".
func assemble(
	file *Config,
	lookupEnv func(string) (string, bool),
	dotenv map[string]string,
) (*engine.LLMConfig, map[string]string, error) {
	getenv := func(name string) string {
		v, _ := lookupEnv(name)
		return v
	}
	dotenvLiterals := map[string]string{}

	out := fileLayer(file)
	envCfg := envLayer(getenv, func(provider, field, name, value string) string {
		return "env://" + name
	})
	dotCfg := envLayer(func(name string) string { return dotenv[name] }, func(provider, field, name, value string) string {
		dotenvLiterals[provider+"/"+field] = value
		return llmConfigURI(provider, field)
	})
	for _, layer := range []*engine.LLMConfig{envCfg, dotCfg} {
		for name, p := range layer.Providers {
			out.Provider(name).Merge(p)
		}
	}

	// Merge can only turn DisableStreaming on; an explicit "false" from a
	// higher layer must be able to turn it back off.
	for _, v := range []string{getenv(disableStreamingVar), dotenv[disableStreamingVar]} {
		if v == "" {
			continue
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %s=%q is not a boolean", ErrMalformed, disableStreamingVar, v)
		}
		if b || out.Providers["openai"] != nil {
			out.Provider("openai").DisableStreaming = b
		}
	}

	// An explicitly configured model in the environment decides the default
	// route, as it always did; otherwise the config file's default stands.
	for _, dm := range defaultModelVars {
		v := dotenv[dm.name]
		if v == "" {
			v = getenv(dm.name)
		}
		if v != "" {
			out.DefaultProvider = dm.provider
			out.DefaultModel = v
			break
		}
	}

	for name, p := range out.Providers {
		if p.IsEmpty() {
			delete(out.Providers, name)
		}
	}
	if len(out.Providers) == 0 {
		out.Providers = nil
	}
	if out.IsEmpty() {
		return nil, dotenvLiterals, nil
	}
	return out, dotenvLiterals, nil
}

// envLayer reads the environment table through getenv. A credential whose
// value is itself a secret URI travels verbatim; any other credential travels
// as the URI literal returns for it. An expiry without a token in the same
// layer is dropped: it describes no credential this layer supplies, and must
// not be served for a token from another one.
func envLayer(getenv func(string) string, literal func(provider, field, name, value string) string) *engine.LLMConfig {
	cfg := &engine.LLMConfig{}
	for _, ev := range envVars {
		v := getenv(ev.name)
		if v == "" {
			continue
		}
		if ev.credential == FieldAuthTokenExpiresAt && getenv(tokenVarFor(ev.provider)) == "" {
			continue
		}
		p := cfg.Provider(ev.provider)
		if ev.set != nil {
			ev.set(p, v)
			continue
		}
		uri := v
		if !IsSecretURI(v) {
			uri = literal(ev.provider, ev.credential, ev.name, v)
		}
		switch ev.credential {
		case FieldAPIKey:
			p.APIKey = uri
		case FieldAuthToken:
			p.AuthToken = uri
		case FieldAuthTokenExpiresAt:
			p.AuthTokenExpiresAt = uri
		}
	}
	return cfg
}

// fileLayer translates the config file's [llm] section. Disabled providers
// contribute nothing.
func fileLayer(file *Config) *engine.LLMConfig {
	cfg := &engine.LLMConfig{}
	if file == nil {
		return cfg
	}
	for _, name := range providers {
		p, ok := file.enabledProvider(name)
		if !ok {
			continue
		}
		wp := &engine.LLMProviderConfig{
			BaseURL:           p.BaseURL,
			Model:             p.Model,
			SmallModel:        p.SmallModel,
			ReasoningEffort:   p.ReasoningEffort,
			APICompat:         p.APICompat,
			AzureVersion:      p.AzureVersion,
			DisableStreaming:  p.DisableStreaming,
			ClaudeCodeVersion: p.ClaudeCodeVersion,
		}
		switch {
		case p.IsOAuth():
			// The token is served — and refreshed when due or rejected — by
			// ResolveSecret, which re-reads the file at every resolution.
			if p.AuthToken != "" {
				wp.AuthToken = fileCredential(name, FieldAuthToken, p.AuthToken)
				wp.AuthTokenExpiresAt = llmConfigURI(name, FieldAuthTokenExpiresAt)
			}
		case p.APIKey != "":
			wp.APIKey = fileCredential(name, FieldAPIKey, p.APIKey)
		}
		if !wp.IsEmpty() {
			*cfg.Provider(name) = *wp
		}
	}

	if file.LLM.DefaultProvider != "" || file.LLM.DefaultModel != "" {
		if p, ok := file.LLM.Providers[file.LLM.DefaultProvider]; ok && !p.Enabled {
			// A disabled default contributes nothing, its model included.
			return cfg
		}
		cfg.DefaultProvider = file.LLM.DefaultProvider
		cfg.DefaultModel = file.LLM.DefaultModel
	}
	return cfg
}

func fileCredential(provider, field, value string) string {
	if IsSecretURI(value) {
		return value
	}
	return llmConfigURI(provider, field)
}

func llmConfigURI(provider, field string) string {
	return "llmconfig://" + provider + "/" + field
}

// IsSecretURI reports whether v is a reference to a secret (op://…,
// vault://…, env://…) rather than the secret itself.
func IsSecretURI(v string) bool {
	if !strings.Contains(v, "://") {
		return false
	}
	_, _, err := secretprovider.ResolverForID(v)
	return err == nil
}

// dotEnvSecrets holds the literal credentials the last Assemble read from
// ./.env, keyed by "<provider>/<field>", for ResolveSecret to serve.
var (
	dotEnvSecretsMu sync.Mutex
	dotEnvSecrets   map[string]string
)

// ResolveSecret serves llmconfig://<provider>/<field> secrets. It is
// registered as the scheme's resolver by the CLI.
//
// A credential Assemble took from ./.env is served as read. Anything else is
// read from the config file at every resolution:
//
//   - api_key: the provider's stored API key.
//   - auth_token: the subscription OAuth token, refreshed first if it is due
//     or if the context carries the RejectedSecretValue fingerprint of the
//     current token; the rotated token is persisted.
//   - auth_token_expires_at: the token's true expiry, RFC 3339 UTC, or empty
//     when unknown. The engine reads it right after auth_token, so it sees
//     the expiry of a token that read just rotated.
//
// A provider that is absent or disabled is a not-found error.
func ResolveSecret(ctx context.Context, path string) ([]byte, error) {
	name, field, ok := strings.Cut(path, "/")
	if !ok {
		return nil, fmt.Errorf("llmconfig: malformed path %q, want <provider>/<field>", path)
	}
	if !IsProvider(name) {
		return nil, fmt.Errorf("llmconfig: unknown provider %q", name)
	}
	switch field {
	case FieldAPIKey, FieldAuthToken, FieldAuthTokenExpiresAt:
	default:
		return nil, fmt.Errorf("llmconfig: unknown field %q", field)
	}

	dotEnvSecretsMu.Lock()
	literal, fromDotEnv := dotEnvSecrets[path]
	dotEnvSecretsMu.Unlock()
	if fromDotEnv {
		return resolveStored(ctx, literal)
	}

	cfg, err := Load()
	if err != nil {
		return nil, err
	}
	p, ok := cfg.enabledProvider(name)
	if !ok {
		return nil, fmt.Errorf("llmconfig: provider %q is not configured or not enabled: %w", name, secrets.ErrNotFound)
	}
	switch field {
	case FieldAPIKey:
		if p.APIKey == "" {
			return nil, fmt.Errorf("llmconfig: provider %q has no API key: %w", name, secrets.ErrNotFound)
		}
		return resolveStored(ctx, p.APIKey)
	case FieldAuthToken:
		refreshed, err := RefreshOAuthProviderAfterRejection(ctx, name, secretprovider.RejectedSecretValue(ctx))
		if err != nil {
			return nil, err
		}
		if refreshed == nil || refreshed.AuthToken == "" {
			return nil, fmt.Errorf("llmconfig: provider %q has no OAuth login: %w", name, secrets.ErrNotFound)
		}
		return resolveStored(ctx, refreshed.AuthToken)
	default: // FieldAuthTokenExpiresAt
		if !p.IsOAuth() {
			return nil, fmt.Errorf("llmconfig: provider %q has no OAuth login: %w", name, secrets.ErrNotFound)
		}
		return []byte(p.TokenExpiresAtRFC3339()), nil
	}
}

// resolveStored returns a stored credential, following it when it is itself a
// secret URI. A llmconfig:// reference is not followed: it could only loop.
func resolveStored(ctx context.Context, v string) ([]byte, error) {
	if !IsSecretURI(v) {
		return []byte(v), nil
	}
	if strings.HasPrefix(v, "llmconfig://") {
		return nil, fmt.Errorf("llmconfig: a stored credential cannot refer to llmconfig://")
	}
	resolve, path, err := secretprovider.ResolverForID(v)
	if err != nil {
		return nil, err
	}
	return resolve(ctx, path)
}
