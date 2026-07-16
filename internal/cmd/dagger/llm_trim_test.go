package daggercmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrimConversationForSummary(t *testing.T) {
	fallbackBudgetChars := (summaryFallbackWindowTokens - summaryReserveTokens) * summaryCharsPerToken

	// Short conversations pass through untouched.
	short := "[User]: hi\n\n[Assistant]: hello"
	assert.Equal(t, short, trimConversationForSummary(short, 0))
	assert.Equal(t, short, trimConversationForSummary(short, 200000))

	// Oversized conversations keep the newest messages within budget and
	// note the omission. Window 0 falls back to the conservative default.
	var parts []string
	for range 2000 {
		parts = append(parts, "[User]: "+strings.Repeat("x", 500))
	}
	parts = append(parts, "[Assistant]: the newest message")
	long := strings.Join(parts, "\n\n")
	require.Greater(t, len(long), fallbackBudgetChars)

	trimmed := trimConversationForSummary(long, 0)
	assert.LessOrEqual(t, len(trimmed), fallbackBudgetChars+100)
	assert.True(t, strings.HasPrefix(trimmed, "[Earlier conversation omitted"))
	assert.Contains(t, trimmed, "the newest message")

	// A larger real window keeps more of the conversation.
	roomier := trimConversationForSummary(long, 500000)
	assert.Greater(t, len(roomier), len(trimmed))
	assert.Contains(t, roomier, "the newest message")

	// A window at or below the reserve still keeps a minimal budget.
	tiny := trimConversationForSummary(long, summaryReserveTokens)
	minBudgetChars := summaryReserveTokens * summaryCharsPerToken
	assert.LessOrEqual(t, len(tiny), minBudgetChars+100)
	assert.Contains(t, tiny, "the newest message")

	// A single message larger than the whole budget keeps its tail.
	giant := "[Tool result]: " + strings.Repeat("y", fallbackBudgetChars*2) + "END"
	trimmed = trimConversationForSummary(giant, 0)
	assert.LessOrEqual(t, len(trimmed), fallbackBudgetChars+100)
	assert.True(t, strings.HasPrefix(trimmed, "[Earlier conversation omitted"))
	assert.True(t, strings.HasSuffix(trimmed, "END"))
}
