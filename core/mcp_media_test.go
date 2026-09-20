package core

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestMCPMediaContent(t *testing.T) {
	data := []byte("binary media payload")
	encoded := base64.StdEncoding.EncodeToString(data)
	t.Run("mixed content and structured data", func(t *testing.T) {
		result := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "before"},
				&mcp.ImageContent{Data: data, MIMEType: "image/png"},
				&mcp.TextContent{Text: "between"},
				&mcp.AudioContent{Data: data, MIMEType: "audio/wav"},
				&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "file:///report.pdf", MIMEType: "application/pdf", Blob: data}},
				&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "file:///notes", Text: "notes"}},
				&mcp.ResourceLink{URI: "https://example.org", Name: "reference", Description: "not fetched"},
			},
			StructuredContent: map[string]any{"answer": 42},
		}
		blocks, err := mcpContentBlocks(result)
		require.NoError(t, err)
		require.NoError(t, ValidateLLMContent(blocks))
		require.Len(t, blocks, 8)
		require.Equal(t, "before", blocks[0].Text)
		require.Equal(t, LLMContentImage, blocks[1].Kind)
		require.Equal(t, encoded, blocks[1].Data, "MCP SDK bytes must be encoded exactly once")
		require.Equal(t, "between", blocks[2].Text)
		require.Equal(t, LLMContentAudio, blocks[3].Kind)
		require.Equal(t, LLMContentDocument, blocks[4].Kind)
		require.Equal(t, "notes", blocks[5].Text)
		require.Contains(t, blocks[6].Text, "https://example.org")
		require.Contains(t, blocks[7].Text, "42")
	})

	t.Run("errored media is preserved and telemetry is textual", func(t *testing.T) {
		recorder, ctx := stateRecorderCtx(t)
		result := newMCP().CallContent(ctx, []LLMTool{{
			Name: "screenshot",
			Call: func(context.Context, any) (any, error) {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{
					&mcp.TextContent{Text: "page failed to load"},
					&mcp.ImageContent{Data: data, MIMEType: "image/png"},
				}}, nil
			},
		}}, &LLMToolCall{Name: "screenshot", CallID: "shot-1"})
		require.True(t, result.Errored)
		require.Equal(t, "shot-1", result.CallID)
		require.Len(t, result.Content, 2)
		require.Equal(t, encoded, result.Content[1].Data)
		require.Contains(t, result.ContentText(), "[image: image/png]")
		recorder.mu.Lock()
		defer recorder.mu.Unlock()
		var logged bool
		for _, record := range recorder.records {
			require.NotContains(t, record.body, encoded)
			logged = logged || strings.Contains(record.body, "[image: image/png]")
		}
		require.True(t, logged)
	})

	t.Run("invalid content becomes explicit tool error", func(t *testing.T) {
		for _, result := range []*mcp.CallToolResult{
			nil,
			{Content: []mcp.Content{(*mcp.ImageContent)(nil)}},
			{Content: []mcp.Content{&mcp.ImageContent{Data: data, MIMEType: "audio/wav"}}},
			{Content: []mcp.Content{&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{MIMEType: "application/zip", Blob: data}}}},
			{Content: []mcp.Content{&mcp.ToolUseContent{ID: "unexpected", Name: "tool"}}},
		} {
			block := newMCP().CallContent(t.Context(), []LLMTool{{Name: "bad", Call: func(context.Context, any) (any, error) { return result, nil }}}, &LLMToolCall{Name: "bad"})
			require.True(t, block.Errored)
			require.NotEmpty(t, block.Text)
			require.Empty(t, block.Content)
		}
	})
}

func TestMediaToolDispatch(t *testing.T) {
	media := &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("image"))}
	for _, mode := range []string{"sequential", "parallel", "changeset"} {
		t.Run(mode, func(t *testing.T) {
			tool := LLMTool{Name: "image", ReadOnly: mode == "parallel", ReturnsChangeset: mode == "changeset", Call: func(context.Context, any) (any, error) { return media, nil }}
			msgs := newMCP().CallBatch(t.Context(), []LLMTool{tool}, []*LLMToolCall{{Name: "image", CallID: "call-1"}}, nil)
			require.Len(t, msgs, 1)
			result := msgs[0].Content[0]
			require.Equal(t, LLMContentToolResult, result.Kind)
			require.Equal(t, "call-1", result.CallID)
			require.False(t, result.Errored)
			require.Equal(t, media, result.Content[0])
			require.NotSame(t, media, result.Content[0])
		})
	}
	t.Run("timeout preserves media", func(t *testing.T) {
		tool := timeoutToolForTest(t, LLMTool{Name: "image", Call: func(context.Context, any) (any, error) { return media, nil }})
		result, err := tool.Call(t.Context(), map[string]any{"duration": "1s", "tool": "image", "arguments": map[string]any{}})
		require.NoError(t, err)
		block, ok := result.(*LLMContentBlock)
		require.True(t, ok)
		require.Equal(t, media, block.Content[0])
	})
}

func TestMediaToolTextBudget(t *testing.T) {
	media := &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("unchanged image"))}
	result := &LLMContentBlock{Kind: LLMContentToolResult}
	for range 10 {
		result.Content = append(result.Content, &LLMContentBlock{Kind: LLMContentText, Text: strings.Repeat("line\n", 10000)}, media.Clone())
	}
	guardToolContent(result)
	size := len(result.Text)
	for _, block := range result.Content {
		if block.Kind == LLMContentText {
			size += len(block.Text)
		} else {
			require.Equal(t, media.Data, block.Data)
		}
	}
	require.LessOrEqual(t, size, llmToolResultMaxBytes)
	require.Contains(t, result.ContentText(), "omitted")
}

func TestMediaToolResultSelectors(t *testing.T) {
	media := &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("image"))}
	msg := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentToolResult, CallID: "call-1", Text: "caption", Content: []*LLMContentBlock{media}}}}
	t.Run("attached", func(t *testing.T) {
		sels, err := toolResultSelectors(nil, []*LLMMessage{msg}, nil)
		require.NoError(t, err)
		require.Len(t, sels, 1)
		require.Equal(t, "withToolResult", sels[0].Field)
		require.Equal(t, dagql.NewString("caption"), sels[0].Args[1].Value)
		require.Equal(t, "blocks", sels[0].Args[3].Name)
		inputs := sels[0].Args[3].Value.(dagql.ArrayInput[dagql.InputObject[LLMContentBlockInput]])
		require.Equal(t, media.Data, inputs[0].Value.Data)
	})
	t.Run("continuation", func(t *testing.T) {
		sels, err := toolResultSelectors(&LLM{}, []*LLMMessage{msg}, map[string]string{"call-1": "reload"})
		require.NoError(t, err)
		require.Equal(t, "withContent", sels[0].Field)
		inputs := sels[0].Args[0].Value.(dagql.ArrayInput[dagql.InputObject[LLMContentBlockInput]])
		require.Len(t, inputs, 2)
		require.Equal(t, "[continued via tool reload]\ncaption", inputs[0].Value.Text)
		require.Equal(t, media.Data, inputs[1].Value.Data)
	})
}
