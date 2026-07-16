package daggercmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrimConversationForSummary(t *testing.T) {
	budgetChars := (summaryContextWindowTokens - summaryReserveTokens) * summaryCharsPerToken

	// Short conversations pass through untouched.
	short := "[User]: hi\n\n[Assistant]: hello"
	assert.Equal(t, short, trimConversationForSummary(short))

	// Oversized conversations keep the newest messages within budget and
	// note the omission.
	var parts []string
	for range 2000 {
		parts = append(parts, "[User]: "+strings.Repeat("x", 500))
	}
	parts = append(parts, "[Assistant]: the newest message")
	long := strings.Join(parts, "\n\n")
	require.Greater(t, len(long), budgetChars)

	trimmed := trimConversationForSummary(long)
	assert.LessOrEqual(t, len(trimmed), budgetChars+100)
	assert.True(t, strings.HasPrefix(trimmed, "[Earlier conversation omitted"))
	assert.Contains(t, trimmed, "the newest message")

	// A single message larger than the whole budget keeps its tail.
	giant := "[Tool result]: " + strings.Repeat("y", budgetChars*2) + "END"
	trimmed = trimConversationForSummary(giant)
	assert.LessOrEqual(t, len(trimmed), budgetChars+100)
	assert.True(t, strings.HasPrefix(trimmed, "[Earlier conversation omitted"))
	assert.True(t, strings.HasSuffix(trimmed, "END"))
}
