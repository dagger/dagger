package core

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestMCPToolResultPreservesNativeMedia(t *testing.T) {
	wire := `{
		"content":[
			{"type":"text","text":"before"},
			{"type":"image","mimeType":"image/png","data":"aW1hZ2U=","annotations":{"audience":["assistant"]}},
			{"type":"text","text":"between"},
			{"type":"audio","mimeType":"audio/wav","data":"YXVkaW8="},
			{"type":"resource","resource":{"uri":"urn:test:pdf","mimeType":"application/pdf","blob":"cGRm"}},
			{"type":"resource","resource":{"uri":"urn:test:text","mimeType":"text/plain","text":"resource text"}},
			{"type":"resource_link","uri":"file:///tmp/report.pdf","name":"report","description":"PDF report","mimeType":"application/pdf"}
		],
		"structuredContent":{"status":"partial"},
		"isError":true,
		"_meta":{"trace":"test"}
	}`
	var input mcpsdk.CallToolResult
	require.NoError(t, json.Unmarshal([]byte(wire), &input))
	result, err := mcpToolResult(&input)
	require.NoError(t, err)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.JSONEq(t, wire, string(encoded))

	// Do not round structured output through float64 while bridging SDKs.
	input.StructuredContent = map[string]any{"sequence": int64(9007199254740993)}
	result, err = mcpToolResult(&input)
	require.NoError(t, err)
	encoded, err = json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	require.Equal(t, `{"sequence":9007199254740993}`, string(encoded))
}

func TestMCPToolResultPreservesTypedMedia(t *testing.T) {
	input := &LLMContentBlock{
		Kind:    LLMContentToolResult,
		CallID:  "call-1",
		Text:    "legacy",
		Errored: true,
		Content: []*LLMContentBlock{
			{Kind: LLMContentImage, MIMEType: "image/png", Data: "aW1hZ2U="},
			{Kind: LLMContentText, Text: "caption"},
			{Kind: LLMContentAudio, MIMEType: "audio/wav", Data: "YXVkaW8="},
			{Kind: LLMContentDocument, MIMEType: "application/pdf", Data: "cGRm"},
		},
	}
	for _, input := range []any{input, *input, []*LLMContentBlock{input}} {
		result, err := mcpToolResult(input)
		require.NoError(t, err)
		encoded, err := json.Marshal(result)
		require.NoError(t, err)
		require.JSONEq(t, `{"isError":true,"content":[
			{"type":"text","text":"legacy"},
			{"type":"image","mimeType":"image/png","data":"aW1hZ2U="},
			{"type":"text","text":"caption"},
			{"type":"audio","mimeType":"audio/wav","data":"YXVkaW8="},
			{"type":"resource","resource":{"uri":"urn:dagger:tool-result:4","mimeType":"application/pdf","blob":"cGRm"}}
		]}`, string(encoded))
	}

	// A block slice is native even without a TOOL_RESULT wrapper.
	result, err := mcpToolResult(input.Content)
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Len(t, result.Content, 4)
	image, ok := mcp.AsImageContent(result.Content[0])
	require.True(t, ok)
	require.Equal(t, "aW1hZ2U=", image.Data)
}

func TestMCPToolResultPlainValues(t *testing.T) {
	for _, tc := range []struct {
		input any
		text  string
	}{
		{"hello", "hello"},
		{map[string]any{"type": "image", "data": "ordinary object"}, `{"data":"ordinary object","type":"image"}`},
		{nil, "null"},
	} {
		result, err := mcpToolResult(tc.input)
		require.NoError(t, err)
		require.False(t, result.IsError)
		require.Len(t, result.Content, 1)
		text, ok := mcp.AsTextContent(result.Content[0])
		require.True(t, ok)
		require.Equal(t, tc.text, text.Text)
	}
}

func TestMCPToolResultRejectsInvalidTypedContent(t *testing.T) {
	for _, input := range []any{
		(*mcpsdk.CallToolResult)(nil),
		(*LLMContentBlock)(nil),
		&LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/png", Data: "invalid!"},
		&LLMContentBlock{Kind: LLMContentThinking, Text: "not tool output"},
		&LLMContentBlock{Kind: "UNKNOWN"},
		&LLMContentBlock{Kind: LLMContentToolResult, Content: []*LLMContentBlock{{Kind: LLMContentToolCall}}},
	} {
		result, err := mcpToolResult(input)
		require.Error(t, err)
		require.Nil(t, result)
	}
}
