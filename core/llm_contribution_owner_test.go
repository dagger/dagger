package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLLMWithContributionOwner(t *testing.T) {
	base, err := (&Query{}).NewLLM(t.Context(), "test-model", "")
	require.NoError(t, err)
	llm := base.WithSystemPromptOwner("own", "entry").
		WithPrompt("user history").
		WithSystemPromptOwner("dependency", "donor")
	llm.mcp.skillDirs = []ownedSkillDirectory{{Owner: "entry"}, {Owner: "donor"}}

	claimed, err := llm.WithContributionOwner("entry", []int{2}, []int{1})
	require.NoError(t, err)
	require.Equal(t, "entry", claimed.Messages[2].CompositionOwner)
	require.Equal(t, "entry", claimed.mcp.skillDirs[1].Owner)
	require.Equal(t, "donor", llm.Messages[2].CompositionOwner, "claiming must not mutate the original")
	require.Equal(t, "donor", llm.mcp.skillDirs[1].Owner, "claiming must not mutate the original")

	// What the entry's module owns is what refreshing it clears.
	cleared := claimed.WithoutComposition("entry")
	require.Len(t, cleared.Messages, 1)
	require.Equal(t, "user history", cleared.Messages[0].TextContent())
	require.Empty(t, cleared.mcp.skillDirs)

	_, err = llm.WithContributionOwner("entry", []int{1}, nil)
	require.ErrorContains(t, err, "not a system prompt")
	_, err = llm.WithContributionOwner("entry", []int{3}, nil)
	require.ErrorContains(t, err, "out of range")
	_, err = llm.WithContributionOwner("entry", nil, []int{2})
	require.ErrorContains(t, err, "out of range")
}
