package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func requireByteAttr(t *testing.T, attrs attribute.Set, key string, want any) {
	t.Helper()
	value, ok := attrs.Value(attribute.Key(key))
	require.True(t, ok, "missing %s", key)
	require.Equal(t, want, value.AsInterface(), key)
}

// The real dispatch path must attach result bytes before ending its SDK span.
// Argument bytes precede decoding, defaults, error handling and execution.
func TestToolBytesThroughDispatch(t *testing.T) {
	largeCode, err := json.Marshal(map[string]string{"source": strings.Repeat("fmt.Println(\"こんにちは\")\n", 2000)})
	require.NoError(t, err)
	for _, tt := range []struct {
		name, args, result    string
		missing, fail, cancel bool
	}{
		{name: "unicode", args: `{"source":"こんにちは"}`, result: "こんにちは"},
		{name: "empty"},
		{name: "original JSON", args: " { \"source\" : \"x\", \"n\": 1.00 } \n", result: "ok"},
		{name: "large code tiny result", args: string(largeCode), result: "ok"},
		{name: "bounded result", args: "{}", result: strings.Repeat("long result\n", 20000)},
		{name: "malformed", args: "{"},
		{name: "invalid UTF-8", args: "\xff\xfe"},
		{name: "unknown", args: `{"source":"work"}`, missing: true},
		{name: "failure", args: "{}", fail: true},
		{name: "cancellation", args: "{}", cancel: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sr, ctx := recordingTestRecorder(t)
			if tt.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			tools := []LLMTool{{Name: "write", Call: func(ctx context.Context, args any) (any, error) {
				require.True(t, trace.SpanFromContext(ctx).IsRecording())
				args.(map[string]any)["engine_injected"] = "not model output"
				if tt.cancel {
					return nil, ctx.Err()
				}
				if tt.fail {
					return nil, errors.New("failed")
				}
				return tt.result, nil
			}}}
			if tt.missing {
				tools = nil
			}
			displays := newDisplayPhases(ctx, "", tools)
			phase := displays.StartToolCall(0, "call", "write", modelToolArguments)
			// Bytes add across chunks, even inside UTF-8 and JSON escapes. There
			// is no tokenizer, argument buffer, or model-specific state.
			for i := 0; i < len(tt.args); i += 7 {
				phase.writeToolArgs(tt.args[i:min(i+7, len(tt.args))])
			}
			displays.Close(0)
			results, _ := newMCP().CallBatch(ctx, tools, []*LLMToolCall{{CallID: "call", Name: "write", Arguments: JSON(tt.args)}}, displays.toolCalls, nil)
			require.Len(t, sr.Ended(), 1)
			attrs := attribute.NewSet(sr.Ended()[0].Attributes()...)
			requireByteAttr(t, attrs, telemetryattrs.LLMToolArgumentBytesAttr, int64(len(tt.args)))
			requireByteAttr(t, attrs, telemetryattrs.LLMToolArgumentBytesSourceAttr, "model")
			requireByteAttr(t, attrs, telemetryattrs.LLMToolArgumentBytesScopeAttr, "argument_json")
			result := results[0].Content[0]
			requireByteAttr(t, attrs, telemetryattrs.LLMToolResultBytesAttr, int64(len(result.Text)))
			requireByteAttr(t, attrs, telemetryattrs.LLMToolResultBytesScopeAttr, "text")
			require.False(t, attrs.HasValue(telemetryattrs.LLMToolResultTokensAttr), "do not relabel bytes as tokens")
			if result.Errored {
				require.Equal(t, codes.Error, sr.Ended()[0].Status().Code)
			}
			if tt.name == "bounded result" {
				require.Less(t, len(result.Text), len(tt.result))
			}
			if tt.name == "large code tiny result" {
				require.Greater(t, len(tt.args), len(result.Text)*100)
			}
		})
	}
}

func TestToolBytesTimeoutProvenance(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			sr, ctx := recordingTestRecorder(t)
			m := newMCP()
			tools := NewLLMToolSet()
			tools.Add(LLMTool{Name: "read", Call: func(ctx context.Context, _ any) (any, error) {
				if fail {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return "こんにちは", nil
			}})
			m.loadBuiltins(nil, tools)
			args := `{"tool":"Timeout","duration":"1s","arguments":{"tool":"read","duration":"1ms","arguments":{"x":"こんにちは"}}}`
			displays := newDisplayPhases(ctx, "", tools.Order)
			displays.EmitToolCall(0, "outer", "Timeout", args, modelToolArguments)
			results, _ := m.CallBatch(ctx, tools.Order, []*LLMToolCall{{CallID: "outer", Name: "Timeout", Arguments: JSON(args)}}, displays.toolCalls, nil)
			require.Equal(t, fail, results[0].Content[0].Errored)
			require.Len(t, sr.Ended(), 3)
			for _, span := range sr.Ended() {
				attrs := attribute.NewSet(span.Attributes()...)
				if span.SpanContext().SpanID() == displays.toolCalls["outer"].Span.SpanContext().SpanID() {
					requireByteAttr(t, attrs, telemetryattrs.LLMToolArgumentBytesSourceAttr, "model")
					requireByteAttr(t, attrs, telemetryattrs.LLMToolArgumentBytesAttr, int64(len(args)))
				} else {
					requireByteAttr(t, attrs, telemetryattrs.LLMToolArgumentBytesSourceAttr, "forwarded")
					n, ok := attrs.Value(telemetryattrs.LLMToolArgumentBytesAttr)
					require.True(t, ok)
					require.Positive(t, n.AsInt64(), "forwarded text still has a real footprint")
				}
				if fail {
					require.Equal(t, codes.Error, span.Status().Code)
				}
			}
		})
	}
}

func TestToolBytesMediaAndTextFields(t *testing.T) {
	for _, media := range []bool{false, true} {
		t.Run(fmt.Sprint(media), func(t *testing.T) {
			sr, ctx := recordingTestRecorder(t)
			tools := []LLMTool{{Name: "read", Call: func(context.Context, any) (any, error) {
				result := &LLMContentBlock{Kind: LLMContentToolResult, Text: "before", Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "こんにちは"}}}
				if media {
					result.Content = append(result.Content, &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/png", Data: []byte("image data")})
				}
				return result, nil
			}}}
			displays := newDisplayPhases(ctx, "", tools)
			displays.EmitToolCall(0, "call", "read", "{}", modelToolArguments)
			newMCP().CallBatch(ctx, tools, []*LLMToolCall{{CallID: "call", Name: "read"}}, displays.toolCalls, nil)
			require.Len(t, sr.Ended(), 1)
			attrs := attribute.NewSet(sr.Ended()[0].Attributes()...)
			requireByteAttr(t, attrs, telemetryattrs.LLMToolResultBytesAttr, int64(len("beforeこんにちは")))
			scope := "text"
			if media {
				scope = "text_only"
			}
			requireByteAttr(t, attrs, telemetryattrs.LLMToolResultBytesScopeAttr, scope)
		})
	}
}

func TestToolBytesSkippedAndDirectCalls(t *testing.T) {
	sr, ctx := recordingTestRecorder(t)
	tools := []LLMTool{{Name: "continue", ReturnsLLM: true, Call: func(context.Context, any) (any, error) { t.Fatal("must not execute"); return nil, nil }}}
	displays := newDisplayPhases(ctx, "", tools)
	const args = `{"source":"こんにちは"}`
	displays.EmitToolCall(0, "call", "continue", args, modelToolArguments)
	newMCP().CallBatch(ctx, tools, []*LLMToolCall{{CallID: "call", Name: "continue", Arguments: JSON(args)}}, displays.toolCalls, func(context.Context) error { return errors.New("load failed") })
	require.Len(t, sr.Ended(), 1)
	requireByteAttr(t, attribute.NewSet(sr.Ended()[0].Attributes()...), telemetryattrs.LLMToolArgumentBytesAttr, int64(len(args)))
	newMCP().CallContent(ctx, nil, &LLMToolCall{Name: "missing", Arguments: JSON(args)})
	trace.SpanFromContext(ctx).End()
	_, exists := spanAttr(sr.Ended()[1], telemetryattrs.LLMToolArgumentBytesAttr)
	require.False(t, exists, "a direct call has no model-generated argument display")
}

func TestToolBytesArgumentProvenance(t *testing.T) {
	for _, tt := range []struct {
		provenance    toolArgumentProvenance
		source, scope string
	}{
		{modelToolArguments, "model", "argument_json"},
		{reserializedToolArguments, "model", "reserialized_argument_json"},
		{recordedToolArguments, "recorded", "argument_json"},
		{forwardedToolArguments, "forwarded", "argument_json"},
		{unknownToolArguments, "unknown", "argument_json"},
	} {
		t.Run(tt.source+tt.scope, func(t *testing.T) {
			t.Parallel()
			sr, ctx := recordingTestRecorder(t)
			displays := newDisplayPhases(ctx, "", nil)
			displays.EmitToolCall(0, "call", "missing", `{"x":"こんにちは"}`, tt.provenance)
			newMCP().CallBatch(ctx, nil, []*LLMToolCall{{CallID: "call", Name: "missing"}}, displays.toolCalls, nil)
			attrs := attribute.NewSet(sr.Ended()[0].Attributes()...)
			requireByteAttr(t, attrs, telemetryattrs.LLMToolArgumentBytesAttr, int64(len(`{"x":"こんにちは"}`)))
			requireByteAttr(t, attrs, telemetryattrs.LLMToolArgumentBytesSourceAttr, tt.source)
			requireByteAttr(t, attrs, telemetryattrs.LLMToolArgumentBytesScopeAttr, tt.scope)
		})
	}
}

func TestToolBytesOpenAIOriginalAndTruncatedArguments(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, finish := range []string{"tool_calls", "length"} {
			for _, raw := range []string{"", "  { \"x\" : 1.00 }  ", `{"source":"こんにちは`} {
				t.Run(fmt.Sprintf("stream=%t/%s/%s", streaming, finish, raw), func(t *testing.T) {
					sr, ctx := recordingTestRecorder(t)
					var requests, executions atomic.Int64
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						requests.Add(1)
						if streaming {
							w.Header().Set("Content-Type", "text/event-stream")
							fmt.Fprintf(w, "data: {\"id\":\"reply\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call\",\"type\":\"function\",\"function\":{\"name\":\"write\",\"arguments\":%q}}]}}]}\n\n", raw)
							fmt.Fprintf(w, "data: {\"id\":\"reply\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":%q}]}\n\ndata: [DONE]\n\n", finish)
						} else {
							w.Header().Set("Content-Type", "application/json")
							fmt.Fprintf(w, `{"id":"reply","object":"chat.completion","choices":[{"index":0,"finish_reason":%q,"message":{"role":"assistant","tool_calls":[{"id":"call","type":"function","function":{"name":"write","arguments":%q}}]}}]}`, finish, raw)
						}
					}))
					t.Cleanup(server.Close)
					tools := []LLMTool{{Name: "write", Schema: map[string]any{"type": "object"}, Call: func(context.Context, any) (any, error) { executions.Add(1); return "ok", nil }}}
					endpoint := &LLMEndpoint{Provider: OpenAI, Model: "any-model", BaseURL: server.URL, Key: "test"}
					response, err := newOpenAIClient(endpoint, "", !streaming).SendQuery(ctx, []*LLMMessage{{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "write"}}}}, tools, &LLMCallOpts{})
					if finish == "length" {
						var rejected *ModelFinishedError
						require.ErrorAs(t, err, &rejected)
						require.Nil(t, response)
						require.Zero(t, executions.Load())
					} else {
						require.NoError(t, err)
						block := response.Content[0]
						if raw == "" {
							require.Equal(t, "{}", string(block.Arguments))
						}
						newMCP().CallBatch(ctx, tools, []*LLMToolCall{{CallID: block.CallID, Name: block.ToolName, Arguments: block.Arguments}}, response.ToolCallDisplays, nil)
					}
					require.Equal(t, int64(1), requests.Load())
					var found bool
					for _, span := range sr.Ended() {
						if span.Name() == "write" {
							found = true
							attrs := attribute.NewSet(span.Attributes()...)
							requireByteAttr(t, attrs, telemetryattrs.LLMToolArgumentBytesAttr, int64(len(raw)))
							requireByteAttr(t, attrs, telemetryattrs.LLMToolArgumentBytesSourceAttr, "model")
							if finish == "length" {
								require.False(t, attrs.HasValue(telemetryattrs.LLMToolResultBytesAttr))
							}
						}
					}
					require.True(t, found, "rejected generation still has an argument footprint")
				})
			}
		}
	}
}
