package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func toolOrderKinds(msgs []*LLMMessage) [][]LLMContentBlockKind {
	var kinds [][]LLMContentBlockKind
	for _, msg := range msgs {
		var row []LLMContentBlockKind
		for _, block := range msg.Content {
			row = append(row, block.Kind)
		}
		kinds = append(kinds, row)
	}
	return kinds
}

// A tool continuing from a conversation that appended media after the pending
// calls leaves the turn's results behind that media — the shape Anthropic
// rejected with "tool_use ids were found without tool_result blocks
// immediately after". The wire form answers the calls first.
func TestRenderMessagesForModelToolResultsFirst(t *testing.T) {
	t.Parallel()

	prompt := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "show me"}}}
	calls := &LLMMessage{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{
		{Kind: LLMContentToolCall, CallID: "shot", ToolName: "viewScreenshot"},
		{Kind: LLMContentToolCall, CallID: "log", ToolName: "readArtifact"},
	}}
	screenshot := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{
		{Kind: LLMContentText, Text: "Browser screenshot"},
		{Kind: LLMContentImage, MIMEType: "image/png", Data: "aGVsbG8="},
	}}
	shotResult := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentToolResult, CallID: "shot", Text: "Continuing from the returned conversation."}}}
	logResult := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentToolResult, CallID: "log", Text: "no such file", Errored: true}}}
	reply := &LLMMessage{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "done"}}}
	followUp := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "thanks"}}}

	stored := []*LLMMessage{prompt, calls, screenshot, shotResult, logResult, reply, followUp}
	storedCopy := append([]*LLMMessage(nil), stored...)

	rendered := renderMessagesForModel(stored)
	require.Equal(t, []*LLMMessage{prompt, calls, shotResult, logResult, screenshot, reply, followUp}, rendered,
		"results answer the calls first; the screenshot follows, and nothing else moves")
	require.Equal(t, storedCopy, stored, "rendering must not reorder the stored history")

	// OpenAI: the assistant's tool calls are followed directly by their tool
	// messages, then the screenshot as a user message.
	openAIMessages, err := convertHistoryToOpenAI(rendered)
	require.NoError(t, err)
	var roles []string
	for _, msg := range openAIMessages {
		switch {
		case msg.OfUser != nil:
			roles = append(roles, "user")
		case msg.OfAssistant != nil:
			roles = append(roles, "assistant")
		case msg.OfTool != nil:
			roles = append(roles, "tool")
		default:
			roles = append(roles, "other")
		}
	}
	require.Equal(t, []string{"user", "assistant", "tool", "tool", "user", "assistant", "user"}, roles)

	// A history already in order is returned as-is, not copied.
	inOrder := []*LLMMessage{prompt, calls, shotResult, logResult, screenshot, reply, followUp}
	again := renderMessagesForModel(inOrder)
	require.Same(t, &inOrder[0], &again[0])

	// User content after a response without tool calls is not a tool turn.
	noCalls := []*LLMMessage{prompt, reply, screenshot, followUp}
	require.Equal(t, noCalls, renderMessagesForModel(noCalls))
}

// An agent message injected mid-turn is attributed, then follows the results.
func TestRenderMessagesForModelToolResultsFirstAttributed(t *testing.T) {
	t.Parallel()

	calls := &LLMMessage{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{{Kind: LLMContentToolCall, CallID: "call", ToolName: "build"}}}
	injected := &LLMMessage{
		Role:    LLMMessageRoleUser,
		Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "status?"}},
		Origin:  &LLMMessageOrigin{Kind: LLMMessageOriginAgent, AgentName: "scout", Ref: "#1"},
	}
	result := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentToolResult, CallID: "call", Text: "ok"}}}

	rendered := renderMessagesForModel([]*LLMMessage{calls, injected, result})
	require.Len(t, rendered, 3)
	require.Same(t, result, rendered[1])
	require.Equal(t, "[message #1 from agent \"scout\"]\n\nstatus?", rendered[2].TextContent())
}

// A message mixing results and other content leads with its results, and the
// message count never changes, so recordings stay index-aligned.
func TestRenderMessagesForModelToolResultsFirstMixed(t *testing.T) {
	t.Parallel()

	calls := &LLMMessage{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{{Kind: LLMContentToolCall, CallID: "call", ToolName: "build"}}}
	mixed := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{
		{Kind: LLMContentText, Text: "note"},
		{Kind: LLMContentToolResult, CallID: "call", Text: "ok"},
	}}
	before := mixed.Clone()

	rendered := renderMessagesForModel([]*LLMMessage{calls, mixed})
	require.Len(t, rendered, 2)
	require.Equal(t, [][]LLMContentBlockKind{
		{LLMContentToolCall},
		{LLMContentToolResult, LLMContentText},
	}, toolOrderKinds(rendered))
	require.Equal(t, before, mixed, "rendering must not mutate stored content")
}

// A recording stored in the old order still replays: both sides of the
// comparison are rendered the same way.
func TestRecordedResponseToolResultsFirst(t *testing.T) {
	msgs := []*LLMMessage{
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "show me"}}},
		{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{{Kind: LLMContentToolCall, CallID: "shot", ToolName: "viewScreenshot"}}},
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{
			{Kind: LLMContentText, Text: "Browser screenshot"},
			{Kind: LLMContentImage, MIMEType: "image/png", Data: "aGVsbG8="},
		}},
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentToolResult, CallID: "shot", Text: "Continuing from the returned conversation."}}},
		{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "a picture"}}},
	}
	_, ctx := recordingTestRecorder(t)
	response, err := newRecordedResponseProvider(msgs).SendQuery(ctx, renderMessagesForModel(msgs[:4]), nil, nil)
	require.NoError(t, err)
	require.Equal(t, "a picture", response.TextContent())
}
