package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func tokenAttrs(t *testing.T, provider LLMProvider, model, text string) attribute.Set {
	t.Helper()
	ctx := withToolResultTokenModel(t.Context(), &LLMEndpoint{Provider: provider, Model: model})
	return attribute.NewSet(toolResultTokenAttrs(ctx, &LLMContentBlock{Kind: LLMContentToolResult, Text: text})...)
}

func requireTokenAttr(t *testing.T, attrs attribute.Set, key string, want any) {
	t.Helper()
	value, ok := attrs.Value(attribute.Key(key))
	require.True(t, ok, "missing %s", key)
	require.Equal(t, want, value.AsInterface(), key)
}

func TestToolResultTokenModel(t *testing.T) {
	for _, tt := range []struct {
		provider LLMProvider
		model    string
		encoding string
	}{
		{OpenAI, "gpt-4", "cl100k_base"},
		{OpenAI, "gpt-4o-2024-08-06", "o200k_base"},
		{OpenAICodex, "gpt-5.3-codex", "o200k_base"},
		{OpenAI, "gpt-4.1-mini", "o200k_base"},
		{OpenAI, "o3", "o200k_base"},
		{OpenAI, "my-deployment", ""},
		{OpenAI, "gpt-4o-custom", ""},
		{OpenAI, "gpt-4o-xxxx-08-06", ""},
		{OpenAI, "gpt-oss-120b", ""},
		{OpenAI, "unknown", ""},
		{OpenRouter, "gpt-4o", ""},
		{Other, "gpt-4o", ""},
		{Local, "gpt-4o", ""},
		{Anthropic, "claude-sonnet-4", ""},
		{Google, "gemini-2.5-pro", ""},
	} {
		t.Run(string(tt.provider)+"/"+tt.model, func(t *testing.T) {
			require.Equal(t, tt.encoding, (toolResultTokenModel{provider: tt.provider, model: tt.model, trusted: true}).encoding())
			attrs := tokenAttrs(t, tt.provider, tt.model, "hello")
			requireTokenAttr(t, attrs, telemetryattrs.LLMToolResultTokensModelAttr, tt.model)
			if tt.encoding == "" {
				requireTokenAttr(t, attrs, telemetryattrs.LLMToolResultTokensMethodAttr, "bytes_div_4")
				require.False(t, attrs.HasValue(telemetryattrs.LLMToolResultTokensEncodingAttr))
			} else {
				requireTokenAttr(t, attrs, telemetryattrs.LLMToolResultTokensMethodAttr, "tokenizer")
				requireTokenAttr(t, attrs, telemetryattrs.LLMToolResultTokensEncodingAttr, tt.encoding)
			}
		})
	}
}

func TestToolResultTokenizerEndpointTrust(t *testing.T) {
	for _, tt := range []struct {
		endpoint LLMEndpoint
		trusted  bool
	}{
		{LLMEndpoint{Provider: OpenAI}, true},
		{LLMEndpoint{Provider: OpenAI, BaseURL: "https://api.openai.com/v1"}, true},
		{LLMEndpoint{Provider: OpenAI, BaseURL: "https://api.openai.com:443/v1"}, true},
		{LLMEndpoint{Provider: OpenAICodex, BaseURL: "https://chatgpt.com/backend-api"}, true},
		{LLMEndpoint{Provider: OpenAI, BaseURL: "http://localhost:8080/v1"}, false},
		{LLMEndpoint{Provider: OpenAI, BaseURL: "https://compatible.example/v1"}, false},
		{LLMEndpoint{Provider: OpenAI, BaseURL: "https://api.openai.com.example/v1"}, false},
		{LLMEndpoint{Provider: OpenAI, BaseURL: "http://api.openai.com/v1"}, false},
		{LLMEndpoint{Provider: OpenAI, BaseURL: "https://api.openai.com:8443/v1"}, false},
		{LLMEndpoint{Provider: OpenAI, BaseURL: ":invalid"}, false},
		{LLMEndpoint{Provider: OpenAI, azureVersion: "2024-06-01"}, false},
		{LLMEndpoint{Provider: OpenAICodex, BaseURL: "https://compatible.example"}, false},
		{LLMEndpoint{Provider: Other, BaseURL: "https://api.openai.com/v1"}, false},
	} {
		t.Run(string(tt.endpoint.Provider)+"/"+tt.endpoint.BaseURL+"/"+tt.endpoint.azureVersion, func(t *testing.T) {
			require.Equal(t, tt.trusted, toolResultTokenizerTrusted(&tt.endpoint))
			tt.endpoint.Model = "gpt-4o"
			ctx := withToolResultTokenModel(t.Context(), &tt.endpoint)
			attrs := attribute.NewSet(toolResultTokenAttrs(ctx, &LLMContentBlock{Kind: LLMContentToolResult, Text: "hello"})...)
			method := "bytes_div_4"
			if tt.trusted {
				method = "tokenizer"
			}
			requireTokenAttr(t, attrs, telemetryattrs.LLMToolResultTokensMethodAttr, method)
		})
	}
}

func TestToolResultTokenCounts(t *testing.T) {
	for _, tt := range []struct {
		text   string
		cl100k int64
		o200k  int64
	}{
		{"", 0, 0},
		{"hello world", 2, 2},
		{"antidisestablishmentarianism", 6, 6},
		{"お誕生日おめでとう", 9, 8},
		{"2 + 2 = 4", 7, 7},
	} {
		t.Run(tt.text, func(t *testing.T) {
			requireTokenAttr(t, tokenAttrs(t, OpenAI, "gpt-4", tt.text), telemetryattrs.LLMToolResultTokensAttr, tt.cl100k)
			requireTokenAttr(t, tokenAttrs(t, OpenAI, "gpt-4o", tt.text), telemetryattrs.LLMToolResultTokensAttr, tt.o200k)
			requireTokenAttr(t, tokenAttrs(t, Anthropic, "claude", tt.text), telemetryattrs.LLMToolResultTokensAttr, int64((len(tt.text)+3)/4))
		})
	}
	// Neither special-looking strings nor malformed UTF-8 may panic or turn
	// literal tool text into a protocol special token.
	for _, model := range []string{"gpt-4", "gpt-4o"} {
		attrs := tokenAttrs(t, OpenAI, model, "<|endoftext|>")
		requireTokenAttr(t, attrs, telemetryattrs.LLMToolResultTokensMethodAttr, "tokenizer")
		n, _ := attrs.Value(telemetryattrs.LLMToolResultTokensAttr)
		require.Greater(t, n.AsInt64(), int64(1))
		attrs = tokenAttrs(t, OpenAI, model, "\xff\xfe")
		requireTokenAttr(t, attrs, telemetryattrs.LLMToolResultTokensMethodAttr, "bytes_div_4")
		requireTokenAttr(t, attrs, telemetryattrs.LLMToolResultTokensAttr, int64(1))
	}
}

func TestToolResultTokenCodecsReusedConcurrently(t *testing.T) {
	const text = "func main() {\n\tfmt.Println(\"こんにちは\")\n}\n"
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			for _, model := range []string{"gpt-4", "gpt-4o"} {
				first := tokenAttrs(t, OpenAI, model, text)
				require.Equal(t, first, tokenAttrs(t, OpenAI, model, text))
				requireTokenAttr(t, first, telemetryattrs.LLMToolResultTokensMethodAttr, "tokenizer")
			}
			require.Same(t, toolResultCl100k(), toolResultCl100k())
			require.Same(t, toolResultO200k(), toolResultO200k())
		})
	}
	wg.Wait()
}

// Exercise the real dispatch path, not just the finalizer: CallContent used to
// end the display span before CallBatch could attach its result token count.
func TestToolResultTokensThroughDispatch(t *testing.T) {
	for _, tt := range []struct {
		name      string
		text      string
		err       error
		arguments JSON
		missing   bool
		cancel    bool
		logsOwn   bool
	}{
		{name: "success", text: "hello world"},
		{name: "empty"},
		{name: "bounded", text: strings.Repeat("large result\n", 20000)},
		{name: "error", err: errors.New("tool failed")},
		{name: "unknown tool", missing: true},
		{name: "invalid arguments", arguments: JSON("{")},
		{name: "cancellation", cancel: true},
		{name: "own logs", text: "DangEval report", logsOwn: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sr, ctx := recordingTestRecorder(t)
			ep := &LLMEndpoint{Provider: OpenAI, Model: "gpt-4o"}
			ctx = withToolResultTokenModel(ctx, ep)
			// Captured by value: finalization must not re-resolve a changed
			// endpoint or accidentally use a continuation's model.
			ep.Model = "unknown"
			if tt.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			tool := LLMTool{Name: "read", LogsOwnResult: tt.logsOwn, Call: func(ctx context.Context, _ any) (any, error) {
				require.True(t, trace.SpanFromContext(ctx).IsRecording())
				if tt.cancel {
					return nil, ctx.Err()
				}
				return tt.text, tt.err
			}}
			tools := []LLMTool{tool}
			if tt.missing {
				tools = nil
			}
			args := tt.arguments
			if len(args) == 0 {
				args = JSON("{}")
			}
			displays := newDisplayPhases(ctx, "", tools)
			displays.EmitToolCall(0, "call", tool.Name, string(args))
			results, rest := newMCP().CallBatch(ctx, tools, []*LLMToolCall{{CallID: "call", Name: tool.Name, Arguments: args}}, displays.toolCalls, nil)
			require.Empty(t, rest)
			require.Len(t, results, 1)
			result := results[0].Content[0]
			if tt.name == "bounded" {
				require.Less(t, len(result.Text), len(tt.text))
			}
			require.Len(t, sr.Ended(), 1, "only the completed tool span, once")
			span := sr.Ended()[0]
			wantStatus := codes.Ok
			if tt.err != nil || tt.missing || len(tt.arguments) != 0 || tt.cancel {
				wantStatus = codes.Error
				require.True(t, result.Errored)
			}
			require.Equal(t, wantStatus, span.Status().Code)
			attrs := attribute.NewSet(span.Attributes()...)
			want := tokenAttrs(t, OpenAI, "gpt-4o", result.ContentText())
			n, _ := want.Value(telemetryattrs.LLMToolResultTokensAttr)
			requireTokenAttr(t, attrs, telemetryattrs.LLMToolResultTokensAttr, n.AsInt64())
			requireTokenAttr(t, attrs, telemetryattrs.LLMToolResultTokensMethodAttr, "tokenizer")
			requireTokenAttr(t, attrs, telemetryattrs.LLMToolResultTokensModelAttr, "gpt-4o")
		})
	}
}

func TestToolResultTokensTimeout(t *testing.T) {
	sr, ctx := recordingTestRecorder(t)
	ctx = withToolResultTokenModel(ctx, &LLMEndpoint{Provider: OpenAI, Model: "gpt-4"})
	m := newMCP()
	tools := NewLLMToolSet()
	tools.Add(LLMTool{Name: "wait", Call: func(ctx context.Context, _ any) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	m.loadBuiltins(nil, tools)
	args := JSON(`{"tool":"wait","duration":"1ms","arguments":{}}`)
	displays := newDisplayPhases(ctx, "", tools.Order)
	displays.EmitToolCall(0, "timeout", "Timeout", string(args))
	results, _ := m.CallBatch(ctx, tools.Order, []*LLMToolCall{{CallID: "timeout", Name: "Timeout", Arguments: args}}, displays.toolCalls, nil)
	require.True(t, results[0].Content[0].Errored)
	require.Len(t, sr.Ended(), 2)
	for _, span := range sr.Ended() {
		require.Equal(t, codes.Error, span.Status().Code)
		attrs := attribute.NewSet(span.Attributes()...)
		requireTokenAttr(t, attrs, telemetryattrs.LLMToolResultTokensMethodAttr, "tokenizer")
		requireTokenAttr(t, attrs, telemetryattrs.LLMToolResultTokensEncodingAttr, "cl100k_base")
		n, _ := attrs.Value(telemetryattrs.LLMToolResultTokensAttr)
		require.Positive(t, n.AsInt64())
	}
}

func TestToolResultTokensProviderContext(t *testing.T) {
	sr, ctx := recordingTestRecorder(t)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"reply","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"call","type":"function","function":{"name":"read","arguments":"{}"}}]}}]}`)
	}))
	t.Cleanup(server.Close)
	endpoint := &LLMEndpoint{Provider: OpenAI, Model: "gpt-4o", BaseURL: server.URL, Key: "test"}
	tools := []LLMTool{{Name: "read", Schema: map[string]any{"type": "object"}, Call: func(context.Context, any) (any, error) {
		return "お誕生日おめでとう", nil
	}}}
	response, err := newOpenAIClient(endpoint, "", true).SendQuery(ctx, []*LLMMessage{{
		Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "read"}},
	}}, tools, &LLMCallOpts{})
	require.NoError(t, err)
	// Dispatch uses the original ambient context, not the provider's context:
	// only the returned tool display can carry the already-resolved model.
	newMCP().CallBatch(ctx, tools, []*LLMToolCall{{CallID: "call", Name: "read"}}, response.ToolCallDisplays, nil)
	require.Equal(t, int64(1), requests.Load(), "counting must not make another request")
	var found bool
	for _, span := range sr.Ended() {
		if span.Name() == "read" {
			found = true
			attrs := attribute.NewSet(span.Attributes()...)
			requireTokenAttr(t, attrs, telemetryattrs.LLMToolResultTokensAttr, int64(7))
			requireTokenAttr(t, attrs, telemetryattrs.LLMToolResultTokensModelAttr, "gpt-4o")
			// Custom base URLs retain Provider=OpenAI, but model aliases on
			// compatible servers must not silently use OpenAI's tokenizer.
			requireTokenAttr(t, attrs, telemetryattrs.LLMToolResultTokensMethodAttr, "bytes_div_4")
			require.False(t, attrs.HasValue(telemetryattrs.LLMToolResultTokensEncodingAttr))
		}
	}
	require.True(t, found)
}

func TestToolResultTokensMediaProjection(t *testing.T) {
	sr, ctx := recordingTestRecorder(t)
	ctx = withToolResultTokenModel(ctx, &LLMEndpoint{Provider: OpenAI, Model: "gpt-4o"})
	tools := []LLMTool{{Name: "image", Call: func(context.Context, any) (any, error) {
		return &LLMContentBlock{Kind: LLMContentToolResult, Content: []*LLMContentBlock{
			{Kind: LLMContentText, Text: "caption"},
			{Kind: LLMContentImage, MIMEType: "image/png", Data: []byte("image")},
		}}, nil
	}}}
	displays := newDisplayPhases(ctx, "", tools)
	displays.EmitToolCall(0, "image", "image", "{}")
	results, _ := newMCP().CallBatch(ctx, tools, []*LLMToolCall{{CallID: "image", Name: "image"}}, displays.toolCalls, nil)
	require.Len(t, sr.Ended(), 1)
	span := sr.Ended()[0]
	attrs := attribute.NewSet(span.Attributes()...)
	requireTokenAttr(t, attrs, telemetryattrs.LLMToolResultTokensMethodAttr, "bytes_div_4")
	requireTokenAttr(t, attrs, telemetryattrs.LLMToolResultTokensScopeAttr, "text_projection")
	require.False(t, attrs.HasValue(telemetryattrs.LLMToolResultTokensEncodingAttr))
	requireTokenAttr(t, attrs, telemetryattrs.LLMToolResultTokensAttr, estimateTextTokens(len(results[0].Content[0].ContentText())))
}
