package modelcatalog_test

import (
	"testing"

	"github.com/dagger/dagger/core/modelcatalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeModelID(t *testing.T) {
	for input, want := range map[string]string{
		"claude-sonnet-4-5":           "claude-sonnet-4-5",
		"claude-sonnet-4-5-20250929":  "claude-sonnet-4-5",
		"anthropic/claude-sonnet-4-5": "claude-sonnet-4-5",
		"claude-fable-5[1m]":          "claude-fable-5",
		"claude-3-5-sonnet-latest":    "claude-3-5-sonnet",
		"openai/gpt-5.6-sol":          "gpt-5.6-sol",
	} {
		assert.Equal(t, want, modelcatalog.NormalizeModelID(input), "NormalizeModelID(%q)", input)
	}
}

func TestLookup(t *testing.T) {
	// Known catalogued model, in a few equivalent spellings.
	for _, id := range []string{
		"claude-sonnet-4-5",
		"claude-sonnet-4-5-20250929",
		"anthropic/claude-sonnet-4-5",
	} {
		m, ok := modelcatalog.Lookup("anthropic", id)
		require.True(t, ok, "expected %q to resolve", id)
		assert.Equal(t, int64(200000), m.ContextWindow)
	}

	// openai-codex shares the OpenAI catalog.
	_, ok := modelcatalog.Lookup("openai-codex", "gpt-5.6-sol")
	assert.True(t, ok)

	// Unknown provider and unknown model both miss.
	_, ok = modelcatalog.Lookup("local", "qwen3")
	assert.False(t, ok)
	_, ok = modelcatalog.Lookup("anthropic", "totally-unknown-model")
	assert.False(t, ok)
}

func TestCost(t *testing.T) {
	// claude-sonnet-4-5: $3/1M in, $15/1M out, $3.75/1M cache-write, $0.30/1M
	// cache-read. 1M of each exercises the per-field mapping directly.
	const M = 1_000_000
	got := modelcatalog.Cost("anthropic", "claude-sonnet-4-5", M, M, M, M)
	// 3 (in) + 15 (out) + 0.30 (cache read) + 3.75 (cache write)
	assert.InDelta(t, 3+15+0.30+3.75, got, 1e-9)

	// Cache reads are far cheaper than cache writes: swapping them changes the
	// total, proving the read/write fields aren't transposed.
	readHeavy := modelcatalog.Cost("anthropic", "claude-sonnet-4-5", 0, 0, M, 0)
	writeHeavy := modelcatalog.Cost("anthropic", "claude-sonnet-4-5", 0, 0, 0, M)
	assert.InDelta(t, 0.30, readHeavy, 1e-9)
	assert.InDelta(t, 3.75, writeHeavy, 1e-9)
	assert.Less(t, readHeavy, writeHeavy)

	// Uncatalogued model / unknown provider costs nothing.
	assert.Zero(t, modelcatalog.Cost("local", "qwen3", M, M, M, M))
}
