package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/engine"
	telemetry "github.com/dagger/otel-go"
)

// LLMRouter maps model names and provider choices to endpoints, from the LLM
// configuration the session's clients sent in their ClientMetadata. See
// hack/designs/llm-config-transport.md.
type LLMRouter struct {
	// defaultProvider and defaultModel are the configured default route, when
	// the client chose one explicitly. See DefaultRoute for the fallback.
	defaultProvider LLMProvider
	defaultModel    string

	// Providers holds each configured provider's routing fields and the
	// client that resolves its credentials.
	Providers map[LLMProvider]*llmProviderRoute

	// localClient is the client whose configuration supplied the local
	// endpoint's base URL. A local endpoint is reachable from that client's
	// host, so the tunnel (see LLM.Endpoint) must run through that client's
	// session. Nil when the router was built for a single client.
	localClient *engine.ClientMetadata

	// credentialCtx bounds live credential resolution (subscription OAuth
	// tokens re-read at request time) by the session's lifetime. Nil leaves
	// resolution bound to the request that asks, which suits tests that build
	// a router without a session.
	credentialCtx context.Context
}

// llmProviderRoute is one provider's routing configuration plus the client
// whose session its credential URIs resolve against.
type llmProviderRoute struct {
	engine.LLMProviderConfig

	// client supplied the credentials. Nil when there are none.
	client *engine.ClientMetadata
}

// resolveCredential resolves one of the route's secret URIs against the
// client that supplied it — whoever happens to be making the request. That is
// what lets a nested `dagger agent` use the session's LLM auth without ever
// holding credentials itself.
func (route *llmProviderRoute) resolveCredential(ctx context.Context, uri string) (string, error) {
	if route.client == nil {
		return "", fmt.Errorf("LLM credential %q: no client to resolve it against", uri)
	}
	return resolveClientSecret(ctx, route.client, uri)
}

// resolveClientSecret resolves a secret URI through client's session
// attachables: one GetSecret RPC, no dagql node and no cache-key derivation.
func resolveClientSecret(ctx context.Context, client *engine.ClientMetadata, uri string) (string, error) {
	ctx = engine.ContextWithClientMetadata(ctx, client)
	secret := &Secret{URIVal: uri, SourceClientID: client.ClientID}
	plaintext, err := secret.Plaintext(ctx)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// llmProviderPriority is the order in which providers are considered when no
// default route is configured explicitly: the legacy env-var behaviour that
// CI setups with a single exported key rely on.
var llmProviderPriority = []LLMProvider{OpenAI, OpenAICodex, Anthropic, Google, Local}

// NewLLMRouter builds a router from a single client's configuration.
func NewLLMRouter(cfg *engine.LLMConfig, client *engine.ClientMetadata) (*LLMRouter, error) {
	r := &LLMRouter{Providers: map[LLMProvider]*llmProviderRoute{}}
	if err := r.Apply(cfg, client); err != nil {
		return nil, err
	}
	return r, nil
}

// Apply overlays cfg, supplied by client, onto the router. Only values cfg
// sets overwrite what is already there, so configuration can be layered: the
// session's main client as the base with the calling client's own values on
// top. Credentials follow the client that supplied them.
func (r *LLMRouter) Apply(cfg *engine.LLMConfig, client *engine.ClientMetadata) error {
	if cfg == nil {
		return nil
	}
	if r.Providers == nil {
		r.Providers = map[LLMProvider]*llmProviderRoute{}
	}
	if cfg.DefaultProvider != "" {
		r.defaultProvider = LLMProvider(cfg.DefaultProvider)
	}
	if cfg.DefaultModel != "" {
		r.defaultModel = cfg.DefaultModel
	}
	for name, src := range cfg.Providers {
		if src.IsEmpty() {
			continue
		}
		// The version is embedded verbatim in the Claude Code user-agent, so
		// a malformed value would present a client that never existed. Fail
		// loudly rather than silently falling back to the default the user
		// was trying to replace.
		if v := src.ClaudeCodeVersion; v != "" && !claudeCodeVersionPattern.MatchString(v) {
			return fmt.Errorf("%s claude_code_version must be a bare X.Y.Z version, got %q", name, v)
		}
		provider := LLMProvider(name)
		route := r.route(provider)
		route.Merge(src)
		if src.HasCredential() {
			route.client = client
		}
		if provider == Local && src.BaseURL != "" {
			// Later loads win regardless of whether the URL string differs:
			// localhost names a different host per client.
			r.localClient = client
		}
	}
	return nil
}

// route returns the provider's route, allocating an empty one if absent so
// callers can read it unconditionally.
func (r *LLMRouter) route(provider LLMProvider) *llmProviderRoute {
	if r.Providers == nil {
		r.Providers = map[LLMProvider]*llmProviderRoute{}
	}
	route, ok := r.Providers[provider]
	if !ok {
		route = &llmProviderRoute{}
		r.Providers[provider] = route
	}
	return route
}

// provider returns the provider's config, or an empty one if absent.
func (r *LLMRouter) provider(provider LLMProvider) *engine.LLMProviderConfig {
	if route, ok := r.Providers[provider]; ok {
		return &route.LLMProviderConfig
	}
	return &engine.LLMProviderConfig{}
}

func (r *LLMRouter) isAnthropicModel(model string) bool {
	return strings.HasPrefix(model, "claude-") || strings.HasPrefix(model, "anthropic/")
}

func (r *LLMRouter) isOpenAIModel(model string) bool {
	return strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "openai/")
}

func (r *LLMRouter) isCodexModel(model string) bool {
	return strings.Contains(model, "codex") || strings.HasPrefix(model, "openai-codex/")
}

func (r *LLMRouter) isGoogleModel(model string) bool {
	return strings.HasPrefix(model, "gemini-") || strings.HasPrefix(model, "google/")
}

func (r *LLMRouter) isMistralModel(model string) bool {
	return strings.HasPrefix(model, "mistral-") || strings.HasPrefix(model, "mistral/")
}

// isLocalModel reports whether model is served by the configured local
// endpoint. Unlike the other providers, local models have no naming convention
// to key on, so we match the configured model name exactly.
func (r *LLMRouter) isLocalModel(model string) bool {
	local := r.provider(Local)
	return local.BaseURL != "" && local.APICompat != "" && local.Model == model
}

func (r *LLMRouter) isRecording(model string) bool {
	return strings.HasPrefix(model, "recording-") || strings.HasPrefix(model, "recording/")
}

func (r *LLMRouter) routeAnthropicModel() *LLMEndpoint {
	cfg := r.provider(Anthropic)
	return &LLMEndpoint{
		Provider:          Anthropic,
		BaseURL:           cfg.BaseURL,
		IsOAuth:           cfg.AuthToken != "",
		ReasoningEffort:   cfg.ReasoningEffort,
		ClaudeCodeVersion: cfg.ClaudeCodeVersion,
	}
}

func (r *LLMRouter) routeOpenAIModel() *LLMEndpoint {
	cfg := r.provider(OpenAI)
	return &LLMEndpoint{
		Provider:         OpenAI,
		BaseURL:          cfg.BaseURL,
		azureVersion:     cfg.AzureVersion,
		disableStreaming: cfg.DisableStreaming,
	}
}

func (r *LLMRouter) routeCodexModel() *LLMEndpoint {
	cfg := r.provider(OpenAICodex)
	return &LLMEndpoint{
		// The Codex client appends "/codex" to reach the Responses API.
		BaseURL:         "https://chatgpt.com/backend-api",
		Provider:        OpenAICodex,
		IsOAuth:         true,
		ReasoningEffort: cfg.ReasoningEffort,
	}
}

func (r *LLMRouter) routeGoogleModel() *LLMEndpoint {
	cfg := r.provider(Google)
	return &LLMEndpoint{
		Provider:        Google,
		BaseURL:         cfg.BaseURL,
		ReasoningEffort: cfg.ReasoningEffort,
	}
}

func (r *LLMRouter) routeLocalModel() (*LLMEndpoint, error) {
	cfg := r.provider(Local)
	switch cfg.APICompat {
	case "openai", "anthropic":
	default:
		return nil, fmt.Errorf("unsupported local API compatibility mode: %q (must be %q or %q)", cfg.APICompat, "openai", "anthropic")
	}
	return &LLMEndpoint{
		Provider:  Local,
		BaseURL:   cfg.BaseURL,
		apiCompat: cfg.APICompat,
	}, nil
}

// routeOtherModel defaults to OpenAI compatibility for unknown providers,
// using the OpenAI slot's endpoint and credential.
func (r *LLMRouter) routeOtherModel() *LLMEndpoint {
	endpoint := r.routeOpenAIModel()
	endpoint.Provider = Other
	return endpoint
}

func (r *LLMRouter) routeRecordingModel(model string) (*LLMEndpoint, error) {
	recording, err := r.getRecording(model)
	if err != nil {
		return nil, err
	}
	return &LLMEndpoint{Client: newRecordedResponseProvider(recording)}, nil
}

// credentialProvider names the provider whose credentials an endpoint uses.
// Unknown providers route through the OpenAI slot.
func (endpoint *LLMEndpoint) credentialProvider() LLMProvider {
	if endpoint.Provider == Other {
		return OpenAI
	}
	return endpoint.Provider
}

// DefaultModel returns the default model, if one is configured: the client's
// explicit choice, else the first provider in llmProviderPriority with a
// model set, else the first with a credential, using that provider's
// built-in default model.
func (r *LLMRouter) DefaultModel() string {
	model, _ := r.DefaultRoute()
	return model
}

// DefaultRoute returns the default model and, when the configuration names
// it, the provider it routes to. The provider is empty when it must be
// inferred from the model name.
func (r *LLMRouter) DefaultRoute() (model string, provider LLMProvider) {
	if r.defaultModel != "" {
		if r.defaultProvider == OpenAICodex {
			return normalizeCodexModel(r.defaultModel), r.defaultProvider
		}
		return r.defaultModel, r.defaultProvider
	}
	for _, provider := range llmProviderPriority {
		cfg := r.provider(provider)
		if cfg.Model == "" {
			continue
		}
		if provider == OpenAICodex {
			// The codex slot is unambiguous, so pin it to Codex even if the
			// configured model (e.g. gpt-5.5) shares OpenAI's naming.
			return normalizeCodexModel(cfg.Model), provider
		}
		if provider == Local {
			return cfg.Model, provider
		}
		return cfg.Model, ""
	}
	switch {
	case r.provider(OpenAI).APIKey != "":
		return modelDefaultOpenAI, ""
	case r.provider(OpenAICodex).AuthToken != "":
		return normalizeCodexModel(modelDefaultCodex), OpenAICodex
	case r.provider(Anthropic).HasCredential():
		return modelDefaultAnthropic, ""
	case r.provider(OpenAI).BaseURL != "":
		// An OpenAI-compatible endpoint with no key: a self-hosted model
		// behind OPENAI_BASE_URL.
		return modelDefaultMeta, ""
	case r.provider(Google).APIKey != "":
		return modelDefaultGoogle, ""
	}
	return "", ""
}

// SmallModel returns the user-configured small model for provider, falling
// back to Catwalk's recommendation. Unknown and local providers without an
// explicit small model return no route, allowing the caller to retain the
// concrete model it is already using.
func (r *LLMRouter) SmallModel(provider LLMProvider) (string, bool) {
	if configured := r.provider(provider).SmallModel; configured != "" {
		return configured, true
	}
	return defaultSmallModel(provider)
}

// routeProvider dispatches directly to the named provider, bypassing
// model-name pattern matching.
func (r *LLMRouter) routeProvider(provider LLMProvider) (*LLMEndpoint, error) {
	switch provider {
	case Anthropic:
		return r.routeAnthropicModel(), nil
	case OpenAI:
		return r.routeOpenAIModel(), nil
	case OpenAICodex:
		return r.routeCodexModel(), nil
	case Google:
		return r.routeGoogleModel(), nil
	case Local:
		return r.routeLocalModel()
	case Other:
		return r.routeOtherModel(), nil
	default:
		return nil, fmt.Errorf("unknown LLM provider %q (expected one of %q, %q, %q, %q, %q, %q)",
			provider, Anthropic, Google, Local, OpenAI, OpenAICodex, Other)
	}
}

// Route returns an endpoint for the requested model. If the model name is not
// set, a default will be selected. If provider is set, it selects the
// provider explicitly; otherwise the provider is inferred from the model
// name.
//
// Routing is pure: the endpoint carries no credential and no provider client
// yet. Endpoint resolves those for the one provider that was routed.
func (r *LLMRouter) Route(model, provider string) (*LLMEndpoint, error) {
	if model == "" {
		var defaultProvider LLMProvider
		model, defaultProvider = r.DefaultRoute()
		if provider == "" {
			provider = string(defaultProvider)
		}
	} else {
		model = resolveModelAlias(model)
	}
	var endpoint *LLMEndpoint
	var err error
	switch {
	case provider != "":
		endpoint, err = r.routeProvider(LLMProvider(provider))
		if err != nil {
			return nil, err
		}
	// NB: must precede the prefix-based matchers — a local model may be named to
	// look like any provider's (e.g. "gpt-oss"), so an exact configured-model
	// match wins.
	case r.isLocalModel(model):
		endpoint, err = r.routeLocalModel()
		if err != nil {
			return nil, err
		}
	case r.isAnthropicModel(model):
		endpoint = r.routeAnthropicModel()
	// NB: must precede isOpenAIModel — a "codex"-named model (e.g. gpt-5.3-codex)
	// also matches the gpt- prefix; the codexModelPrefix form does not, but is
	// caught here too.
	case r.isCodexModel(model):
		endpoint = r.routeCodexModel()
	case r.isOpenAIModel(model):
		endpoint = r.routeOpenAIModel()
	case r.isGoogleModel(model):
		endpoint = r.routeGoogleModel()
	case r.isMistralModel(model):
		return nil, fmt.Errorf("mistral models are not yet supported")
	case r.isRecording(model):
		endpoint, err = r.routeRecordingModel(model)
		if err != nil {
			return nil, err
		}
	default:
		endpoint = r.routeOtherModel()
	}
	// Strip the Codex routing prefix (if any) so the model displays and is sent
	// to the provider under its bare name; non-Codex models are unaffected.
	endpoint.Model = strings.TrimPrefix(model, codexModelPrefix)
	if m, ok := lookupCatalogModel(endpoint.Provider, endpoint.Model); ok {
		endpoint.DefaultMaxTokens = m.DefaultMaxTokens
		endpoint.ContextWindow = m.ContextWindow
	}
	return endpoint, nil
}

// Endpoint routes the model and resolves the routed provider's credentials:
// an API key once, now; a subscription OAuth token as a live CredentialSource
// the endpoint's transport re-asks before every request. The provider client
// is not built yet — the caller may still need to reroute the endpoint
// through a tunnel (see LLM.Endpoint) — so call newClient when ready.
func (r *LLMRouter) Endpoint(ctx context.Context, model, provider string) (*LLMEndpoint, error) {
	endpoint, err := r.Route(model, provider)
	if err != nil {
		return nil, err
	}
	if endpoint.Client != nil {
		// A recording: nothing to authenticate.
		return endpoint, nil
	}
	route, ok := r.Providers[endpoint.credentialProvider()]
	if !ok {
		return endpoint, nil
	}
	if route.APIKey != "" {
		key, err := route.resolveCredential(ctx, route.APIKey)
		if err != nil {
			return nil, fmt.Errorf("resolve %s API key: %w", endpoint.Provider, err)
		}
		endpoint.Key = key
	}
	if route.AuthToken != "" {
		reload := credentialReloader(route.resolveCredential, route.AuthToken, route.AuthTokenExpiresAt)
		// Resolve once now, so a misconfigured credential fails at routing
		// rather than on the first request, and the SDK client is built with
		// the current token.
		cred, err := reload(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve %s auth token: %w", endpoint.Provider, err)
		}
		endpoint.AuthToken = cred.Token
		if r.credentialCtx != nil {
			reload = reload.detach(r.credentialCtx)
		}
		endpoint.AuthTokenSource = newCredentialSource(reload)
	}
	return endpoint, nil
}

// newClient builds the provider client for a routed, authenticated endpoint.
func (endpoint *LLMEndpoint) newClient() (LLMClient, error) {
	switch endpoint.Provider {
	case Anthropic:
		return newAnthropicClient(endpoint), nil
	case OpenAI, Other:
		return newOpenAIClient(endpoint, endpoint.azureVersion, endpoint.disableStreaming), nil
	case OpenAICodex:
		return newOpenAICodexClient(endpoint), nil
	case Google:
		return newGenaiClient(endpoint)
	case Local:
		switch endpoint.apiCompat {
		case "openai":
			return newOpenAIClient(endpoint, "", false), nil
		case "anthropic":
			return newAnthropicClient(endpoint), nil
		default:
			return nil, fmt.Errorf("unsupported local API compatibility mode: %q", endpoint.apiCompat)
		}
	default:
		return nil, fmt.Errorf("no client for LLM provider %q", endpoint.Provider)
	}
}

// loadLLMRouter creates an LLM router for the calling client. LLM
// configuration is a session-wide concern: the router is seeded from the
// session's main client (the CLI on the host), then overlaid with the calling
// (non-module) client's own configuration, which wins wherever it sets a
// value.
//
// The seeding is what lets a nested client — e.g. an
// experimentalPrivilegedNesting exec running `dagger agent` — inherit the
// session's LLM auth without ever holding the credentials: credential URIs
// resolve through the session of the client that supplied them, and only the
// engine-side router sees the plaintext. Nothing is injected into the nested
// container, so its processes cannot read the keys; they can only use the LLM
// through the API.
func loadLLMRouter(ctx context.Context, query *Query) (_ *LLMRouter, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "load LLM router config", telemetry.Internal(), telemetry.Encapsulate())
	defer telemetry.EndWithCause(span, &rerr)

	parentClient, err := query.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return nil, err
	}
	mainClient, err := query.MainClientCallerMetadata(ctx)
	if err != nil {
		return nil, err
	}
	router := &LLMRouter{Providers: map[LLMProvider]*llmProviderRoute{}}
	if mainClient.ClientID != parentClient.ClientID {
		if err := router.Apply(mainClient.LLMConfig, mainClient); err != nil {
			return nil, err
		}
	}
	if err := router.Apply(parentClient.LLMConfig, parentClient); err != nil {
		return nil, err
	}

	// Re-resolution of a credential must outlive this call. The endpoint this
	// router routes is memoized for the whole conversation and re-asked on
	// every provider request — including by an agent loop still stepping long
	// after the request that first routed it completed — so binding resolution
	// to this call's context would make the credential die with it. Scope it
	// to the session instead. This context supplies cancellation only: detach
	// borrows execution authority from each active request, since this call's
	// client lease will already be released. The parent client identifies the
	// session rather than the possibly-module ambient client.
	router.credentialCtx, err = query.Server.SessionScopedContext(
		engine.ContextWithClientMetadata(ctx, parentClient))
	if err != nil {
		return nil, fmt.Errorf("LLM credentials: session context: %w", err)
	}
	return router, nil
}
