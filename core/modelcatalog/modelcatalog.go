// Package modelcatalog is the single source of truth for LLM model metadata
// (context window, default output-token budget, and pricing), backed by the
// embedded catwalk catalog. Both the engine (core) and the CLI consult it, so
// there is exactly one catalog rather than divergent per-side sources.
package modelcatalog

import (
	"regexp"
	"strings"
	"sync"

	"charm.land/catwalk/v3/pkg/catwalk"
	"charm.land/catwalk/v3/pkg/embedded"
)

// providerCatalogIDs maps a Dagger LLM provider name (the string form of
// core.LLMProvider) to a catwalk catalog ID. Providers left out (local, other,
// ...) serve arbitrary models with no reliable catalog entry.
var providerCatalogIDs = map[string]catwalk.InferenceProvider{
	"anthropic":      catwalk.InferenceProviderAnthropic,
	"openai":         catwalk.InferenceProviderOpenAI,
	"openai-codex":   catwalk.InferenceProviderOpenAI,
	"openrouter":     catwalk.InferenceProviderOpenRouter,
	"google":         catwalk.InferenceProviderGemini,
	"github-copilot": catwalk.InferenceProviderCopilot,
}

// Dagger's defaults track the latest general-purpose models in each provider's
// existing tier, rather than Catwalk's recommendations (which can lag releases).
// Keep these shared by engine routing and CLI setup. Local/custom endpoints have
// no defaults here: their available models are controlled by the user.
const (
	DefaultAnthropic  = "claude-sonnet-5-5"
	DefaultOpenAI     = "gpt-6.2-sol"
	DefaultGoogle     = "gemini-3.8-flash"
	DefaultCopilot    = "claude-sonnet-5.5"
	DefaultOpenRouter = "anthropic/claude-sonnet-5.5"
)

var providerDefaults = map[string]struct{ large, small string }{
	"anthropic":      {DefaultAnthropic, "claude-haiku-5-5"},
	"openai":         {DefaultOpenAI, "gpt-6-luna"},
	"openai-codex":   {DefaultOpenAI, "gpt-6-luna"},
	"google":         {DefaultGoogle, "gemini-3.8-flash"},
	"github-copilot": {DefaultCopilot, "claude-haiku-5.5"},
	"openrouter":     {DefaultOpenRouter, "anthropic/claude-haiku-5.5"},
}

// DefaultModel returns Dagger's default model for a hosted provider.
func DefaultModel(provider string) (string, bool) {
	defaults, ok := providerDefaults[provider]
	return defaults.large, ok
}

var (
	catalogOnce   sync.Once
	catalog       map[catwalk.InferenceProvider]map[string]catwalk.Model
	providerTypes map[catwalk.InferenceProvider]catwalk.Type
)

func load() map[catwalk.InferenceProvider]map[string]catwalk.Model {
	catalogOnce.Do(func() {
		catalog = make(map[catwalk.InferenceProvider]map[string]catwalk.Model)
		providerTypes = make(map[catwalk.InferenceProvider]catwalk.Type)
		for _, p := range embedded.GetAll() {
			providerTypes[p.ID] = p.Type
			models := make(map[string]catwalk.Model, len(p.Models))
			for _, m := range p.Models {
				key := NormalizeModelID(m.ID)
				// Catalogs list newest models first; keep the first when a
				// dated and an undated ID normalize to the same key.
				if _, ok := models[key]; !ok {
					models[key] = m
				}
			}
			catalog[p.ID] = models
		}
	})
	return catalog
}

// modelDateSuffix matches release-date model suffixes like "-20250929".
var modelDateSuffix = regexp.MustCompile(`-20\d{6}$`)

// NormalizeModelID reduces a model name to a catalog key so user-supplied
// variants ("claude-sonnet-4-5-20250929", "claude-sonnet-4-5",
// "anthropic/claude-sonnet-4-5", "claude-fable-5[1m]") all resolve to the
// same entry.
func NormalizeModelID(id string) string {
	if i := strings.LastIndexByte(id, '/'); i >= 0 {
		id = id[i+1:]
	}
	if i := strings.IndexByte(id, '['); i >= 0 {
		id = id[:i]
	}
	id = strings.TrimSuffix(id, "-latest")
	id = modelDateSuffix.ReplaceAllString(id, "")
	return id
}

// Lookup returns catwalk's metadata for a provider's model, if known. The
// provider is a Dagger provider name (e.g. "anthropic", "openai", "google");
// unrecognised providers (local, other, ...) always miss.
func Lookup(provider, model string) (catwalk.Model, bool) {
	cwID, ok := providerCatalogIDs[provider]
	if !ok {
		return catwalk.Model{}, false
	}
	m, ok := load()[cwID][NormalizeModelID(model)]
	return m, ok
}

// EndpointType returns the model's API format, falling back to its provider's
// format. Unknown models return no type: their names alone are not catalog
// evidence of which endpoint to use. This describes Catwalk's transport, not
// necessarily Dagger's (for example, Dagger uses Gemini's native SDK).
func EndpointType(provider, model string) (catwalk.Type, bool) {
	m, ok := Lookup(provider, model)
	if !ok {
		return "", false
	}
	endpointType := m.EffectiveType(providerTypes[providerCatalogIDs[provider]])
	return endpointType, endpointType != ""
}

// DefaultSmallModel returns Dagger's small model for a hosted provider.
// Local/custom providers return no model so callers retain their current route.
func DefaultSmallModel(provider string) (string, bool) {
	defaults, ok := providerDefaults[provider]
	return defaults.small, ok
}

// costPerMillion is the denominator for catwalk's per-1M-token prices.
const costPerMillion = 1_000_000.0

// Cost returns the dollar cost of the given token usage for a model, priced
// from the catalog. Input, output, cache-read and cache-write tokens are
// assumed disjoint and billed at their respective rates. An uncatalogued model
// (or unknown provider) costs 0.
func Cost(provider, model string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int64) float64 {
	m, ok := Lookup(provider, model)
	if !ok {
		return 0
	}
	return m.Pricing.Input/costPerMillion*float64(inputTokens) +
		m.Pricing.Output/costPerMillion*float64(outputTokens) +
		m.Pricing.CacheHit/costPerMillion*float64(cacheReadTokens) +
		m.Pricing.CacheCreate/costPerMillion*float64(cacheWriteTokens)
}
