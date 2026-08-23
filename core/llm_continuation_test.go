package core

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
)

func textMsg(role LLMMessageRole, text string) *LLMMessage {
	return &LLMMessage{
		Role:    role,
		Content: []*LLMContentBlock{{Kind: LLMContentText, Text: text}},
	}
}

func TestHistoryPreserved(t *testing.T) {
	base := []*LLMMessage{
		textMsg(LLMMessageRoleUser, "hello"),
		textMsg(LLMMessageRoleAssistant, "hi"),
	}

	// historyPreserved is not a gate — nothing is rejected for failing it. It
	// only decides whether the adoption summary tells the model its history
	// changed shape.
	t.Run("identical history is preserved", func(t *testing.T) {
		require.True(t, historyPreserved(&LLM{Messages: base}, &LLM{Messages: base}))
	})

	t.Run("appended history is preserved", func(t *testing.T) {
		next := append(append([]*LLMMessage{}, base...), textMsg(LLMMessageRoleUser, "more"))
		require.True(t, historyPreserved(&LLM{Messages: base}, &LLM{Messages: next}))
	})

	t.Run("equal-by-value (not pointer) history is preserved", func(t *testing.T) {
		// A history that round-tripped through the API is rebuilt from fresh
		// structs, so the comparison must be on content, not identity.
		rebuilt := []*LLMMessage{
			textMsg(LLMMessageRoleUser, "hello"),
			textMsg(LLMMessageRoleAssistant, "hi"),
		}
		require.True(t, historyPreserved(&LLM{Messages: base}, &LLM{Messages: rebuilt}))
	})

	t.Run("fresh conversation is not preserved", func(t *testing.T) {
		require.False(t, historyPreserved(&LLM{Messages: base}, &LLM{}))
	})

	t.Run("truncated history is not preserved", func(t *testing.T) {
		require.False(t, historyPreserved(&LLM{Messages: base}, &LLM{Messages: base[:1]}))
	})

	t.Run("rewritten history is not preserved", func(t *testing.T) {
		rewritten := []*LLMMessage{
			textMsg(LLMMessageRoleUser, "hello"),
			textMsg(LLMMessageRoleAssistant, "something else entirely"),
		}
		require.False(t, historyPreserved(&LLM{Messages: base}, &LLM{Messages: rewritten}))
	})

	t.Run("empty current is preserved by anything", func(t *testing.T) {
		require.True(t, historyPreserved(&LLM{}, &LLM{Messages: base}))
	})

	t.Run("nil is never preserved", func(t *testing.T) {
		require.False(t, historyPreserved(nil, &LLM{Messages: base}))
		require.False(t, historyPreserved(&LLM{Messages: base}, nil))
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

func TestSummarizeContinuation(t *testing.T) {
	tools := []LLMTool{{Name: "read"}}
	base := []*LLMMessage{
		textMsg(LLMMessageRoleUser, "hello"),
		textMsg(LLMMessageRoleAssistant, "hi"),
	}

	t.Run("preserved history says nothing extra", func(t *testing.T) {
		next := append(append([]*LLMMessage{}, base...), textMsg(LLMMessageRoleUser, "more"))
		out := summarizeContinuation(&LLM{Messages: base}, &LLM{Messages: next}, tools, tools)
		require.NotContains(t, out, "Conversation history replaced")
	})

	t.Run("replaced history is reported", func(t *testing.T) {
		out := summarizeContinuation(
			&LLM{Messages: base},
			&LLM{Messages: []*LLMMessage{textMsg(LLMMessageRoleUser, "summary so far")}},
			tools, tools,
		)
		require.Contains(t, out, "Conversation history replaced: 2 messages -> 1 messages.")
		// The toolset diff is still there: the two notices compose.
		require.Contains(t, out, "Toolset unchanged (1 tools).")
	})
}

func TestToolResultSelectors(t *testing.T) {
	toolCallMsg := func(callID, name string) *LLMMessage {
		return &LLMMessage{
			Role: LLMMessageRoleAssistant,
			Content: []*LLMContentBlock{{
				Kind:     LLMContentToolCall,
				CallID:   callID,
				ToolName: name,
			}},
		}
	}
	resultMsg := func(callID, text string) *LLMMessage {
		return &LLMMessage{
			Role: LLMMessageRoleUser,
			Content: []*LLMContentBlock{{
				Kind:   LLMContentToolResult,
				CallID: callID,
				Text:   text,
			}},
		}
	}
	names := map[string]string{"call-1": "reload"}

	t.Run("result whose call is in the adopted history appends normally", func(t *testing.T) {
		target := &LLM{Messages: []*LLMMessage{toolCallMsg("call-1", "reload")}}
		sels := toolResultSelectors(target, []*LLMMessage{resultMsg("call-1", "ok")}, names)
		require.Len(t, sels, 1)
		require.Equal(t, "withToolResult", sels[0].Field)
		require.Equal(t, dagql.NewString("call-1"), sels[0].Args[0].Value)
	})

	t.Run("orphaned result degrades to a user message", func(t *testing.T) {
		// The adopted conversation dropped the call (self-compaction,
		// summarize-and-restart): a tool_result block with no matching tool_use
		// would be protocol-invalid, so the information is carried as prose.
		target := &LLM{Messages: []*LLMMessage{textMsg(LLMMessageRoleUser, "summary so far")}}
		sels := toolResultSelectors(target, []*LLMMessage{resultMsg("call-1", "ok")}, names)
		require.Len(t, sels, 1)
		require.Equal(t, "withPrompt", sels[0].Field)
		require.Equal(t, "prompt", sels[0].Args[0].Name)
		require.Equal(t, dagql.NewString("[continued via tool reload]\nok"), sels[0].Args[0].Value)
	})

	t.Run("unknown tool name falls back to the call ID", func(t *testing.T) {
		target := &LLM{}
		sels := toolResultSelectors(target, []*LLMMessage{resultMsg("call-9", "ok")}, names)
		require.Len(t, sels, 1)
		require.Equal(t, "withPrompt", sels[0].Field)
		require.Equal(t, dagql.NewString("[continued via tool call-9]\nok"), sels[0].Args[0].Value)
	})

	t.Run("a nil target keeps tool results as tool results", func(t *testing.T) {
		sels := toolResultSelectors(nil, []*LLMMessage{resultMsg("call-1", "ok")}, names)
		require.Len(t, sels, 1)
		require.Equal(t, "withToolResult", sels[0].Field)
	})
}
