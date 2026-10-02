package engine

// LLMConfig is a client's LLM routing configuration, assembled client-side
// (config file, environment, .env) and sent to the engine in ClientMetadata.
// See hack/designs/llm-config-transport.md.
//
// Routing fields are plain values. Credential fields carry secret URIs that
// the engine resolves against the sending client's session, on demand, for
// the provider it actually routes — never values.
type LLMConfig struct {
	// DefaultProvider and DefaultModel are the configured default route, as
	// chosen by `dagger llm setup` / `dagger llm set-default`. Either may be
	// empty, in which case the engine infers a default from the configured
	// providers.
	DefaultProvider string `json:"default_provider,omitempty"`
	DefaultModel    string `json:"default_model,omitempty"`

	// Providers is keyed by the engine's provider name: anthropic, openai,
	// openai-codex, google, local.
	Providers map[string]*LLMProviderConfig `json:"providers,omitempty"`
}

// LLMProviderConfig configures one provider.
type LLMProviderConfig struct {
	// APIKey is a secret URI for the provider's API key.
	APIKey string `json:"api_key,omitempty"`
	// AuthToken is a secret URI for a subscription OAuth bearer token
	// (Anthropic Claude Code, OpenAI Codex). For Anthropic it is an
	// alternative to APIKey; a config that supplies one must not carry the
	// other, see Merge.
	AuthToken string `json:"auth_token,omitempty"`
	// AuthTokenExpiresAt is a secret URI resolving to AuthToken's true expiry
	// as RFC 3339 UTC, when known. The engine reads it right after the token
	// within one resolution, so a resolver that rotates the token on read
	// must update the expiry in the same step.
	AuthTokenExpiresAt string `json:"auth_token_expires_at,omitempty"`

	BaseURL         string `json:"base_url,omitempty"`
	Model           string `json:"model,omitempty"`
	SmallModel      string `json:"small_model,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`

	// APICompat selects the wire protocol of a local endpoint: "openai" or
	// "anthropic".
	APICompat string `json:"api_compat,omitempty"`

	// AzureVersion routes an OpenAI provider through Azure OpenAI with the
	// given API version.
	AzureVersion string `json:"azure_version,omitempty"`
	// DisableStreaming turns off streaming responses for an OpenAI provider.
	DisableStreaming bool `json:"disable_streaming,omitempty"`

	// ClaudeCodeVersion is the Claude Code release to present when
	// authenticating Anthropic with a subscription OAuth token, as a bare
	// X.Y.Z.
	ClaudeCodeVersion string `json:"claude_code_version,omitempty"`
}

// Clone returns a deep copy.
func (c *LLMConfig) Clone() *LLMConfig {
	if c == nil {
		return nil
	}
	cp := *c
	if c.Providers != nil {
		cp.Providers = make(map[string]*LLMProviderConfig, len(c.Providers))
		for name, p := range c.Providers {
			pp := *p
			cp.Providers[name] = &pp
		}
	}
	return &cp
}

// Provider returns the named provider's config, allocating it if absent.
func (c *LLMConfig) Provider(name string) *LLMProviderConfig {
	if c.Providers == nil {
		c.Providers = map[string]*LLMProviderConfig{}
	}
	p, ok := c.Providers[name]
	if !ok {
		p = &LLMProviderConfig{}
		c.Providers[name] = p
	}
	return p
}

// IsEmpty reports whether the config carries nothing at all.
func (c *LLMConfig) IsEmpty() bool {
	if c == nil {
		return true
	}
	if c.DefaultProvider != "" || c.DefaultModel != "" {
		return false
	}
	for _, p := range c.Providers {
		if !p.IsEmpty() {
			return false
		}
	}
	return true
}

// IsEmpty reports whether no field is set.
func (p *LLMProviderConfig) IsEmpty() bool {
	return p == nil || *p == LLMProviderConfig{}
}

// HasCredential reports whether the provider has any credential configured.
func (p *LLMProviderConfig) HasCredential() bool {
	return p != nil && (p.APIKey != "" || p.AuthToken != "")
}

// Merge overlays src onto p: only fields src sets overwrite p's, so a config
// can be layered (the session's main client as the base with the calling
// client's own values on top).
//
// The API key and the OAuth token are alternative credentials for the same
// provider. When src supplies one but not the other, the supplied credential
// wins outright: the other is cleared so a value layered in earlier (the
// host's OAuth login when a nested client sets an explicit API key) can't
// shadow it at request time.
func (p *LLMProviderConfig) Merge(src *LLMProviderConfig) {
	if src == nil {
		return
	}
	switch {
	case src.APIKey != "" && src.AuthToken == "":
		p.APIKey = src.APIKey
		p.AuthToken = ""
		p.AuthTokenExpiresAt = ""
	case src.AuthToken != "" && src.APIKey == "":
		p.AuthToken = src.AuthToken
		p.AuthTokenExpiresAt = src.AuthTokenExpiresAt
		p.APIKey = ""
	case src.APIKey != "" && src.AuthToken != "":
		p.APIKey = src.APIKey
		p.AuthToken = src.AuthToken
		p.AuthTokenExpiresAt = src.AuthTokenExpiresAt
	}
	if src.BaseURL != "" {
		p.BaseURL = src.BaseURL
	}
	if src.Model != "" {
		p.Model = src.Model
	}
	if src.SmallModel != "" {
		p.SmallModel = src.SmallModel
	}
	if src.ReasoningEffort != "" {
		p.ReasoningEffort = src.ReasoningEffort
	}
	if src.APICompat != "" {
		p.APICompat = src.APICompat
	}
	if src.AzureVersion != "" {
		p.AzureVersion = src.AzureVersion
	}
	if src.DisableStreaming {
		p.DisableStreaming = true
	}
	if src.ClaudeCodeVersion != "" {
		p.ClaudeCodeVersion = src.ClaudeCodeVersion
	}
}
