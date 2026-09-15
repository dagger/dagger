package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeModelID(t *testing.T) {
	for input, want := range map[string]string{
		"claude-sonnet-4-5-20250929":  "claude-sonnet-4-5",
		"claude-sonnet-4-5":           "claude-sonnet-4-5",
		"claude-opus-4-20250514":      "claude-opus-4",
		"claude-3-5-sonnet-latest":    "claude-3-5-sonnet",
		"claude-fable-5[1m]":          "claude-fable-5",
		"anthropic/claude-sonnet-4-6": "claude-sonnet-4-6",
		"gpt-5.4":                     "gpt-5.4",
		"gemini-2.5-pro":              "gemini-2.5-pro",
	} {
		assert.Equal(t, want, normalizeModelID(input), "normalizeModelID(%q)", input)
	}
}

func TestLookupCatalogModel(t *testing.T) {
	m, ok := lookupCatalogModel(Anthropic, "claude-fable-5")
	require.True(t, ok)
	assert.Positive(t, m.DefaultMaxTokens)
	assert.Positive(t, m.ContextWindow)

	// Dated and undated forms of the same model resolve to the same entry.
	dated, ok := lookupCatalogModel(Anthropic, "claude-sonnet-4-5-20250929")
	require.True(t, ok)
	undated, ok := lookupCatalogModel(Anthropic, "claude-sonnet-4-5")
	require.True(t, ok)
	assert.Equal(t, dated.ID, undated.ID)

	// Codex shares OpenAI's catalog.
	_, ok = lookupCatalogModel(OpenAICodex, "gpt-5.3-codex")
	assert.True(t, ok)

	_, ok = lookupCatalogModel(Google, "gemini-2.5-flash")
	assert.True(t, ok)

	// Providers with no catalog, and unknown models, miss cleanly.
	_, ok = lookupCatalogModel(Local, "qwen3")
	assert.False(t, ok)
	_, ok = lookupCatalogModel(Anthropic, "totally-unknown-model")
	assert.False(t, ok)
}

func TestClampMaxTokensToContext(t *testing.T) {
	shortConvo := []*LLMMessage{{
		Role:    LLMMessageRoleUser,
		Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "hello"}},
	}}

	// Unknown context window: unchanged.
	assert.Equal(t, int64(64000), clampMaxTokensToContext(64000, 0, shortConvo, nil))

	// Plenty of room: unchanged.
	assert.Equal(t, int64(64000), clampMaxTokensToContext(64000, 200000, shortConvo, nil))

	// A conversation occupying most of the window shrinks the cap: 100k-token
	// window minus ~90k estimated input minus the safety margin.
	longConvo := []*LLMMessage{{
		Role: LLMMessageRoleUser,
		Content: []*LLMContentBlock{{
			Kind: LLMContentText,
			Text: strings.Repeat("x", 90_000*llmCharsPerToken),
		}},
	}}
	clamped := clampMaxTokensToContext(64000, 100_000, longConvo, nil)
	assert.Equal(t, int64(100_000-90_000-llmContextSafetyTokens), clamped)

	// Overfull window: floors at 1 rather than going non-positive.
	assert.Equal(t, int64(1), clampMaxTokensToContext(64000, 10_000, longConvo, nil))
}
