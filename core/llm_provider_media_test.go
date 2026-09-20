package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

// Capture the SDK's actual streaming request, not just its Go parameter structs.
// A non-retryable response keeps these tests independent of response parsing.
func mediaRequestClient(t *testing.T, provider string) (LLMClient, <-chan []byte) {
	t.Helper()
	requests := make(chan []byte, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- body
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	endpoint := &LLMEndpoint{Key: "test", BaseURL: server.URL, Model: "test-model"}
	if provider == "anthropic" {
		return newAnthropicClient(endpoint), requests
	}
	client, err := genai.NewClient(t.Context(), &genai.ClientConfig{
		APIKey:      "test",
		Backend:     genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{BaseURL: server.URL},
	})
	require.NoError(t, err)
	return &GenaiClient{client: client, endpoint: endpoint}, requests
}

func sentMediaRequest(t *testing.T, client LLMClient, requests <-chan []byte, history []*LLMMessage) map[string]json.RawMessage {
	t.Helper()
	_, err := client.SendQuery(t.Context(), history, nil, &LLMCallOpts{})
	require.Error(t, err, "test server deliberately rejects the request")
	select {
	case body := <-requests:
		var request map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(body, &request))
		return request
	default:
		t.Fatalf("no request sent: %v", err)
		return nil
	}
}

func TestAnthropicMediaRequest(t *testing.T) {
	client, requests := mediaRequestClient(t, "anthropic")
	history := []*LLMMessage{
		{Role: LLMMessageRoleSystem, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "instructions"}}},
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{
			{Kind: LLMContentText, Text: "before"},
			{Kind: LLMContentImage, MIMEType: "image/png", Data: "aW1hZ2U="},
			{Kind: LLMContentText, Text: "between"},
			{Kind: LLMContentDocument, MIMEType: "application/pdf", Data: "cGRm"},
		}},
		{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{
			{Kind: LLMContentThinking, Text: "thinking", Signature: "opaque"},
			{Kind: LLMContentToolCall, CallID: "call-1", ToolName: "read", Arguments: JSON(`{}`)},
			{Kind: LLMContentToolCall, CallID: "call-2", ToolName: "read", Arguments: JSON(`{}`)},
		}},
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentToolResult, CallID: "call-2", Text: "legacy", Errored: true, Content: []*LLMContentBlock{
			{Kind: LLMContentImage, MIMEType: "image/jpeg", Data: "aW1hZ2U="},
			{Kind: LLMContentText, Text: "caption"},
			{Kind: LLMContentDocument, MIMEType: "application/pdf", Data: "cGRm"},
		}}}},
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentToolResult, CallID: "call-1", Text: "done"}}},
	}
	request := sentMediaRequest(t, client, requests, history)
	require.JSONEq(t, `[
		{"role":"user","content":[
			{"type":"text","text":"before"},
			{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aW1hZ2U="}},
			{"type":"text","text":"between"},
			{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"cGRm"}}
		]},
		{"role":"assistant","content":[
			{"type":"tool_use","id":"call-1","name":"read","input":{}},
			{"type":"tool_use","id":"call-2","name":"read","input":{}}
		]},
		{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"call-2","is_error":true,"content":[
				{"type":"text","text":"legacy"},
				{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"aW1hZ2U="}},
				{"type":"text","text":"caption"},
				{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"cGRm"}}
			]},
			{"type":"tool_result","tool_use_id":"call-1","is_error":false,"content":[{"type":"text","text":"done"}],"cache_control":{"type":"ephemeral"}}
		]}
	]`, string(request["messages"]))
	require.JSONEq(t, `[{"type":"text","text":"instructions","cache_control":{"type":"ephemeral"}}]`, string(request["system"]))

	// Turning reasoning on must resubmit the original thinking and signature.
	client.(*AnthropicClient).endpoint.ReasoningEffort = "high"
	request = sentMediaRequest(t, client, requests, history)
	var messages []struct {
		Content []json.RawMessage `json:"content"`
	}
	require.NoError(t, json.Unmarshal(request["messages"], &messages))
	require.JSONEq(t, `{"type":"thinking","thinking":"thinking","signature":"opaque"}`, string(messages[1].Content[0]))
}

func TestGenaiMediaRequest(t *testing.T) {
	client, requests := mediaRequestClient(t, "google")
	request := sentMediaRequest(t, client, requests, []*LLMMessage{
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{
			{Kind: LLMContentText, Text: "before"},
			{Kind: LLMContentImage, MIMEType: "image/png", Data: "aW1hZ2U="},
			{Kind: LLMContentAudio, MIMEType: "audio/wav", Data: "YXVkaW8="},
			{Kind: LLMContentDocument, MIMEType: "application/pdf", Data: "cGRm"},
			{Kind: LLMContentText, Text: "after"},
		}},
		{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{
			{Kind: LLMContentThinking, Text: "thinking", Signature: "c2ln"},
			{Kind: LLMContentToolCall, CallID: "call-1", ToolName: "read", Arguments: JSON(`{}`)},
			{Kind: LLMContentToolCall, CallID: "call-2", ToolName: "read", Arguments: JSON(`{}`), Signature: "c2ln"},
		}},
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentToolResult, CallID: "call-2", Text: "legacy", Errored: true, Content: []*LLMContentBlock{
			{Kind: LLMContentImage, MIMEType: "image/png", Data: "aW1hZ2U="},
			{Kind: LLMContentText, Text: "caption"},
			{Kind: LLMContentAudio, MIMEType: "audio/wav", Data: "YXVkaW8="},
			{Kind: LLMContentDocument, MIMEType: "application/pdf", Data: "cGRm"},
		}}}},
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentToolResult, CallID: "call-1", Text: "done"}}},
	})
	require.JSONEq(t, `[
		{"role":"user","parts":[
			{"text":"before"},
			{"inlineData":{"mimeType":"image/png","data":"aW1hZ2U="}},
			{"inlineData":{"mimeType":"audio/wav","data":"YXVkaW8="}},
			{"inlineData":{"mimeType":"application/pdf","data":"cGRm"}},
			{"text":"after"}
		]},
		{"role":"model","parts":[
			{"text":"thinking","thought":true,"thoughtSignature":"c2ln"},
			{"functionCall":{"id":"call-1","name":"read"}},
			{"functionCall":{"id":"call-2","name":"read"},"thoughtSignature":"c2ln"}
		]},
		{"role":"user","parts":[
			{"functionResponse":{"id":"call-2","name":"read","response":{"response":"legacy","error":true,"content":[
				{"type":"text","text":"legacy"},
				{"type":"image","mimeType":"image/png","partIndex":0},
				{"type":"text","text":"caption"},
				{"type":"audio","mimeType":"audio/wav","partIndex":1},
				{"type":"document","mimeType":"application/pdf","partIndex":2}
			]},"parts":[
				{"inlineData":{"mimeType":"image/png","data":"aW1hZ2U="}},
				{"inlineData":{"mimeType":"audio/wav","data":"YXVkaW8="}},
				{"inlineData":{"mimeType":"application/pdf","data":"cGRm"}}
			]}},
			{"functionResponse":{"id":"call-1","name":"read","response":{"response":"done","error":false}}}
		]}
	]`, string(request["contents"]))
}

func TestProviderMediaOnlyToolResult(t *testing.T) {
	for _, provider := range []string{"anthropic", "google"} {
		t.Run(provider, func(t *testing.T) {
			client, requests := mediaRequestClient(t, provider)
			request := sentMediaRequest(t, client, requests, []*LLMMessage{
				{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "look"}}},
				{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{{Kind: LLMContentToolCall, CallID: "call-1", ToolName: "read", Arguments: JSON(`{}`)}}},
				{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentToolResult, CallID: "call-1", Content: []*LLMContentBlock{{Kind: LLMContentImage, MIMEType: "image/png", Data: "eA=="}}}}},
			})
			if provider == "anthropic" {
				var messages []struct {
					Content []json.RawMessage `json:"content"`
				}
				require.NoError(t, json.Unmarshal(request["messages"], &messages))
				require.JSONEq(t, `{"type":"tool_result","tool_use_id":"call-1","is_error":false,"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"eA=="}}],"cache_control":{"type":"ephemeral"}}`, string(messages[2].Content[0]))
			} else {
				var contents []struct {
					Parts []json.RawMessage `json:"parts"`
				}
				require.NoError(t, json.Unmarshal(request["contents"], &contents))
				require.JSONEq(t, `{"functionResponse":{"id":"call-1","name":"read","response":{"response":"","error":false,"content":[{"type":"image","mimeType":"image/png","partIndex":0}]},"parts":[{"inlineData":{"mimeType":"image/png","data":"eA=="}}]}}`, string(contents[2].Parts[0]))
			}
		})
	}
}

func TestProviderMediaValidation(t *testing.T) {
	for _, provider := range []string{"anthropic", "google"} {
		t.Run(provider, func(t *testing.T) {
			client, requests := mediaRequestClient(t, provider)
			for _, tc := range []struct {
				name  string
				role  LLMMessageRole
				block *LLMContentBlock
			}{
				{"system media", LLMMessageRoleSystem, &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/png", Data: "eA=="}},
				{"assistant media", LLMMessageRoleAssistant, &LLMContentBlock{Kind: LLMContentDocument, MIMEType: "application/pdf", Data: "eA=="}},
				{"unsupported image", LLMMessageRoleUser, &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/svg+xml", Data: "eA=="}},
				{"unsupported document", LLMMessageRoleUser, &LLMContentBlock{Kind: LLMContentDocument, MIMEType: "application/zip", Data: "eA=="}},
				{"unsupported audio", LLMMessageRoleUser, &LLMContentBlock{Kind: LLMContentAudio, MIMEType: "audio/unknown", Data: "eA=="}},
				{"invalid base64", LLMMessageRoleUser, &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/png", Data: "invalid!"}},
				{"unknown kind", LLMMessageRoleUser, &LLMContentBlock{Kind: "UNKNOWN"}},
				{"nil block", LLMMessageRoleUser, nil},
				{"missing data", LLMMessageRoleUser, &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/png"}},
				{"missing MIME type", LLMMessageRoleUser, &LLMContentBlock{Kind: LLMContentImage, Data: "eA=="}},
				{"nested tool call", LLMMessageRoleUser, &LLMContentBlock{Kind: LLMContentToolResult, CallID: "call-1", Content: []*LLMContentBlock{{Kind: LLMContentToolCall, CallID: "call-2", ToolName: "read"}}}},
				{"nested invalid base64", LLMMessageRoleUser, &LLMContentBlock{Kind: LLMContentToolResult, CallID: "call-1", Content: []*LLMContentBlock{{Kind: LLMContentImage, MIMEType: "image/png", Data: "invalid!"}}}},
				{"nested unsupported", LLMMessageRoleUser, &LLMContentBlock{Kind: LLMContentToolResult, CallID: "call-1", Content: []*LLMContentBlock{{Kind: LLMContentImage, MIMEType: "image/svg+xml", Data: "eA=="}}}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					_, err := client.SendQuery(t.Context(), []*LLMMessage{{Role: tc.role, Content: []*LLMContentBlock{tc.block}}}, nil, &LLMCallOpts{})
					require.Error(t, err)
					require.Empty(t, requests, "invalid content must be rejected before the HTTP request")
				})
			}
		})
	}
	t.Run("anthropic audio", func(t *testing.T) {
		client, requests := mediaRequestClient(t, "anthropic")
		for _, block := range []*LLMContentBlock{
			{Kind: LLMContentAudio, MIMEType: "audio/wav", Data: "eA=="},
			{Kind: LLMContentToolResult, CallID: "call-1", Content: []*LLMContentBlock{{Kind: LLMContentAudio, MIMEType: "audio/wav", Data: "eA=="}}},
		} {
			_, err := client.SendQuery(t.Context(), []*LLMMessage{{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{block}}}, nil, &LLMCallOpts{})
			require.ErrorContains(t, err, "AUDIO content is not supported")
			require.Empty(t, requests)
		}
	})
}
