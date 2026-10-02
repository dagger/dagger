package core

import (
	"encoding/json"

	"charm.land/catwalk/pkg/catwalk"

	"github.com/dagger/dagger/core/modelcatalog"
)

// normalizeModelID reduces a model name to a catalog key. See
// modelcatalog.NormalizeModelID.
func normalizeModelID(id string) string {
	return modelcatalog.NormalizeModelID(id)
}

// lookupCatalogModel returns catwalk's metadata (default max output tokens,
// context window) for a provider's model, if known.
func lookupCatalogModel(provider LLMProvider, model string) (catwalk.Model, bool) {
	return modelcatalog.Lookup(string(provider), model)
}

func defaultSmallModel(provider LLMProvider) (string, bool) {
	return modelcatalog.DefaultSmallModel(string(provider))
}

// LLMReasoningMode is how a model takes reasoning configuration.
type LLMReasoningMode int

const (
	// LLMReasoningUnknown is for models the catalog doesn't know (local
	// endpoints, models newer than the embedded catalog). Providers pass the
	// reasoning effort through in their native effort form.
	LLMReasoningUnknown LLMReasoningMode = iota
	// LLMReasoningUnsupported models can't reason; any effort is ignored.
	LLMReasoningUnsupported
	// LLMReasoningBudget models reason only with a thinking-token budget,
	// not an effort level (e.g. Claude Haiku 4.5, Claude Sonnet 4.5).
	LLMReasoningBudget
	// LLMReasoningEffort models accept an effort level natively.
	LLMReasoningEffort
)

// catalogReasoningMode derives a catalogued model's reasoning mode. Catwalk
// lists effort levels only for models that take one; a model that can reason
// but lists none takes a thinking-token budget instead.
func catalogReasoningMode(m catwalk.Model) LLMReasoningMode {
	switch {
	case !m.CanReason:
		return LLMReasoningUnsupported
	case len(m.ReasoningLevels) == 0:
		return LLMReasoningBudget
	default:
		return LLMReasoningEffort
	}
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
