package core

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/catwalk/pkg/embedded"
)

// llmCatalogProviders maps LLM providers to catwalk catalog IDs. Providers
// left out (local, other, ...) serve arbitrary models with no reliable
// catalog entry.
var llmCatalogProviders = map[LLMProvider]catwalk.InferenceProvider{
	Anthropic:   catwalk.InferenceProviderAnthropic,
	OpenAI:      catwalk.InferenceProviderOpenAI,
	OpenAICodex: catwalk.InferenceProviderOpenAI,
	Google:      catwalk.InferenceProviderGemini,
}

var (
	llmCatalogOnce sync.Once
	llmCatalog     map[catwalk.InferenceProvider]map[string]catwalk.Model
)

func loadLLMCatalog() map[catwalk.InferenceProvider]map[string]catwalk.Model {
	llmCatalogOnce.Do(func() {
		llmCatalog = make(map[catwalk.InferenceProvider]map[string]catwalk.Model)
		for _, p := range embedded.GetAll() {
			models := make(map[string]catwalk.Model, len(p.Models))
			for _, m := range p.Models {
				key := normalizeModelID(m.ID)
				// Catalogs list newest models first; keep the first when a
				// dated and an undated ID normalize to the same key.
				if _, ok := models[key]; !ok {
					models[key] = m
				}
			}
			llmCatalog[p.ID] = models
		}
	})
	return llmCatalog
}

// llmModelDateSuffix matches release-date model suffixes like "-20250929".
var llmModelDateSuffix = regexp.MustCompile(`-20\d{6}$`)

// normalizeModelID reduces a model name to a catalog key so user-supplied
// variants ("claude-sonnet-4-5-20250929", "claude-sonnet-4-5",
// "anthropic/claude-sonnet-4-5", "claude-fable-5[1m]") all resolve to the
// same entry.
func normalizeModelID(id string) string {
	if i := strings.LastIndexByte(id, '/'); i >= 0 {
		id = id[i+1:]
	}
	if i := strings.IndexByte(id, '['); i >= 0 {
		id = id[:i]
	}
	id = strings.TrimSuffix(id, "-latest")
	id = llmModelDateSuffix.ReplaceAllString(id, "")
	return id
}

// lookupCatalogModel returns catwalk's metadata (default max output tokens,
// context window) for a provider's model, if known.
func lookupCatalogModel(provider LLMProvider, model string) (catwalk.Model, bool) {
	cwID, ok := llmCatalogProviders[provider]
	if !ok {
		return catwalk.Model{}, false
	}
	m, ok := loadLLMCatalog()[cwID][normalizeModelID(model)]
	return m, ok
}

const (
	// llmCharsPerToken is the rough chars-per-token ratio used to estimate
	// how much of the context window the conversation occupies.
	llmCharsPerToken = 4
	// llmContextSafetyTokens pads the context estimate to absorb estimation
	// error and per-message/tool overhead.
	llmContextSafetyTokens = 4096
)

// estimateContextTokens roughly counts the tokens the conversation and tool
// declarations occupy, at ~4 chars per token.
func estimateContextTokens(messages []*LLMMessage, tools []LLMTool) int64 {
	var chars int64
	for _, msg := range messages {
		for _, block := range msg.Content {
			chars += int64(len(block.Text) + len(block.ToolName) + len(block.Arguments) + len(block.Signature))
		}
	}
	for _, tool := range tools {
		chars += int64(len(tool.Name) + len(tool.Description))
		if schema, err := json.Marshal(tool.Schema); err == nil {
			chars += int64(len(schema))
		}
	}
	return chars / llmCharsPerToken
}

// clampMaxTokensToContext caps maxTokens to the space remaining in the
// model's context window after the estimated input, so a model-max default
// doesn't overflow the window on long conversations (the API rejects
// requests whose input tokens + max_tokens exceed the context window). An
// unknown context window (zero) leaves maxTokens unchanged.
func clampMaxTokensToContext(maxTokens, contextWindow int64, messages []*LLMMessage, tools []LLMTool) int64 {
	if contextWindow <= 0 {
		return maxTokens
	}
	available := contextWindow - estimateContextTokens(messages, tools) - llmContextSafetyTokens
	return min(maxTokens, max(1, available))
}
