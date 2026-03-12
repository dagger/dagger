package llmconfig

// ModelInfo describes a model available from a provider.
type ModelInfo struct {
	// ID is the model identifier sent in API requests.
	ID string
	// Label is a short human-readable name for display in selection UIs.
	Label string
	// SupportsThinking indicates that the model supports an extended
	// reasoning / thinking mode.
	SupportsThinking bool
	// Default indicates this is the recommended default for its provider.
	Default bool
}

// ProviderModels maps provider keys (matching config provider names) to their
// available models. This is the single source of truth for which models are
// offered in `dagger llm setup` and for default model selection.
//
// Keep this list curated: include the latest flagship models plus one or two
// older/cheaper options per provider. Bump the Default flag when a new
// generation ships.
var ProviderModels = map[string][]ModelInfo{
	"anthropic": {
		{ID: "claude-sonnet-4-6", Label: "Claude Sonnet 4.6 (latest)", SupportsThinking: true, Default: true},
		{ID: "claude-sonnet-4-5", Label: "Claude Sonnet 4.5", SupportsThinking: true},
		{ID: "claude-haiku-4-5", Label: "Claude Haiku 4.5 (fast, cheap)", SupportsThinking: true},
		{ID: "claude-opus-4-6", Label: "Claude Opus 4.6 (smartest)", SupportsThinking: true},
	},
	"openai": {
		{ID: "gpt-4.1", Label: "GPT 4.1 (latest)", Default: true},
		{ID: "gpt-4.1-mini", Label: "GPT 4.1 Mini (fast, cheap)"},
		{ID: "o3", Label: "o3 (reasoning)"},
	},
	"openai-codex": {
		{ID: "gpt-5.3-codex", Label: "GPT 5.3 Codex (latest)", Default: true},
		{ID: "gpt-5.1-codex", Label: "GPT 5.1 Codex"},
		{ID: "codex-mini", Label: "Codex Mini (fast, cheap)"},
	},
	"openrouter": {
		{ID: "anthropic/claude-sonnet-4-6", Label: "Claude Sonnet 4.6 (latest)", SupportsThinking: true, Default: true},
		{ID: "anthropic/claude-sonnet-4-5", Label: "Claude Sonnet 4.5", SupportsThinking: true},
		{ID: "openai/gpt-4.1", Label: "GPT 4.1"},
		{ID: "google/gemini-2.5-flash", Label: "Gemini 2.5 Flash"},
		{ID: "meta-llama/llama-4-maverick", Label: "Llama 4 Maverick"},
	},
	"google": {
		{ID: "gemini-2.5-flash", Label: "Gemini 2.5 Flash (latest)", Default: true},
		{ID: "gemini-2.5-pro", Label: "Gemini 2.5 Pro"},
	},
}

// DefaultModelForProvider returns the default model ID for the given provider,
// or empty string if the provider is unknown.
func DefaultModelForProvider(provider string) string {
	for _, m := range ProviderModels[provider] {
		if m.Default {
			return m.ID
		}
	}
	// Fallback: first in list
	if models := ProviderModels[provider]; len(models) > 0 {
		return models[0].ID
	}
	return ""
}

// ModelsForProvider returns the model list for a given provider key.
// Returns nil for unknown providers.
func ModelsForProvider(provider string) []ModelInfo {
	return ProviderModels[provider]
}
