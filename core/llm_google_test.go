package core

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

func TestDecodeThoughtSignature(t *testing.T) {
	raw := []byte("some-opaque-signature")
	encoded := base64.StdEncoding.EncodeToString(raw)

	assert.Equal(t, raw, decodeThoughtSignature(encoded))
	assert.Nil(t, decodeThoughtSignature(""))
	assert.Nil(t, decodeThoughtSignature("not valid base64!!"))
}

func TestGenaiThinkingConfig(t *testing.T) {
	newClient := func(effort string) *GenaiClient {
		return &GenaiClient{endpoint: &LLMEndpoint{ReasoningEffort: effort}}
	}

	// Empty / "none" effort yields no config.
	for _, effort := range []string{"", "none"} {
		assert.Nil(t, newClient(effort).thinkingConfig(), "effort %q should disable thinking", effort)
	}

	// A real effort requests thought summaries and maps to Gemini's uppercase
	// thinking level.
	cfg := newClient("high").thinkingConfig()
	require.NotNil(t, cfg)
	assert.True(t, cfg.IncludeThoughts)
	assert.Equal(t, genai.ThinkingLevelHigh, cfg.ThinkingLevel)

	cfg = newClient("low").thinkingConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, genai.ThinkingLevelLow, cfg.ThinkingLevel)
}

// TestGenaiThinkingRoundTrip exercises the send path: a captured thinking block
// and a tool call with a thought signature must be replayed to Gemini as a
// thought part and a function-call part carrying their signatures.
func TestGenaiThinkingRoundTrip(t *testing.T) {
	c := &GenaiClient{endpoint: &LLMEndpoint{}}

	toolSig := []byte("tool-call-signature")
	thinkSig := []byte("thinking-signature")

	history := []*LLMMessage{
		{
			Role: LLMMessageRoleUser,
			Content: []*LLMContentBlock{
				{Kind: LLMContentText, Text: "hello"},
			},
		},
		{
			Role: LLMMessageRoleAssistant,
			Content: []*LLMContentBlock{
				{Kind: LLMContentThinking, Text: "let me think", Signature: base64.StdEncoding.EncodeToString(thinkSig)},
				{Kind: LLMContentText, Text: "on it"},
				{Kind: LLMContentToolCall, CallID: "call-1", ToolName: "do_thing", Arguments: JSON(`{"x":1}`), Signature: base64.StdEncoding.EncodeToString(toolSig)},
			},
		},
	}

	genaiHistory, _, err := c.prepareGenaiHistory(history)
	require.NoError(t, err)
	require.Len(t, genaiHistory, 2)

	model := genaiHistory[1]
	assert.Equal(t, "model", model.Role)
	require.Len(t, model.Parts, 3)

	// thought part, replayed with signature and Thought=true
	assert.True(t, model.Parts[0].Thought)
	assert.Equal(t, "let me think", model.Parts[0].Text)
	assert.Equal(t, thinkSig, model.Parts[0].ThoughtSignature)

	// plain reply text
	assert.False(t, model.Parts[1].Thought)
	assert.Equal(t, "on it", model.Parts[1].Text)

	// function call, replayed with its thought signature
	require.NotNil(t, model.Parts[2].FunctionCall)
	assert.Equal(t, "do_thing", model.Parts[2].FunctionCall.Name)
	assert.Equal(t, toolSig, model.Parts[2].ThoughtSignature)
}

// TestGenaiThinkingCapture exercises the response path: thought parts are
// captured as thinking blocks (with their signature) and kept out of the
// visible reply, while function-call signatures are preserved.
func TestGenaiThinkingCapture(t *testing.T) {
	c := &GenaiClient{endpoint: &LLMEndpoint{}}

	thinkSig := []byte("thinking-signature")
	toolSig := []byte("tool-call-signature")

	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{
				Role: "model",
				Parts: []*genai.Part{
					{Text: "reasoning...", Thought: true, ThoughtSignature: thinkSig},
					{Text: "the answer"},
					{FunctionCall: &genai.FunctionCall{Name: "do_thing", Args: map[string]any{"x": float64(1)}}, ThoughtSignature: toolSig},
				},
			},
		}},
	}

	stream := func(yield func(*genai.GenerateContentResponse, error) bool) {
		yield(resp, nil)
	}
	noUsage := func(*genai.GenerateContentResponseUsageMetadata) LLMTokenUsage { return LLMTokenUsage{} }

	blocks, _, err := c.processStreamResponse(stream, newDisplayPhases(context.Background(), ""), noUsage)
	require.NoError(t, err)
	require.Len(t, blocks, 3)

	// Order is thinking, text, tool call.
	assert.Equal(t, LLMContentThinking, blocks[0].Kind)
	assert.Equal(t, "reasoning...", blocks[0].Text)
	assert.Equal(t, base64.StdEncoding.EncodeToString(thinkSig), blocks[0].Signature)

	assert.Equal(t, LLMContentText, blocks[1].Kind)
	assert.Equal(t, "the answer", blocks[1].Text, "thought text must not leak into the reply")

	assert.Equal(t, LLMContentToolCall, blocks[2].Kind)
	assert.Equal(t, "do_thing", blocks[2].ToolName)
	assert.Equal(t, base64.StdEncoding.EncodeToString(toolSig), blocks[2].Signature)
}

// TestGenaiParallelToolCallIDs covers B4: two parallel calls to the same tool,
// neither with a Gemini-provided ID, must get distinct CallIDs so they don't
// collide in the display map or their result association.
func TestGenaiParallelToolCallIDs(t *testing.T) {
	c := &GenaiClient{endpoint: &LLMEndpoint{}}

	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{
				Role: "model",
				Parts: []*genai.Part{
					{FunctionCall: &genai.FunctionCall{Name: "do_thing", Args: map[string]any{"x": float64(1)}}},
					{FunctionCall: &genai.FunctionCall{Name: "do_thing", Args: map[string]any{"x": float64(2)}}},
				},
			},
		}},
	}
	stream := func(yield func(*genai.GenerateContentResponse, error) bool) { yield(resp, nil) }
	noUsage := func(*genai.GenerateContentResponseUsageMetadata) LLMTokenUsage { return LLMTokenUsage{} }

	blocks, _, err := c.processStreamResponse(stream, newDisplayPhases(context.Background(), ""), noUsage)
	require.NoError(t, err)
	require.Len(t, blocks, 2)

	assert.Equal(t, "do_thing", blocks[0].ToolName)
	assert.Equal(t, "do_thing", blocks[1].ToolName)
	assert.NotEmpty(t, blocks[0].CallID)
	assert.NotEmpty(t, blocks[1].CallID)
	assert.NotEqual(t, blocks[0].CallID, blocks[1].CallID, "parallel same-tool calls must get distinct CallIDs")
}

// TestGenaiToolCallUsesProvidedID covers B4: when Gemini populates the
// function-call ID, it is used as the CallID.
func TestGenaiToolCallUsesProvidedID(t *testing.T) {
	c := &GenaiClient{endpoint: &LLMEndpoint{}}

	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{
				Role: "model",
				Parts: []*genai.Part{
					{FunctionCall: &genai.FunctionCall{ID: "gemini-id-1", Name: "do_thing", Args: map[string]any{}}},
				},
			},
		}},
	}
	stream := func(yield func(*genai.GenerateContentResponse, error) bool) { yield(resp, nil) }
	noUsage := func(*genai.GenerateContentResponseUsageMetadata) LLMTokenUsage { return LLMTokenUsage{} }

	blocks, _, err := c.processStreamResponse(stream, newDisplayPhases(context.Background(), ""), noUsage)
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	assert.Equal(t, "gemini-id-1", blocks[0].CallID)
}

// TestGenaiToolCallIDRoundTrip covers B4: the CallID assigned on the response is
// replayed as both the function-call ID and the matching function-response ID,
// while the response still carries the real tool name.
func TestGenaiToolCallIDRoundTrip(t *testing.T) {
	c := &GenaiClient{endpoint: &LLMEndpoint{}}

	history := []*LLMMessage{
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "hi"}}},
		{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{
			{Kind: LLMContentToolCall, CallID: "do_thing-2", ToolName: "do_thing", Arguments: JSON(`{"x":1}`)},
		}},
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{
			{Kind: LLMContentToolResult, CallID: "do_thing-2", Text: "ok"},
		}},
	}

	genaiHistory, _, err := c.prepareGenaiHistory(history)
	require.NoError(t, err)
	require.Len(t, genaiHistory, 3)

	model := genaiHistory[1]
	require.Len(t, model.Parts, 1)
	require.NotNil(t, model.Parts[0].FunctionCall)
	assert.Equal(t, "do_thing-2", model.Parts[0].FunctionCall.ID)
	assert.Equal(t, "do_thing", model.Parts[0].FunctionCall.Name)

	user := genaiHistory[2]
	require.Len(t, user.Parts, 1)
	require.NotNil(t, user.Parts[0].FunctionResponse)
	assert.Equal(t, "do_thing-2", user.Parts[0].FunctionResponse.ID)
	assert.Equal(t, "do_thing", user.Parts[0].FunctionResponse.Name)
}

// TestGenaiTextAnswerSignature covers B3(a): a thought signature on a
// text-answer part is captured on the TEXT block and replayed on the text part.
func TestGenaiTextAnswerSignature(t *testing.T) {
	c := &GenaiClient{endpoint: &LLMEndpoint{}}

	answerSig := []byte("answer-signature")
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{
				Role: "model",
				Parts: []*genai.Part{
					{Text: "the answer", ThoughtSignature: answerSig},
				},
			},
		}},
	}
	stream := func(yield func(*genai.GenerateContentResponse, error) bool) { yield(resp, nil) }
	noUsage := func(*genai.GenerateContentResponseUsageMetadata) LLMTokenUsage { return LLMTokenUsage{} }

	blocks, _, err := c.processStreamResponse(stream, newDisplayPhases(context.Background(), ""), noUsage)
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	assert.Equal(t, LLMContentText, blocks[0].Kind)
	assert.Equal(t, "the answer", blocks[0].Text)
	assert.Equal(t, base64.StdEncoding.EncodeToString(answerSig), blocks[0].Signature, "text-answer signature must be captured")

	// And it round-trips onto the replayed text part.
	history := []*LLMMessage{
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "hi"}}},
		{Role: LLMMessageRoleAssistant, Content: blocks},
	}
	genaiHistory, _, err := c.prepareGenaiHistory(history)
	require.NoError(t, err)
	require.Len(t, genaiHistory, 2)
	model := genaiHistory[1]
	require.Len(t, model.Parts, 1)
	assert.Equal(t, "the answer", model.Parts[0].Text)
	assert.Equal(t, answerSig, model.Parts[0].ThoughtSignature)
}

// TestGenaiMultipleThinkingSegments covers B3(b): distinct thought signatures on
// separate thinking segments are preserved per-block instead of collapsing into
// one block with only the last signature.
func TestGenaiMultipleThinkingSegments(t *testing.T) {
	c := &GenaiClient{endpoint: &LLMEndpoint{}}

	sig1 := []byte("sig-1")
	sig2 := []byte("sig-2")
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{
				Role: "model",
				Parts: []*genai.Part{
					{Text: "first thought", Thought: true, ThoughtSignature: sig1},
					{FunctionCall: &genai.FunctionCall{Name: "a", Args: map[string]any{}}},
					{Text: "second thought", Thought: true, ThoughtSignature: sig2},
					{FunctionCall: &genai.FunctionCall{Name: "b", Args: map[string]any{}}},
				},
			},
		}},
	}
	stream := func(yield func(*genai.GenerateContentResponse, error) bool) { yield(resp, nil) }
	noUsage := func(*genai.GenerateContentResponseUsageMetadata) LLMTokenUsage { return LLMTokenUsage{} }

	blocks, _, err := c.processStreamResponse(stream, newDisplayPhases(context.Background(), ""), noUsage)
	require.NoError(t, err)
	require.Len(t, blocks, 4)

	assert.Equal(t, LLMContentThinking, blocks[0].Kind)
	assert.Equal(t, base64.StdEncoding.EncodeToString(sig1), blocks[0].Signature)
	assert.Equal(t, LLMContentToolCall, blocks[1].Kind)
	assert.Equal(t, "a", blocks[1].ToolName)
	assert.Equal(t, LLMContentThinking, blocks[2].Kind)
	assert.Equal(t, base64.StdEncoding.EncodeToString(sig2), blocks[2].Signature, "second thinking segment's signature must be preserved")
	assert.Equal(t, LLMContentToolCall, blocks[3].Kind)
	assert.Equal(t, "b", blocks[3].ToolName)
}
