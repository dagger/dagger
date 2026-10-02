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

	router := &LLMRouter{
		AnthropicAPIKey:          "test",
		AnthropicBaseURL:         srv.URL,
		AnthropicReasoningEffort: effort,
	}
	endpoint, err := router.Route(model, string(Anthropic))
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
