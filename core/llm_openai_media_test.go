package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIMediaPayload(t *testing.T) {
	messages, err := convertHistoryToOpenAI([]*LLMMessage{{
		Role: LLMMessageRoleUser,
		Content: []*LLMContentBlock{
			{Kind: LLMContentText, Text: "before"},
			{Kind: LLMContentImage, MIMEType: "image/png", Data: "aW1hZ2U="},
			{Kind: LLMContentText, Text: "between"},
			{Kind: LLMContentAudio, MIMEType: "audio/wav", Data: "YXVkaW8="},
			{Kind: LLMContentAudio, MIMEType: "audio/mpeg", Data: "bXAz"},
			{Kind: LLMContentDocument, MIMEType: "application/pdf", Data: "cGRm"},
			{Kind: LLMContentText, Text: "after"},
		},
	}})
	require.NoError(t, err)
	payload, err := json.Marshal(openai.ChatCompletionNewParams{Model: "test", Messages: messages})
	require.NoError(t, err)
	assert.JSONEq(t, `{"model":"test","messages":[{"role":"user","content":[
		{"type":"text","text":"before"},
		{"type":"image_url","image_url":{"url":"data:image/png;base64,aW1hZ2U="}},
		{"type":"text","text":"between"},
		{"type":"input_audio","input_audio":{"data":"YXVkaW8=","format":"wav"}},
		{"type":"input_audio","input_audio":{"data":"bXAz","format":"mp3"}},
		{"type":"file","file":{"file_data":"data:application/pdf;base64,cGRm","filename":"document.pdf"}},
		{"type":"text","text":"after"}
	]}]}`, string(payload))
}

func TestOpenAIToolMediaPayload(t *testing.T) {
	// Two separate messages exercise parallel tool outputs: the synthetic user
	// message must follow both tool messages, not interrupt their sequence.
	history := []*LLMMessage{
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{
			Kind: LLMContentToolResult, CallID: "screenshot", Text: "legacy", Errored: true,
			Content: []*LLMContentBlock{
				{Kind: LLMContentText, Text: "before"},
				{Kind: LLMContentImage, MIMEType: "image/png", Data: "aW1hZ2U="},
				{Kind: LLMContentText, Text: "after"},
			},
		}}},
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{
			Kind: LLMContentToolResult, CallID: "document", Content: []*LLMContentBlock{
				{Kind: LLMContentDocument, MIMEType: "application/pdf", Data: "cGRm"},
			},
		}}},
	}
	messages, err := convertHistoryToOpenAI(history)
	require.NoError(t, err)
	require.Len(t, messages, 3)
	require.NotNil(t, messages[0].OfTool)
	require.NotNil(t, messages[1].OfTool)
	assert.Equal(t, "screenshot", messages[0].OfTool.ToolCallID)
	assert.Equal(t, "document", messages[1].OfTool.ToolCallID)
	text, err := json.Marshal(messages[0])
	require.NoError(t, err)
	assert.Contains(t, string(text), "error: legacy")
	assert.Contains(t, string(text), "before")
	assert.Contains(t, string(text), "after")
	assert.NotContains(t, string(text), "aW1hZ2U=")
	payload, err := json.Marshal(messages[2])
	require.NoError(t, err)
	assert.JSONEq(t, `{"role":"user","content":[
		{"type":"text","text":"Content from tool result \"screenshot\":"},
		{"type":"text","text":"legacy"},
		{"type":"text","text":"before"},
		{"type":"image_url","image_url":{"url":"data:image/png;base64,aW1hZ2U="}},
		{"type":"text","text":"after"},
		{"type":"text","text":"Content from tool result \"document\":"},
		{"type":"file","file":{"file_data":"data:application/pdf;base64,cGRm","filename":"document.pdf"}}
	]}`, string(payload))
}

func TestOpenAITextToolResultsAndMixedOrder(t *testing.T) {
	messages, err := convertHistoryToOpenAI([]*LLMMessage{{
		Role: LLMMessageRoleUser,
		Content: []*LLMContentBlock{
			{Kind: LLMContentText, Text: "before"},
			{Kind: LLMContentToolResult, CallID: "read", Text: "legacy", Content: []*LLMContentBlock{
				{Kind: LLMContentText, Text: "child"},
			}},
			{Kind: LLMContentText, Text: "after"},
		},
	}})
	require.NoError(t, err)
	payload, err := json.Marshal(messages)
	require.NoError(t, err)
	assert.JSONEq(t, `[
		{"role":"user","content":[{"type":"text","text":"before"}]},
		{"role":"tool","tool_call_id":"read","content":"legacy\nchild"},
		{"role":"user","content":[{"type":"text","text":"after"}]}
	]`, string(payload))
}

func TestOpenAIToolAudioPayload(t *testing.T) {
	messages, err := convertHistoryToOpenAI([]*LLMMessage{{
		Role: LLMMessageRoleUser,
		Content: []*LLMContentBlock{{Kind: LLMContentToolResult, CallID: "listen", Content: []*LLMContentBlock{
			{Kind: LLMContentAudio, MIMEType: "audio/wav", Data: "YXVkaW8="},
		}}},
	}})
	require.NoError(t, err)
	require.Len(t, messages, 2)
	payload, err := json.Marshal(messages[1])
	require.NoError(t, err)
	assert.JSONEq(t, `{"role":"user","content":[
		{"type":"text","text":"Content from tool result \"listen\":"},
		{"type":"input_audio","input_audio":{"data":"YXVkaW8=","format":"wav"}}
	]}`, string(payload))
}

func TestCodexMediaPayload(t *testing.T) {
	_, items, err := convertToCodexResponsesFormat([]*LLMMessage{{
		Role: LLMMessageRoleUser,
		Content: []*LLMContentBlock{
			{Kind: LLMContentText, Text: "before"},
			{Kind: LLMContentImage, MIMEType: "image/jpeg", Data: "aW1hZ2U="},
			{Kind: LLMContentDocument, MIMEType: "application/pdf", Data: "cGRm"},
			{Kind: LLMContentText, Text: "after"},
		},
	}})
	require.NoError(t, err)
	payload, err := json.Marshal(responses.ResponseNewParams{
		Model: "test", Input: responses.ResponseNewParamsInputUnion{OfInputItemList: items},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"model":"test","input":[{"role":"user","content":[
		{"type":"input_text","text":"before"},
		{"type":"input_image","image_url":"data:image/jpeg;base64,aW1hZ2U=","detail":"auto"},
		{"type":"input_file","file_data":"data:application/pdf;base64,cGRm","filename":"document.pdf"},
		{"type":"input_text","text":"after"}
	]}]}`, string(payload))
}

func TestCodexToolMediaPayload(t *testing.T) {
	_, items, err := convertToCodexResponsesFormat([]*LLMMessage{{
		Role: LLMMessageRoleUser,
		Content: []*LLMContentBlock{
			{Kind: LLMContentText, Text: "before result"},
			{Kind: LLMContentToolResult, CallID: "read", Text: "legacy", Errored: true,
				Content: []*LLMContentBlock{
					{Kind: LLMContentText, Text: "before image"},
					{Kind: LLMContentImage, MIMEType: "image/png", Data: "aW1hZ2U="},
					{Kind: LLMContentText, Text: "after image"},
					{Kind: LLMContentDocument, MIMEType: "application/pdf", Data: "cGRm"},
				}},
			{Kind: LLMContentText, Text: "after result"},
		},
	}})
	require.NoError(t, err)
	payload, err := json.Marshal(items)
	require.NoError(t, err)
	assert.JSONEq(t, `[
		{"role":"user","content":[{"type":"input_text","text":"before result"}]},
		{"type":"function_call_output","call_id":"read","output":[
			{"type":"input_text","text":"error: legacy"},
			{"type":"input_text","text":"before image"},
			{"type":"input_image","image_url":"data:image/png;base64,aW1hZ2U=","detail":"auto"},
			{"type":"input_text","text":"after image"},
			{"type":"input_file","file_data":"data:application/pdf;base64,cGRm","filename":"document.pdf"}
		]},
		{"role":"user","content":[{"type":"input_text","text":"after result"}]}
	]`, string(payload))
}

func TestOpenAIRejectUnsupportedMedia(t *testing.T) {
	for _, tc := range []struct {
		name  string
		role  LLMMessageRole
		block *LLMContentBlock
	}{
		{"system image", LLMMessageRoleSystem, &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/png", Data: "eA=="}},
		{"assistant image", LLMMessageRoleAssistant, &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/png", Data: "eA=="}},
		{"unknown role", LLMMessageRole("unknown"), &LLMContentBlock{Kind: LLMContentText, Text: "x"}},
		{"unknown kind", LLMMessageRoleUser, &LLMContentBlock{Kind: LLMContentBlockKind("unknown")}},
		{"nil block", LLMMessageRoleUser, nil},
		{"bad base64", LLMMessageRoleUser, &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/png", Data: "bad!!"}},
		{"bad image MIME", LLMMessageRoleUser, &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/tiff", Data: "eA=="}},
		{"unsupported document", LLMMessageRoleUser, &LLMContentBlock{Kind: LLMContentDocument, MIMEType: "application/zip", Data: "eA=="}},
		{"unsupported audio", LLMMessageRoleUser, &LLMContentBlock{Kind: LLMContentAudio, MIMEType: "audio/ogg", Data: "eA=="}},
		{"user tool call", LLMMessageRoleUser, &LLMContentBlock{Kind: LLMContentToolCall, CallID: "id", ToolName: "read"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			history := []*LLMMessage{{Role: tc.role, Content: []*LLMContentBlock{tc.block}}}
			_, err := convertHistoryToOpenAI(history)
			require.Error(t, err)
			_, _, err = convertToCodexResponsesFormat(history)
			require.Error(t, err)
		})
	}
	for _, nested := range []bool{false, true} {
		block := &LLMContentBlock{Kind: LLMContentAudio, MIMEType: "audio/wav", Data: "eA=="}
		if nested {
			block = &LLMContentBlock{Kind: LLMContentToolResult, CallID: "read", Content: []*LLMContentBlock{block}}
		}
		_, _, err := convertToCodexResponsesFormat([]*LLMMessage{{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{block}}})
		require.ErrorContains(t, err, "audio input is unsupported")
	}
}

func TestLocalMediaRequest(t *testing.T) {
	requestBody := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requestBody <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(server.Close)
	// Local endpoints share the OpenAI mapper, including inline media; no URL
	// fetch or provider-hosted file upload is necessary.
	client := newOpenAIClient(&LLMEndpoint{Model: "local-vision", Provider: Local, BaseURL: server.URL}, "", true)
	_, err := client.SendQuery(t.Context(), []*LLMMessage{{
		Role: LLMMessageRoleUser, Content: []*LLMContentBlock{
			{Kind: LLMContentText, Text: "describe"},
			{Kind: LLMContentImage, MIMEType: "image/png", Data: "aW1hZ2U="},
		},
	}}, []LLMTool{{Name: "read", Schema: map[string]any{"type": "object"}}}, &LLMCallOpts{})
	require.NoError(t, err)
	var payload struct {
		Messages json.RawMessage `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(<-requestBody, &payload))
	assert.JSONEq(t, `[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"data:image/png;base64,aW1hZ2U="}}]}]`, string(payload.Messages))
}
