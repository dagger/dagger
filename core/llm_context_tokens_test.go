package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEstimateContextTokensUsesLastUsageAndTrailingMessages(t *testing.T) {
	messages := []*LLMMessage{
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "hello"}}},
		{
			Role:    LLMMessageRoleAssistant,
			Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "world"}},
			TokenUsage: &LLMTokenUsage{
				InputTokens:      100,
				OutputTokens:     20,
				CachedTokenReads: 30,
				TotalTokens:      150,
			},
		},
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentToolResult, Text: "12345678"}}},
	}

	require.Equal(t, int64(152), estimateOccupiedContextTokens(messages))
}

func TestEstimateContextTokensFallsBackToMessageEstimates(t *testing.T) {
	messages := []*LLMMessage{
		{Role: LLMMessageRoleSystem, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "12345678"}}},
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "1234"}}},
	}

	require.Equal(t, int64(3), estimateOccupiedContextTokens(messages))
}
