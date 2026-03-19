package llmconfig

import (
	"strings"
	"sync"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/catwalk/pkg/embedded"
)

// ModelInfo describes a model available from a provider.
type ModelInfo struct {
	// ID is the model identifier sent in API requests.
	ID string
	// Label is a short human-readable name for display in selection UIs.
	Label string
	// CanReason indicates that the model supports extended reasoning / thinking.
	CanReason bool
	// ReasoningLevels lists the reasoning effort levels the model supports
	// (e.g. "low", "medium", "high"). Empty if reasoning is not supported.
	ReasoningLevels []string
	// DefaultReasoningEffort is the default reasoning level for the model.
	DefaultReasoningEffort string
	// Default indicates this is the recommended default for its provider.
	Default bool
}

// ProviderEntry describes a provider option for the setup UI.
type ProviderEntry struct {
	// Label is the human-readable name shown in the selector.
	Label string
	// Value is the choice identifier used in the setup switch.
	Value string
	// ConfigKey is the key in Config.LLM.Providers (may differ from Value
	// for OAuth variants, e.g. "anthropic-oauth" → "anthropic").
	ConfigKey string
	// IsOAuth is true if this entry expects OAuth authentication.
	IsOAuth bool
}

// providerEntries is the ordered list of provider options for setup.
// Derived from the catalog keys plus OAuth variants.
var providerEntries = []ProviderEntry{
	{"Anthropic (API key)", "anthropic", "anthropic", false},
	{"Anthropic (Claude Code OAuth)", "anthropic-oauth", "anthropic", true},
	{"Google (Gemini)", "google", "google", false},
	{"Local / custom endpoint", "local", "local", false},
	{"OpenAI (API key)", "openai", "openai", false},
	{"OpenAI Codex (ChatGPT subscription)", "openai-codex", "openai-codex", true},
	{"OpenRouter", "openrouter", "openrouter", false},
}

// ProviderEntries returns the ordered list of provider options for setup UIs.
func ProviderEntries() []ProviderEntry {
	return providerEntries
}

// catwalkProviderID maps our provider keys to catwalk provider IDs.
var catwalkProviderID = map[string]catwalk.InferenceProvider{
	"anthropic":    catwalk.InferenceProviderAnthropic,
	"openai":       catwalk.InferenceProviderOpenAI,
	"openai-codex": catwalk.InferenceProviderOpenAI,
	"google":       catwalk.InferenceProviderGemini,
	"openrouter":   catwalk.InferenceProviderOpenRouter,
}

var (
	catalogOnce sync.Once
	catalog     map[string][]ModelInfo
)

func loadCatalog() map[string][]ModelInfo {
	catalogOnce.Do(func() {
		catalog = make(map[string][]ModelInfo)

		// Index catwalk providers by ID for lookup.
		cwByID := make(map[catwalk.InferenceProvider]catwalk.Provider)
		for _, p := range embedded.GetAll() {
			cwByID[p.ID] = p
		}

		for ourKey, cwID := range catwalkProviderID {
			cw, ok := cwByID[cwID]
			if !ok {
				continue
			}

			var models []ModelInfo
			for _, m := range cw.Models {
				// For openai-codex, only include codex models.
				if ourKey == "openai-codex" && !strings.Contains(m.ID, "codex") {
					continue
				}
				// For openai (non-codex), exclude codex models.
				if ourKey == "openai" && strings.Contains(m.ID, "codex") {
					continue
				}

				models = append(models, ModelInfo{
					ID:                     m.ID,
					Label:                  m.Name,
					CanReason:              m.CanReason,
					ReasoningLevels:        m.ReasoningLevels,
					DefaultReasoningEffort: m.DefaultReasoningEffort,
					Default:                m.ID == cw.DefaultLargeModelID,
				})
			}

			catalog[ourKey] = models
		}
	})

	return catalog
}

// DefaultModelForProvider returns the default model ID for the given provider,
// or empty string if the provider is unknown.
func DefaultModelForProvider(provider string) string {
	for _, m := range loadCatalog()[provider] {
		if m.Default {
			return m.ID
		}
	}
	// Fallback: first in list
	if models := loadCatalog()[provider]; len(models) > 0 {
		return models[0].ID
	}
	return ""
}

// ModelsForProvider returns the model list for a given provider key.
// Returns nil for unknown providers.
func ModelsForProvider(provider string) []ModelInfo {
	return loadCatalog()[provider]
}

// ModelByID looks up a specific model by provider and model ID.
// Returns the model and true if found, zero value and false otherwise.
func ModelByID(provider, modelID string) (ModelInfo, bool) {
	for _, m := range loadCatalog()[provider] {
		if m.ID == modelID {
			return m, true
		}
	}
	return ModelInfo{}, false
}
