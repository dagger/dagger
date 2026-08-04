package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func textMsg(role LLMMessageRole, text string) *LLMMessage {
	return &LLMMessage{
		Role:    role,
		Content: []*LLMContentBlock{{Kind: LLMContentText, Text: text}},
	}
}

func TestContinuesHistory(t *testing.T) {
	base := []*LLMMessage{
		textMsg(LLMMessageRoleUser, "hello"),
		textMsg(LLMMessageRoleAssistant, "hi"),
	}

	t.Run("identical history continues", func(t *testing.T) {
		require.True(t, continuesHistory(&LLM{Messages: base}, &LLM{Messages: base}))
	})

	t.Run("appended history continues", func(t *testing.T) {
		next := append(append([]*LLMMessage{}, base...), textMsg(LLMMessageRoleUser, "more"))
		require.True(t, continuesHistory(&LLM{Messages: base}, &LLM{Messages: next}))
	})

	t.Run("equal-by-value (not pointer) history continues", func(t *testing.T) {
		// A history that round-tripped through the API is rebuilt from fresh
		// structs, so the check must compare content, not identity.
		rebuilt := []*LLMMessage{
			textMsg(LLMMessageRoleUser, "hello"),
			textMsg(LLMMessageRoleAssistant, "hi"),
		}
		require.True(t, continuesHistory(&LLM{Messages: base}, &LLM{Messages: rebuilt}))
	})

	t.Run("fresh conversation does not continue", func(t *testing.T) {
		require.False(t, continuesHistory(&LLM{Messages: base}, &LLM{}))
	})

	t.Run("truncated history does not continue", func(t *testing.T) {
		require.False(t, continuesHistory(&LLM{Messages: base}, &LLM{Messages: base[:1]}))
	})

	t.Run("rewritten history does not continue", func(t *testing.T) {
		rewritten := []*LLMMessage{
			textMsg(LLMMessageRoleUser, "hello"),
			textMsg(LLMMessageRoleAssistant, "something else entirely"),
		}
		require.False(t, continuesHistory(&LLM{Messages: base}, &LLM{Messages: rewritten}))
	})

	t.Run("empty current continues into anything", func(t *testing.T) {
		require.True(t, continuesHistory(&LLM{}, &LLM{Messages: base}))
	})

	t.Run("nil is never a continuation", func(t *testing.T) {
		require.False(t, continuesHistory(nil, &LLM{Messages: base}))
		require.False(t, continuesHistory(&LLM{Messages: base}, nil))
	})
}

func TestMessagesEqual(t *testing.T) {
	toolCall := func(args string) *LLMMessage {
		return &LLMMessage{
			Role: LLMMessageRoleAssistant,
			Content: []*LLMContentBlock{{
				Kind:      LLMContentToolCall,
				CallID:    "call-1",
				ToolName:  "install",
				Arguments: JSON(args),
			}},
		}
	}
	require.True(t, messagesEqual(toolCall(`{"ref":"./m"}`), toolCall(`{"ref":"./m"}`)))
	require.False(t, messagesEqual(toolCall(`{"ref":"./m"}`), toolCall(`{"ref":"./n"}`)))

	// Role, block count, and every block field participate.
	require.False(t, messagesEqual(
		textMsg(LLMMessageRoleUser, "x"),
		textMsg(LLMMessageRoleAssistant, "x"),
	))
	require.False(t, messagesEqual(
		&LLMMessage{Role: LLMMessageRoleUser},
		textMsg(LLMMessageRoleUser, "x"),
	))
	require.False(t, messagesEqual(
		&LLMMessage{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{{
			Kind: LLMContentThinking, Text: "t", Signature: "sig-a",
		}}},
		&LLMMessage{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{{
			Kind: LLMContentThinking, Text: "t", Signature: "sig-b",
		}}},
	))
}

func TestSummarizeToolsetChange(t *testing.T) {
	tools := func(names ...string) []LLMTool {
		out := make([]LLMTool, len(names))
		for i, n := range names {
			out[i] = LLMTool{Name: n}
		}
		return out
	}

	t.Run("added tools are listed", func(t *testing.T) {
		out := summarizeToolsetChange(tools("read", "write"), tools("read", "write", "zoom", "screen"))
		require.Contains(t, out, "Continuing from the returned conversation.")
		require.Contains(t, out, "Tools added: screen, zoom")
		require.NotContains(t, out, "Tools removed")
	})

	t.Run("removed tools are listed", func(t *testing.T) {
		out := summarizeToolsetChange(tools("read", "write"), tools("read"))
		require.Contains(t, out, "Tools removed: write")
	})

	t.Run("unchanged toolset reports its size", func(t *testing.T) {
		out := summarizeToolsetChange(tools("read", "write"), tools("write", "read"))
		require.Contains(t, out, "Toolset unchanged (2 tools).")
	})

	t.Run("unknown previous toolset reports everything as added", func(t *testing.T) {
		out := summarizeToolsetChange(nil, tools("read"))
		require.Contains(t, out, "Tools added: read")
	})
}
