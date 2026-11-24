package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine"
)

// TestAnthropicOAuthUserAgent locks in the Claude Code identity the
// subscription OAuth client presents on the wire. Anthropic gates newly
// released models on the version in this header, so the built-in default must
// be what goes out when nothing is configured, and a configured override must
// replace it verbatim.
func TestAnthropicOAuthUserAgent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version string
		want    string
	}{
		{
			name: "default",
			want: "claude-cli/" + defaultClaudeCodeVersion + " (external, cli)",
		},
		{
			name:    "override",
			version: "9.9.9",
			want:    "claude-cli/9.9.9 (external, cli)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var seen []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				seen = append(seen, r.Header.Get("User-Agent"))
				mu.Unlock()
				// A 400 is not retried by the SDK, and the body is irrelevant:
				// only the request headers are under test.
				w.WriteHeader(http.StatusBadRequest)
			}))
			t.Cleanup(srv.Close)

			endpoint := &LLMEndpoint{
				Provider:          Anthropic,
				IsOAuth:           true,
				AuthToken:         "token",
				BaseURL:           srv.URL,
				ClaudeCodeVersion: tc.version,
			}
			client := newAnthropicClient(endpoint)
			_, _ = client.client.Messages.New(context.Background(), anthropic.MessageNewParams{
				Model:     "claude-test",
				MaxTokens: 1,
				Messages: []anthropic.MessageParam{
					anthropic.NewUserMessage(anthropic.NewTextBlock("hi")),
				},
			})

			mu.Lock()
			defer mu.Unlock()
			require.Equal(t, []string{tc.want}, seen)
		})
	}
}

// TestLlmConfigClaudeCodeVersion covers the claude_code_version override on
// an endpoint: a configured bare X.Y.Z reaches the Claude Code user-agent, and
// the router rejects anything else (see TestLLMRouterClaudeCodeVersion).
func TestLlmConfigClaudeCodeVersion(t *testing.T) {
	t.Run("unset leaves the default in place", func(t *testing.T) {
		r := routerWith(t, map[string]*engine.LLMProviderConfig{
			"anthropic": {AuthToken: "env://ANTHROPIC_AUTH_TOKEN"},
		})
		ep, err := r.Route("claude-x", "")
		require.NoError(t, err)
		assert.Empty(t, ep.ClaudeCodeVersion)
	})

	t.Run("bare X.Y.Z is accepted", func(t *testing.T) {
		r := routerWith(t, map[string]*engine.LLMProviderConfig{
			"anthropic": {AuthToken: "env://ANTHROPIC_AUTH_TOKEN", ClaudeCodeVersion: "2.1.260"},
		})
		ep, err := r.Route("claude-x", "")
		require.NoError(t, err)
		assert.Equal(t, "2.1.260", ep.ClaudeCodeVersion)
	})
}

// anthropicReasoningRequest routes model through a router configured with a
// provider-wide reasoning effort (as ANTHROPIC_REASONING_EFFORT would), so the
// endpoint carries the model's real catalog metadata, then captures the
// request the SDK actually sends. history carries a prior thinking block so
// the test can see whether it is resubmitted.
func anthropicReasoningRequest(t *testing.T, model, effort string, maxTokens int) map[string]json.RawMessage {
	t.Helper()
	requests := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- body
		// Not retried by the SDK; only the request body is under test.
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	router := routerWith(t, map[string]*engine.LLMProviderConfig{
		"anthropic": {
			BaseURL:         srv.URL,
			ReasoningEffort: effort,
		},
	})
	endpoint, err := router.Route(model, string(Anthropic))
	require.NoError(t, err)
	// Route is pure; stand in for the key Endpoint would resolve.
	endpoint.Key = "test"
	endpoint.Client, err = endpoint.newClient()
	require.NoError(t, err)

	history := []*LLMMessage{
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "hi"}}},
		{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{
			{Kind: LLMContentThinking, Text: "pondering", Signature: "sig"},
			{Kind: LLMContentToolCall, CallID: "call-1", ToolName: "read", Arguments: JSON(`{}`)},
		}},
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentToolResult, CallID: "call-1", Text: "done"}}},
	}
	_, err = endpoint.Client.SendQuery(t.Context(), history, nil, &LLMCallOpts{MaxTokens: maxTokens})
	require.Error(t, err, "test server deliberately rejects the request")
	var request map[string]json.RawMessage
	select {
	case body := <-requests:
		require.NoError(t, json.Unmarshal(body, &request))
	default:
		t.Fatalf("no request sent: %v", err)
	}
	return request
}

// resubmittedThinking reports whether the request carries the prior turn's
// thinking block.
func resubmittedThinking(t *testing.T, request map[string]json.RawMessage) bool {
	t.Helper()
	var messages []struct {
		Content []struct {
			Type string `json:"type"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(request["messages"], &messages))
	for _, msg := range messages {
		for _, block := range msg.Content {
			if block.Type == "thinking" {
				return true
			}
		}
	}
	return false
}

// TestAnthropicReasoningByModel covers translating a provider-configured
// reasoning effort for models that can't take it natively. Claude Haiku 4.5
// (the catalog's default small model, so what withSmallModel picks) rejects
// adaptive thinking with a 400 ("adaptive thinking is not supported on this
// model"), so it must get budget-based extended thinking instead, while
// effort-capable and uncatalogued models keep effort + adaptive thinking.
func TestAnthropicReasoningByModel(t *testing.T) {
	small, ok := (&LLMRouter{}).SmallModel(Anthropic)
	require.True(t, ok)
	require.Equal(t, "claude-haiku-4-5-20251001", small,
		"catalog's default small model changed; check it still exercises budget-based thinking")

	t.Run("small model gets budget thinking, not adaptive", func(t *testing.T) {
		request := anthropicReasoningRequest(t, small, "high", 0)
		assert.NotContains(t, request, "output_config")
		assert.JSONEq(t, `{"type":"enabled","budget_tokens":16384}`, string(request["thinking"]))
		var maxTokens int64
		require.NoError(t, json.Unmarshal(request["max_tokens"], &maxTokens))
		assert.Greater(t, maxTokens, int64(16384), "budget_tokens must stay below max_tokens")
		assert.True(t, resubmittedThinking(t, request), "thinking is on, so prior thinking must be resubmitted")
	})

	t.Run("budget follows the effort level", func(t *testing.T) {
		request := anthropicReasoningRequest(t, small, "low", 0)
		assert.NotContains(t, request, "output_config")
		assert.JSONEq(t, `{"type":"enabled","budget_tokens":4096}`, string(request["thinking"]))
	})

	t.Run("budget leaves room for the reply under a tight max_tokens", func(t *testing.T) {
		request := anthropicReasoningRequest(t, small, "high", 4096)
		assert.JSONEq(t, `4096`, string(request["max_tokens"]))
		assert.JSONEq(t, `{"type":"enabled","budget_tokens":2048}`, string(request["thinking"]))
	})

	t.Run("max_tokens too small for any budget disables thinking", func(t *testing.T) {
		request := anthropicReasoningRequest(t, small, "high", 1500)
		assert.NotContains(t, request, "output_config")
		assert.NotContains(t, request, "thinking")
		assert.False(t, resubmittedThinking(t, request), "thinking is off, so prior thinking must be dropped")
	})

	t.Run("none disables reasoning", func(t *testing.T) {
		request := anthropicReasoningRequest(t, small, "none", 0)
		assert.NotContains(t, request, "output_config")
		assert.NotContains(t, request, "thinking")
		assert.False(t, resubmittedThinking(t, request))
	})

	for _, model := range []string{
		// Takes effort levels natively, per the catalog.
		"claude-sonnet-4-6",
		// Not in the catalog (e.g. newer than it): unchanged behavior.
		"claude-unreleased-model",
	} {
		t.Run(model+" keeps effort with adaptive thinking", func(t *testing.T) {
			request := anthropicReasoningRequest(t, model, "high", 0)
			assert.JSONEq(t, `{"effort":"high"}`, string(request["output_config"]))
			assert.JSONEq(t, `{"type":"adaptive","display":"summarized"}`, string(request["thinking"]))
			assert.True(t, resubmittedThinking(t, request))
		})
	}
}

// TestAnthropicReasoningUnsupportedModel covers a model the catalog says
// can't reason: the effort is dropped along with any prior thinking.
func TestAnthropicReasoningUnsupportedModel(t *testing.T) {
	client := newAnthropicClient(&LLMEndpoint{
		Model:           "claude-no-reasoning",
		ReasoningEffort: "high",
		ReasoningMode:   LLMReasoningUnsupported,
	})
	reasoning := client.reasoningParams(nil, nil, &LLMCallOpts{})
	assert.False(t, reasoning.enabled)
	assert.Equal(t, anthropic.ThinkingConfigParamUnion{}, reasoning.thinking)
	assert.Equal(t, anthropic.OutputConfigParam{}, reasoning.outputConfig)
	assert.Equal(t, int64(8192), reasoning.maxTokens)
}

// TestAnthropicEagerInputStreaming covers asking for tool input to stream as
// it is generated, on both auth paths: by default Anthropic buffers each value
// and only sends a key once its value is complete, so a long argument shows
// nothing until it lands whole. It must not disturb the Claude Code identity
// the OAuth path presents.
func TestAnthropicEagerInputStreaming(t *testing.T) {
	for _, tc := range []struct {
		name     string
		endpoint LLMEndpoint
		beta     string
	}{
		{
			name:     "api key",
			endpoint: LLMEndpoint{Provider: Anthropic, Key: "key"},
		},
		{
			name:     "oauth",
			endpoint: LLMEndpoint{Provider: Anthropic, IsOAuth: true, AuthToken: "token"},
			beta:     "claude-code-20250219,oauth-2025-04-20",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			type sent struct {
				beta string
				body []byte
			}
			requests := make(chan sent, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				requests <- sent{beta: r.Header.Get("anthropic-beta"), body: body}
				// Not retried by the SDK; only the request is under test.
				w.WriteHeader(http.StatusBadRequest)
			}))
			t.Cleanup(srv.Close)

			endpoint := tc.endpoint
			endpoint.BaseURL = srv.URL
			endpoint.Model = "claude-test"
			client := newAnthropicClient(&endpoint)
			_, err := client.SendQuery(t.Context(), []*LLMMessage{
				{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "hi"}}},
			}, []LLMTool{
				{Name: "write", Schema: map[string]any{"type": "object"}},
				{Name: "read", Schema: map[string]any{"type": "object"}},
			}, &LLMCallOpts{})
			require.Error(t, err, "test server deliberately rejects the request")

			var req sent
			select {
			case req = <-requests:
			default:
				t.Fatalf("no request sent: %v", err)
			}
			assert.Equal(t, tc.beta, req.beta)
			var body struct {
				Tools []struct {
					Name                string `json:"name"`
					EagerInputStreaming *bool  `json:"eager_input_streaming"`
				} `json:"tools"`
			}
			require.NoError(t, json.Unmarshal(req.body, &body))
			require.Len(t, body.Tools, 2)
			for _, tool := range body.Tools {
				require.NotNil(t, tool.EagerInputStreaming, "tool %s", tool.Name)
				assert.True(t, *tool.EagerInputStreaming, "tool %s", tool.Name)
			}
		})
	}
}

// anthropicToolUseStream answers with one streamed tool_use block whose input
// arrives as the given input_json_delta fragments.
func anthropicToolUseStream(t *testing.T, fragments ...string) *AnthropicClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		event := func(typ string, data any) {
			enc, err := json.Marshal(data)
			if err != nil {
				panic(err)
			}
			_, _ = io.WriteString(w, "event: "+typ+"\ndata: "+string(enc)+"\n\n")
		}
		event("message_start", map[string]any{"type": "message_start", "message": map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-test",
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": 1, "output_tokens": 1},
		}})
		event("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{
			"type": "tool_use", "id": "toolu_1", "name": "write", "input": map[string]any{},
		}})
		for _, fragment := range fragments {
			event("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{
				"type": "input_json_delta", "partial_json": fragment,
			}})
		}
		event("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
		event("message_delta", map[string]any{"type": "message_delta",
			"delta": map[string]any{"stop_reason": "tool_use", "stop_sequence": nil},
			"usage": map[string]any{"output_tokens": 10},
		})
		event("message_stop", map[string]any{"type": "message_stop"})
	}))
	t.Cleanup(srv.Close)
	return newAnthropicClient(&LLMEndpoint{Provider: Anthropic, Key: "key", BaseURL: srv.URL, Model: "claude-test"})
}

func anthropicStreamedToolCall(t *testing.T, client *AnthropicClient) *LLMContentBlock {
	t.Helper()
	res, err := client.SendQuery(t.Context(), []*LLMMessage{
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "write it"}}},
	}, []LLMTool{{Name: "write", Schema: map[string]any{"type": "object"}}}, &LLMCallOpts{})
	require.NoError(t, err)
	require.Len(t, res.Content, 1)
	block := res.Content[0]
	require.Equal(t, LLMContentToolCall, block.Kind)
	require.Equal(t, "toolu_1", block.CallID)
	require.Equal(t, "write", block.ToolName)
	return block
}

// TestAnthropicStreamedToolArguments covers assembling a tool call's arguments
// from eagerly streamed input, which the API doesn't validate.
func TestAnthropicStreamedToolArguments(t *testing.T) {
	t.Run("fragments assemble into the arguments", func(t *testing.T) {
		client := anthropicToolUseStream(t, `{"filePath": "a.txt", `, `"contents": "hel`, `lo"}`)
		block := anthropicStreamedToolCall(t, client)
		assert.JSONEq(t, `{"filePath":"a.txt","contents":"hello"}`, block.Arguments.String())
	})

	t.Run("no input keeps the empty object", func(t *testing.T) {
		client := anthropicToolUseStream(t)
		block := anthropicStreamedToolCall(t, client)
		assert.JSONEq(t, `{}`, block.Arguments.String())
	})

	t.Run("malformed input fails as a tool error", func(t *testing.T) {
		raw := `{"filePath": "a.txt", "contents": "hel`
		client := anthropicToolUseStream(t, raw)
		block := anthropicStreamedToolCall(t, client)
		// Kept verbatim, rather than the {} the SDK's accumulator swaps in,
		// which would run the tool with no arguments at all.
		assert.Equal(t, raw, block.Arguments.String())

		called := false
		result := newMCP().CallContent(t.Context(), []LLMTool{{
			Name:   "write",
			Schema: map[string]any{"type": "object"},
			Call: func(context.Context, any) (any, error) {
				called = true
				return "written", nil
			},
		}}, &LLMToolCall{CallID: block.CallID, Name: block.ToolName, Arguments: block.Arguments})
		assert.False(t, called, "the tool must not run on malformed arguments")
		assert.True(t, result.Errored)
		assert.Equal(t, block.CallID, result.CallID)
		assert.Contains(t, result.Text, "failed to parse tool arguments")
	})
}

// TestProviderResubmitsMalformedToolArguments covers sending a conversation
// that holds a tool call with malformed arguments back to a model: they are
// wrapped as Anthropic recommends rather than failing every later turn.
func TestProviderResubmitsMalformedToolArguments(t *testing.T) {
	for _, provider := range []string{"anthropic", "google"} {
		t.Run(provider, func(t *testing.T) {
			client, requests := mediaRequestClient(t, provider)
			raw := `{"filePath": "a.txt", "contents": "hel`
			request := sentMediaRequest(t, client, requests, []*LLMMessage{
				{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "write it"}}},
				{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{{Kind: LLMContentToolCall, CallID: "call-1", ToolName: "write", Arguments: JSON(raw)}}},
				{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentToolResult, CallID: "call-1", Text: "failed to parse tool arguments", Errored: true}}},
			})
			want, err := json.Marshal(map[string]string{"INVALID_JSON": raw})
			require.NoError(t, err)
			if provider == "anthropic" {
				var messages []struct {
					Content []struct {
						Input json.RawMessage `json:"input"`
					} `json:"content"`
				}
				require.NoError(t, json.Unmarshal(request["messages"], &messages))
				assert.JSONEq(t, string(want), string(messages[1].Content[0].Input))
			} else {
				var contents []struct {
					Parts []struct {
						FunctionCall struct {
							Args json.RawMessage `json:"args"`
						} `json:"functionCall"`
					} `json:"parts"`
				}
				require.NoError(t, json.Unmarshal(request["contents"], &contents))
				assert.JSONEq(t, string(want), string(contents[1].Parts[0].FunctionCall.Args))
			}
		})
	}
}
