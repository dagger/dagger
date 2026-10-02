package llmconfig

import (
	"context"
	"errors"
	"fmt"
	"maps"
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

// openRouterBaseURL is the endpoint an `openrouter` provider in the config
// file routes to when it names none: OpenRouter is OpenAI-compatible, so it
// rides in the wire `openai` slot.
const openRouterBaseURL = "https://openrouter.ai/api/v1"

// wireSlots maps each wire provider name (the engine's LLMProvider names) to
// the config-file provider keys that can fill it, in tie-break order. Only one
// file provider can own a slot; see FileProviderName.
var wireSlots = map[string][]string{
	"anthropic":    {"anthropic"},
	"openai":       {"openai", "openrouter"},
	"openai-codex": {"openai-codex"},
	"google":       {"google", "gemini"},
	"local":        {"local"},
}

// WireProviderName normalizes a config-file provider key to its wire name:
// `gemini` is `google`, `openrouter` is `openai`. It returns "" for a name the
// engine has no slot for.
func WireProviderName(name string) string {
	for wire, names := range wireSlots {
		if slices.Contains(names, name) {
			return wire
		}
	}
	return ""
}

// FileProviderName returns the config-file provider that fills the wire
// provider's slot, and false when no enabled provider does. When several
// enabled file providers share a slot (openai and openrouter, google and
// gemini), the default provider owns it, else the first in tie-break order —
// so map iteration order can never pair one provider's key with another's
// base URL.
func (c *Config) FileProviderName(wire string) (string, bool) {
	if c == nil {
		return "", false
	}
	owner := ""
	for _, name := range wireSlots[wire] {
		p, ok := c.LLM.Providers[name]
		if !ok || !p.Enabled {
			continue
		}
		if owner == "" || name == c.LLM.DefaultProvider {
			owner = name
		}
	}
	return owner, owner != ""
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
	for _, wire := range slices.Sorted(maps.Keys(wireSlots)) {
		name, ok := file.FileProviderName(wire)
		if !ok {
			continue
		}
		p := file.LLM.Providers[name]
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
		if name == "openrouter" && wp.BaseURL == "" {
			wp.BaseURL = openRouterBaseURL
		}
		switch {
		case p.IsOAuth():
			// The token is served — and refreshed when due or rejected — by
			// ResolveSecret, which re-reads the file at every resolution.
			if p.AuthToken != "" {
				wp.AuthToken = fileCredential(wire, FieldAuthToken, p.AuthToken)
				wp.AuthTokenExpiresAt = llmConfigURI(wire, FieldAuthTokenExpiresAt)
			}
		case p.APIKey != "":
			wp.APIKey = fileCredential(wire, FieldAPIKey, p.APIKey)
		}
		if !wp.IsEmpty() {
			*cfg.Provider(wire) = *wp
		}
	}

	if file.LLM.DefaultProvider != "" || file.LLM.DefaultModel != "" {
		defaultProvider := file.LLM.DefaultProvider
		if p, ok := file.LLM.Providers[defaultProvider]; ok && !p.Enabled {
			// A disabled default contributes nothing, its model included.
			return cfg
		}
		if wire := WireProviderName(defaultProvider); wire != "" {
			defaultProvider = wire
		}
		cfg.DefaultProvider = defaultProvider
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

// ResolveSecret serves llmconfig://<provider>/<field> secrets, provider being
// a wire name. It is registered as the scheme's resolver by the CLI.
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
	wire, field, ok := strings.Cut(path, "/")
	if !ok {
		return nil, fmt.Errorf("llmconfig: malformed path %q, want <provider>/<field>", path)
	}
	if _, ok := wireSlots[wire]; !ok {
		return nil, fmt.Errorf("llmconfig: unknown provider %q", wire)
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
	name, ok := cfg.FileProviderName(wire)
	if !ok {
		return nil, fmt.Errorf("llmconfig: provider %q is not configured or not enabled: %w", wire, secrets.ErrNotFound)
	}
	p := cfg.LLM.Providers[name]
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
