package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Gemini 3 behind an OpenAI-compatible endpoint (Vertex AI, or the Gemini
// API's /v1beta/openai) signs its function calls in
// tool_calls[].extra_content.google.thought_signature and rejects a later
// request whose history drops the signature.
func TestOpenAIChatReturnsGeminiThoughtSignatures(t *testing.T) {
	const signature = "c2lnbmVkLWJ5LWdlbWluaS0z"
	// With parallel calls Gemini signs only the first one. Kept on one line,
	// so each fits in a server-sent event's data line.
	call1 := `"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"a.go\"}"}`
	call2 := `{"index":1,"id":"call_2","type":"function","function":{"name":"read","arguments":"{\"path\":\"b.go\"}"}}`
	signed := `"extra_content":{"google":{"thought_signature":"` + signature + `"}}`
	toolCalls := `[{` + call1 + `,` + signed + `},` + call2 + `]`

	for _, tc := range []struct {
		name      string
		streaming bool
		deltas    []string
	}{
		{name: "non-streaming"},
		{name: "streaming", streaming: true, deltas: []string{
			`{"role":"assistant","tool_calls":` + toolCalls + `}`,
		}},
		// Nothing says which chunk carries the signature.
		{name: "streaming, signature in a later chunk", streaming: true, deltas: []string{
			`{"role":"assistant","tool_calls":[{` + call1 + `},` + call2 + `]}`,
			`{"tool_calls":[{"index":0,` + signed + `}]}`,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requestBody := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				var req struct {
					Stream bool `json:"stream"`
				}
				if err := json.Unmarshal(body, &req); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				// Answer the first request with the tool calls, and record the
				// one that sends them back.
				answer := strings.Contains(string(body), `"tool_calls"`)
				if answer {
					requestBody <- body
				}
				if req.Stream {
					w.Header().Set("Content-Type", "text/event-stream")
					deltas, finish := tc.deltas, "tool_calls"
					if answer {
						deltas, finish = []string{`{"role":"assistant","content":"done"}`}, "stop"
					}
					for _, delta := range deltas {
						_, _ = io.WriteString(w, `data: {"id":"test","object":"chat.completion.chunk","choices":[{"index":0,"delta":`+delta+`}]}`+"\n\n")
					}
					_, _ = io.WriteString(w, `data: {"id":"test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"`+finish+`"}]}`+"\n\n")
					_, _ = io.WriteString(w, "data: [DONE]\n\n")
					return
				}
				message := `{"role":"assistant","tool_calls":` + toolCalls + `}`
				finish := "tool_calls"
				if answer {
					message, finish = `{"role":"assistant","content":"done"}`, "stop"
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"test","object":"chat.completion","choices":[{"index":0,"message":`+message+`,"finish_reason":"`+finish+`"}]}`)
			}))
			t.Cleanup(server.Close)

			client := newOpenAIClient(&LLMEndpoint{Model: "gemini-3-flash", Provider: Other, BaseURL: server.URL}, "", !tc.streaming)
			tools := []LLMTool{{Name: "read", Schema: map[string]any{"type": "object"}}}
			history := llmTestHistory()

			res, err := client.SendQuery(t.Context(), history, tools, &LLMCallOpts{})
			require.NoError(t, err)
			history = append(history,
				&LLMMessage{Role: LLMMessageRoleAssistant, Content: res.Content},
				&LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{
					{Kind: LLMContentToolResult, CallID: "call_1", Text: "package a"},
					{Kind: LLMContentToolResult, CallID: "call_2", Text: "package b"},
				}},
			)
			_, err = client.SendQuery(t.Context(), history, tools, &LLMCallOpts{})
			require.NoError(t, err)

			var payload struct {
				Messages []struct {
					Role      string           `json:"role"`
					ToolCalls []map[string]any `json:"tool_calls"`
				} `json:"messages"`
			}
			require.NoError(t, json.Unmarshal(<-requestBody, &payload))
			var sent []map[string]any
			for _, msg := range payload.Messages {
				sent = append(sent, msg.ToolCalls...)
			}
			require.Len(t, sent, 2)
			assert.Equal(t, "call_1", sent[0]["id"])
			assert.Equal(t, map[string]any{"google": map[string]any{"thought_signature": signature}}, sent[0]["extra_content"])
			// An unsigned call goes back as it came: other endpoints never
			// see the field.
			assert.Equal(t, "call_2", sent[1]["id"])
			assert.NotContains(t, sent[1], "extra_content")
		})
	}
}
