// Package modelcatalog is the single source of truth for LLM model metadata
// (context window, default output-token budget, and pricing), backed by the
// embedded catwalk catalog. Both the engine (core) and the CLI consult it, so
// there is exactly one catalog rather than divergent per-side sources.
package modelcatalog

import (
	"regexp"
	"strings"
	"sync"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/catwalk/pkg/embedded"
)

// providerCatalogIDs maps a Dagger LLM provider name (the string form of
// core.LLMProvider) to a catwalk catalog ID. Providers left out (local, other,
// ...) serve arbitrary models with no reliable catalog entry.
var providerCatalogIDs = map[string]catwalk.InferenceProvider{
	"anthropic":    catwalk.InferenceProviderAnthropic,
	"openai":       catwalk.InferenceProviderOpenAI,
	"openai-codex": catwalk.InferenceProviderOpenAI,
	"google":       catwalk.InferenceProviderGemini,
}

var (
	catalogOnce sync.Once
	catalog     map[catwalk.InferenceProvider]map[string]catwalk.Model
)

func load() map[catwalk.InferenceProvider]map[string]catwalk.Model {
	catalogOnce.Do(func() {
		catalog = make(map[catwalk.InferenceProvider]map[string]catwalk.Model)
		for _, p := range embedded.GetAll() {
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
	// catwalk names the cached rates from the cache's perspective:
	// CostPer1MInCached is the cache-write (creation) rate and
	// CostPer1MOutCached is the cache-read rate.
	return m.CostPer1MIn/costPerMillion*float64(inputTokens) +
		m.CostPer1MOut/costPerMillion*float64(outputTokens) +
		m.CostPer1MOutCached/costPerMillion*float64(cacheReadTokens) +
		m.CostPer1MInCached/costPerMillion*float64(cacheWriteTokens)
}
