package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/genai"
)

type usageFixtureTransport func(*http.Request) (*http.Response, error)

func (f usageFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func usageTestContext(t *testing.T) (context.Context, *tracetest.SpanRecorder) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { require.NoError(t, tp.Shutdown(context.Background())) })
	ctx, parent := tp.Tracer("test").Start(t.Context(), "agent")
	t.Cleanup(func() { parent.End() })
	return testAgentContext(t, ctx, "owner-id", "owner"), recorder
}

func usageSpans(recorder *tracetest.SpanRecorder) []sdktrace.ReadOnlySpan {
	var spans []sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.Name() == telemetryattrs.LLMUsageSpan {
			spans = append(spans, span)
		}
	}
	return spans
}

func usageAttrs(span sdktrace.ReadOnlySpan) map[string]attribute.Value {
	result := map[string]attribute.Value{}
	for _, attr := range span.Attributes() {
		result[string(attr.Key)] = attr.Value
	}
	return result
}

func usageSSE(events ...string) string {
	var result strings.Builder
	for _, event := range events {
		fmt.Fprintf(&result, "data: %s\n\n", event)
	}
	return result.String()
}

const anthropicUsageStart = `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"test-model","content":[],"usage":{"input_tokens":10,"output_tokens":0,"cache_read_input_tokens":5,"cache_creation_input_tokens":0}}}`
const anthropicUsageDelta = `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`
const anthropicUsageStop = `{"type":"message_stop"}`
const chatUsage = `{"id":"c","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13,"prompt_tokens_details":{"cached_tokens":5}}}`
const responsesUsage = `{"type":"response.completed","response":{"id":"r","object":"response","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13,"input_tokens_details":{"cached_tokens":5}}}}`
const googleUsage = `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"cachedContentTokenCount":5,"thoughtsTokenCount":2,"totalTokenCount":15}}`

// Exercise the actual clients and the production transport stack, not just the
// parser. Equal successive requests must be independently countable; exporting
// their spans again must not turn them into new responses.
func TestLLMUsageSDKs(t *testing.T) {
	for _, protocol := range []string{"anthropic", "openai", "responses", "codex", "google"} {
		for _, streaming := range []bool{false, true} {
			if protocol == "codex" && !streaming {
				continue
			} // The Codex backend only streams.
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, streaming), func(t *testing.T) {
				ctx, recorder := usageTestContext(t)
				provider := map[string]LLMProvider{"anthropic": Anthropic, "openai": OpenRouter, "responses": OpenAI, "codex": OpenAICodex, "google": Google}[protocol]
				body := ""
				switch protocol {
				case "anthropic":
					body = `{"id":"m","type":"message","role":"assistant","model":"test-model","content":[],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":3,"cache_read_input_tokens":5,"cache_creation_input_tokens":0}}`
					if streaming {
						body = usageSSE(anthropicUsageStart, anthropicUsageDelta, anthropicUsageDelta, anthropicUsageStop)
					}
				case "openai":
					body = strings.Replace(chatUsage, "chat.completion.chunk", "chat.completion", 1)
					if streaming {
						body = usageSSE(chatUsage, chatUsage, "[DONE]")
					}
				case "responses", "codex":
					body = `{"id":"r","object":"response","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13,"input_tokens_details":{"cached_tokens":5}}}`
					if streaming {
						body = usageSSE(responsesUsage)
					}
				case "google":
					body = googleUsage
					if streaming {
						body = usageSSE(googleUsage)
					}
				}
				requests := 0
				endpoint := &LLMEndpoint{Provider: provider, Model: "test-model", Key: "mock-key", AuthToken: "mock-token", BaseURL: "https://fixture.invalid/v1/"}
				endpoint.httpTransport = usageFixtureTransport(func(r *http.Request) (*http.Response, error) {
					requests++
					// The usage wrapper must not reparent the existing debug span.
					require.NotEqual(t, telemetryattrs.LLMUsageSpan, trace.SpanFromContext(r.Context()).(sdktrace.ReadOnlySpan).Name())
					header := http.Header{"Content-Type": []string{"application/json"}}
					if streaming && protocol != "openai" {
						header.Set("Content-Type", "text/event-stream")
					}
					return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
				})
				for range 2 {
					callUsageSDK(t, ctx, endpoint, protocol, streaming)
				}
				require.Equal(t, 2, requests)
				assertUsageSDKSpans(t, ctx, recorder, provider, protocol, endpoint.Model)
			})
		}
	}
}

func callUsageSDK(t *testing.T, ctx context.Context, endpoint *LLMEndpoint, protocol string, streaming bool) {
	t.Helper()
	switch protocol {
	case "anthropic":
		c := newAnthropicClient(endpoint)
		params := anthropic.MessageNewParams{Model: endpoint.Model, MaxTokens: 20, Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("private prompt"))}}
		if streaming {
			s := c.client.Messages.NewStreaming(ctx, params)
			for s.Next() {
			}
			require.NoError(t, s.Err())
			require.NoError(t, s.Close())
		} else {
			_, err := c.client.Messages.New(ctx, params)
			require.NoError(t, err)
		}
	case "openai":
		c := newOpenAIClient(endpoint, "", false)
		params := openai.ChatCompletionNewParams{Model: endpoint.Model, Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("private prompt")}}
		if streaming {
			s := c.chat.NewStreaming(ctx, params)
			for s.Next() {
			}
			require.NoError(t, s.Err())
			require.NoError(t, s.Close())
		} else {
			_, err := c.chat.New(ctx, params)
			require.NoError(t, err)
		}
	case "responses", "codex":
		c := newOpenAIResponsesClient(endpoint)
		if protocol == "codex" {
			c = newOpenAICodexClient(endpoint)
		}
		params := responses.ResponseNewParams{Model: endpoint.Model}
		if streaming {
			s := c.svc.NewStreaming(ctx, params)
			for s.Next() {
			}
			require.NoError(t, s.Err())
			require.NoError(t, s.Close())
		} else {
			_, err := c.svc.New(ctx, params)
			require.NoError(t, err)
		}
	case "google":
		c, err := newGenaiClient(endpoint)
		require.NoError(t, err)
		content := []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "private prompt"}}}}
		if streaming {
			for _, err := range c.client.Models.GenerateContentStream(ctx, endpoint.Model, content, nil) {
				require.NoError(t, err)
			}
		} else {
			_, err := c.client.Models.GenerateContent(ctx, endpoint.Model, content, nil)
			require.NoError(t, err)
		}
	}
}

func assertUsageSDKSpans(t *testing.T, ctx context.Context, recorder *tracetest.SpanRecorder, provider LLMProvider, protocol, model string) {
	t.Helper()
	spans := usageSpans(recorder)
	require.Len(t, spans, 2)
	require.NotEqual(t, spans[0].SpanContext().SpanID(), spans[1].SpanContext().SpanID())
	for _, span := range spans {
		attrs := usageAttrs(span)
		require.Equal(t, trace.SpanContextFromContext(ctx).SpanID(), span.Parent().SpanID())
		require.Equal(t, attribute.StringValue("1"), attrs[telemetryattrs.LLMUsageSchemaAttr])
		require.Equal(t, attribute.StringValue("http_round_trip"), attrs[telemetryattrs.LLMUsageScopeAttr])
		require.Equal(t, attribute.StringValue("owner-id"), attrs[telemetryattrs.GenAIAgentIDAttr])
		require.Equal(t, attribute.StringValue(string(provider)), attrs[telemetryattrs.GenAIProviderNameAttr])
		require.Equal(t, attribute.StringValue(model), attrs[telemetryattrs.GenAIRequestModelAttr])
		require.Equal(t, attribute.StringValue("final"), attrs[telemetryattrs.LLMUsageCompletenessAttr])
		require.Equal(t, attribute.StringValue("success"), attrs[telemetryattrs.LLMUsageOutcomeAttr])
		require.Equal(t, attribute.Int64Value(10), attrs[telemetryattrs.LLMUsageInputTokensAttr])
		require.Equal(t, attribute.Int64Value(3), attrs[telemetryattrs.LLMUsageOutputTokensAttr])
		require.Equal(t, attribute.Int64Value(5), attrs[telemetryattrs.LLMUsageCacheReadTokensAttr])
		require.Equal(t, attribute.BoolValue(true), attrs[telemetry.UIInternalAttr])
		require.NotContains(t, attrs, telemetryattrs.AgentAttr)
		require.Empty(t, span.Events())
		input, output := "includes_cache_read", "includes_reasoning"
		if protocol == "anthropic" {
			input = "excludes_cache_read_and_write"
			require.Equal(t, attribute.Int64Value(0), attrs[telemetryattrs.LLMUsageCacheWriteTokensAttr])
		} else {
			require.NotContains(t, attrs, telemetryattrs.LLMUsageCacheWriteTokensAttr)
		}
		if protocol == "google" {
			output = "excludes_reasoning"
			require.Equal(t, attribute.Int64Value(2), attrs[telemetryattrs.LLMUsageReasoningTokensAttr])
		}
		require.Equal(t, attribute.StringValue(input), attrs[telemetryattrs.LLMUsageInputSemanticsAttr])
		require.Equal(t, attribute.StringValue(output), attrs[telemetryattrs.LLMUsageOutputSemanticsAttr])
		require.NotContains(t, fmt.Sprint(attrs), "private prompt")
	}
	// Re-exporting the same stable span identities remains two records.
	exporter := tracetest.NewInMemoryExporter()
	require.NoError(t, exporter.ExportSpans(ctx, spans))
	require.NoError(t, exporter.ExportSpans(ctx, spans))
	identities := map[string]bool{}
	for _, span := range exporter.GetSpans() {
		identities[span.SpanContext.TraceID().String()+"/"+span.SpanContext.SpanID().String()] = true
	}
	require.Len(t, identities, 2)
	for _, span := range recorder.Ended() {
		if strings.HasPrefix(span.Name(), "LLM HTTP") {
			require.Equal(t, trace.SpanContextFromContext(ctx).SpanID(), span.Parent().SpanID())
		}
	}
}

// A body can return data together with EOF or another error. Observation must
// retain those bytes and preserve both the count and exact error for the SDK.
type usageTailBody struct {
	data              string
	readErr, closeErr error
	closed            int
}

func (b *usageTailBody) Read(p []byte) (int, error) {
	n := copy(p, b.data)
	b.data = b.data[n:]
	if len(b.data) == 0 {
		return n, b.readErr
	}
	return n, nil
}
func (b *usageTailBody) Close() error { b.closed++; return b.closeErr }

func TestLLMUsageLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, path, body, complete, outcome string
		readErr, closeErr                   error
		wantInput                           *int64
	}{
		{name: "anthropic partial", path: "messages", body: usageSSE(anthropicUsageStart), complete: "partial", outcome: "interrupted", readErr: io.EOF, wantInput: usageInt(10)},
		{name: "anthropic error", path: "messages", body: usageSSE(anthropicUsageStart, `{"type":"error","error":{"message":"private failure"}}`), complete: "partial", outcome: "error", readErr: io.EOF, wantInput: usageInt(10)},
		{name: "anthropic final", path: "messages", body: usageSSE(anthropicUsageStart, anthropicUsageDelta, anthropicUsageStop), complete: "final", outcome: "success", readErr: io.EOF, wantInput: usageInt(10)},
		{name: "zero", path: "chat/completions", body: usageSSE(`{"usage":{"prompt_tokens":0,"completion_tokens":0}}`, "[DONE]"), complete: "final", outcome: "success", readErr: io.EOF, wantInput: usageInt(0)},
		{name: "absent", path: "chat/completions", body: usageSSE(`{"usage":{}}`, "[DONE]"), complete: "unavailable", outcome: "success", readErr: io.EOF},
		{name: "null", path: "chat/completions", body: usageSSE(`{"usage":{"prompt_tokens":null}}`, "[DONE]"), complete: "unavailable", outcome: "success", readErr: io.EOF},
		{name: "no done", path: "chat/completions", body: usageSSE(chatUsage), complete: "partial", outcome: "interrupted", readErr: io.EOF, wantInput: usageInt(10)},
		{name: "finish reason is not done", path: "chat/completions", body: usageSSE(chatUsage, `{"choices":[{"finish_reason":"stop"}]}`), complete: "partial", outcome: "interrupted", readErr: io.EOF, wantInput: usageInt(10)},
		{name: "read error", path: "chat/completions", body: usageSSE(chatUsage), complete: "partial", outcome: "error", readErr: io.ErrUnexpectedEOF, wantInput: usageInt(10)},
		{name: "cancelled", path: "messages", body: usageSSE(anthropicUsageStart), complete: "partial", outcome: "cancelled", readErr: context.Canceled, wantInput: usageInt(10)},
		{name: "early close", path: "chat/completions", body: usageSSE(chatUsage), complete: "partial", outcome: "interrupted", wantInput: usageInt(10)},
		{name: "close after terminal", path: "chat/completions", body: usageSSE(chatUsage, "[DONE]"), complete: "final", outcome: "success", wantInput: usageInt(10)},
		{name: "close error after terminal", path: "chat/completions", body: usageSSE(chatUsage, "[DONE]"), complete: "final", outcome: "error", closeErr: io.ErrClosedPipe, wantInput: usageInt(10)},
		{name: "responses failed final", path: "responses", body: usageSSE(strings.ReplaceAll(responsesUsage, "completed", "failed")), complete: "final", outcome: "error", readErr: io.EOF, wantInput: usageInt(10)},
		{name: "responses incomplete final", path: "responses", body: usageSSE(strings.ReplaceAll(responsesUsage, "completed", "incomplete")), complete: "final", outcome: "error", readErr: io.EOF, wantInput: usageInt(10)},
		{name: "responses no terminal usage", path: "responses", body: usageSSE(strings.ReplaceAll(responsesUsage, "completed", "in_progress"), `{"type":"response.failed","response":{"status":"failed"}}`), complete: "partial", outcome: "error", readErr: io.EOF, wantInput: usageInt(10)},
		{name: "google final", path: "models/test:streamGenerateContent", body: usageSSE(googleUsage), complete: "final", outcome: "success", readErr: io.EOF, wantInput: usageInt(10)},
		{name: "google no finish", path: "models/test:streamGenerateContent", body: usageSSE(strings.ReplaceAll(googleUsage, `"finishReason":"STOP"`, `"finishReason":""`)), complete: "partial", outcome: "interrupted", readErr: io.EOF, wantInput: usageInt(10)},
		{name: "google blocked", path: "models/test:streamGenerateContent", body: usageSSE(`{"promptFeedback":{"blockReason":"SAFETY"},"usageMetadata":{"promptTokenCount":10}}`), complete: "final", outcome: "error", readErr: io.EOF, wantInput: usageInt(10)},
		{name: "google array", path: "models/test:streamGenerateContent", body: "[" + googleUsage + "]", complete: "final", outcome: "success", readErr: io.EOF, wantInput: usageInt(10)},
		{name: "google truncated array", path: "models/test:streamGenerateContent", body: "[" + googleUsage, complete: "partial", outcome: "interrupted", readErr: io.EOF, wantInput: usageInt(10)},
		{name: "broken event loses certainty", path: "chat/completions", body: usageSSE(chatUsage, `{"usage":`, "[DONE]"), complete: "partial", outcome: "success", readErr: io.EOF, wantInput: usageInt(10)},
		{name: "omitted category remains partial", path: "chat/completions", body: usageSSE(chatUsage, `{"usage":{"completion_tokens":4}}`, "[DONE]"), complete: "partial", outcome: "success", readErr: io.EOF, wantInput: usageInt(10)},
		{name: "anthropic initial output is not final", path: "messages", body: usageSSE(anthropicUsageStart, `{"type":"message_delta","usage":{"input_tokens":10}}`, anthropicUsageStop), complete: "partial", outcome: "success", readErr: io.EOF, wantInput: usageInt(10)},
		{name: "later usage recovers certainty", path: "chat/completions", body: usageSSE(chatUsage, `{"usage":`, chatUsage, "[DONE]"), complete: "final", outcome: "success", readErr: io.EOF, wantInput: usageInt(10)},
		{name: "invalid number", path: "chat/completions", body: usageSSE(`{"usage":{"prompt_tokens":-1,"completion_tokens":1.2,"total_tokens":9223372036854775808}}`, "[DONE]"), complete: "unavailable", outcome: "success", readErr: io.EOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, recorder := usageTestContext(t)
			body := &usageTailBody{data: tc.body, readErr: tc.readErr, closeErr: tc.closeErr}
			transport := &llmUsageTransport{provider: "test", model: "test", base: usageFixtureTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: body}, nil
			})}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://fixture.invalid/v1/"+tc.path, nil)
			require.NoError(t, err)
			resp, err := transport.RoundTrip(req)
			require.NoError(t, err)
			buf := make([]byte, len(tc.body)+1)
			n, err := resp.Body.Read(buf)
			require.Equal(t, tc.readErr, err)
			require.Equal(t, tc.body, string(buf[:n]))
			require.Equal(t, tc.closeErr, resp.Body.Close())
			require.Equal(t, tc.closeErr, resp.Body.Close())
			spans := usageSpans(recorder)
			require.Len(t, spans, 1)
			attrs := usageAttrs(spans[0])
			require.Equal(t, attribute.StringValue(tc.complete), attrs[telemetryattrs.LLMUsageCompletenessAttr])
			require.Equal(t, attribute.StringValue(tc.outcome), attrs[telemetryattrs.LLMUsageOutcomeAttr])
			if tc.wantInput == nil {
				require.NotContains(t, attrs, telemetryattrs.LLMUsageInputTokensAttr)
			} else {
				require.Equal(t, attribute.Int64Value(*tc.wantInput), attrs[telemetryattrs.LLMUsageInputTokensAttr])
			}
			require.NotContains(t, fmt.Sprint(attrs), "private failure")
		})
	}
}

func usageInt(n int64) *int64 { return &n }

func TestLLMUsageBoundedFrames(t *testing.T) {
	for _, recoverUsage := range []bool{false, true} {
		t.Run(fmt.Sprint(recoverUsage), func(t *testing.T) {
			ctx, recorder := usageTestContext(t)
			large := `{"choices":[{"delta":{"content":"` + strings.Repeat("x", maxLLMUsageFrame*2) + `"}}]}`
			events := []string{chatUsage, large}
			if recoverUsage {
				events = append(events, chatUsage)
			}
			events = append(events, "[DONE]")
			input := usageSSE(events...)
			_, span := Tracer(ctx).Start(ctx, telemetryattrs.LLMUsageSpan)
			p := &llmUsageBody{body: io.NopCloser(strings.NewReader(input)), ctx: ctx, span: span, protocol: "openai", values: map[string]int64{}}
			var delivered strings.Builder
			buf := make([]byte, 37)
			for {
				n, err := p.Read(buf)
				delivered.Write(buf[:n])
				require.LessOrEqual(t, len(p.line), maxLLMUsageFrame)
				require.LessOrEqual(t, len(p.event), maxLLMUsageFrame)
				require.LessOrEqual(t, len(p.document), maxLLMUsageFrame)
				if err != nil {
					require.ErrorIs(t, err, io.EOF)
					break
				}
			}
			require.NoError(t, p.Close())
			require.Equal(t, input, delivered.String())
			spans := usageSpans(recorder)
			require.Len(t, spans, 1)
			completeness := "partial"
			if recoverUsage {
				completeness = "final"
			}
			require.Equal(t, attribute.StringValue(completeness), usageAttrs(spans[0])[telemetryattrs.LLMUsageCompletenessAttr])
		})
	}
}

func TestLLMUsageNonGenerationAndTransportError(t *testing.T) {
	ctx, recorder := usageTestContext(t)
	sentinel := errors.New("private transport failure")
	transport := &llmUsageTransport{base: usageFixtureTransport(func(*http.Request) (*http.Response, error) { return nil, sentinel })}
	for _, path := range []string{"models", "oauth/token", "messages/count_tokens", "chat/completions"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://fixture.invalid/v1/"+path, nil)
		require.NoError(t, err)
		resp, err := transport.RoundTrip(req)
		if resp != nil {
			require.NoError(t, resp.Body.Close())
		}
		require.ErrorIs(t, err, sentinel)
	}
	spans := usageSpans(recorder)
	require.Len(t, spans, 1)
	require.Equal(t, attribute.StringValue("unavailable"), usageAttrs(spans[0])[telemetryattrs.LLMUsageCompletenessAttr])
	require.Equal(t, "error", spans[0].Status().Description)
	require.Empty(t, spans[0].Events())
}

func TestLLMUsageSDKInterruptions(t *testing.T) {
	for _, tc := range []struct {
		protocol, body, complete string
		provider                 LLMProvider
		readErr                  error
	}{
		{"anthropic", usageSSE(anthropicUsageStart) + "event: error\n" + usageSSE(`{"type":"error","error":{"type":"overloaded_error","message":"private failure"}}`), "partial", Anthropic, io.EOF},
		{"openai", usageSSE(chatUsage), "partial", OpenAI, io.ErrUnexpectedEOF},
		{"responses", usageSSE(strings.ReplaceAll(responsesUsage, "completed", "failed")), "final", OpenAI, io.EOF},
		{"codex", usageSSE(strings.ReplaceAll(responsesUsage, "completed", "incomplete")), "final", OpenAICodex, io.EOF},
		{"google", usageSSE(googleUsage), "final", Google, context.Canceled},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			ctx, recorder := usageTestContext(t)
			endpoint := &LLMEndpoint{Provider: tc.provider, Model: "test-model", Key: "mock-key", AuthToken: "mock-token", BaseURL: "https://fixture.invalid/v1/"}
			endpoint.httpTransport = usageFixtureTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: &usageTailBody{data: tc.body, readErr: tc.readErr}, Request: r}, nil
			})
			switch tc.protocol {
			case "anthropic":
				c := newAnthropicClient(endpoint)
				s := c.client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{Model: endpoint.Model, MaxTokens: 20})
				for s.Next() {
				}
				require.Error(t, s.Err())
				_ = s.Close()
			case "openai":
				c := newOpenAIClient(endpoint, "", false)
				s := c.chat.NewStreaming(ctx, openai.ChatCompletionNewParams{Model: endpoint.Model})
				for s.Next() {
				}
				require.Error(t, s.Err())
				_ = s.Close()
			case "responses", "codex":
				c := newOpenAIResponsesClient(endpoint)
				if tc.protocol == "codex" {
					c = newOpenAICodexClient(endpoint)
				}
				s := c.svc.NewStreaming(ctx, responses.ResponseNewParams{Model: endpoint.Model})
				for s.Next() {
				}
				_ = s.Close()
			case "google":
				c, err := newGenaiClient(endpoint)
				require.NoError(t, err)
				// The SDK may stop at finishReason without surfacing a read error;
				// the transport observation must still preserve it in the outcome.
				for range c.client.Models.GenerateContentStream(ctx, endpoint.Model, []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "private prompt"}}}}, nil) {
				}
			}
			spans := usageSpans(recorder)
			require.Len(t, spans, 1)
			attrs := usageAttrs(spans[0])
			require.Equal(t, attribute.StringValue(tc.complete), attrs[telemetryattrs.LLMUsageCompletenessAttr])
			outcome := "error"
			if errors.Is(tc.readErr, context.Canceled) {
				outcome = "cancelled"
			}
			require.Equal(t, attribute.StringValue(outcome), attrs[telemetryattrs.LLMUsageOutcomeAttr])
			require.Equal(t, attribute.Int64Value(10), attrs[telemetryattrs.LLMUsageInputTokensAttr])
		})
	}
}

func TestLLMUsageSDKFailedFinish(t *testing.T) {
	for _, protocol := range []string{"anthropic", "openai", "responses"} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, streaming), func(t *testing.T) {
				ctx, recorder := usageTestContext(t)
				var body string
				switch protocol {
				case "anthropic":
					body = `{"id":"m","type":"message","role":"assistant","content":[],"stop_reason":"max_tokens","usage":{"input_tokens":10,"output_tokens":3}}`
					if streaming {
						body = usageSSE(anthropicUsageStart, strings.ReplaceAll(anthropicUsageDelta, "end_turn", "max_tokens"), anthropicUsageStop)
					}
				case "openai":
					body = `{"id":"c","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"partial"},"finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":3}}`
					if streaming {
						body = usageSSE(`{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`, chatUsage, "[DONE]")
					}
				case "responses":
					body = `{"id":"r","object":"response","status":"incomplete","output":[],"usage":{"input_tokens":10,"output_tokens":3}}`
					if streaming {
						body = usageSSE(strings.ReplaceAll(responsesUsage, "completed", "incomplete"))
					}
				}
				endpoint := &LLMEndpoint{Provider: OpenAI, Model: "test-model", Key: "mock-key", BaseURL: "https://fixture.invalid/v1/"}
				endpoint.httpTransport = usageFixtureTransport(func(r *http.Request) (*http.Response, error) {
					header := http.Header{"Content-Type": []string{"application/json"}}
					if streaming {
						header.Set("Content-Type", "text/event-stream")
					}
					return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
				})
				callUsageSDK(t, ctx, endpoint, protocol, streaming)
				spans := usageSpans(recorder)
				require.Len(t, spans, 1)
				attrs := usageAttrs(spans[0])
				require.Equal(t, attribute.StringValue("final"), attrs[telemetryattrs.LLMUsageCompletenessAttr])
				require.Equal(t, attribute.StringValue("error"), attrs[telemetryattrs.LLMUsageOutcomeAttr])
				require.Equal(t, attribute.Int64Value(3), attrs[telemetryattrs.LLMUsageOutputTokensAttr])
			})
		}
	}
}

type usageEOFBody struct {
	io.ReadCloser
	sawEOF, closed bool
}

func (b *usageEOFBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.sawEOF = b.sawEOF || errors.Is(err, io.EOF)
	return n, err
}
func (b *usageEOFBody) Close() error { b.closed = true; return b.ReadCloser.Close() }

func TestLLMUsageGoogleFailedConsumer(t *testing.T) {
	ctx, recorder := usageTestContext(t)
	body := &usageEOFBody{ReadCloser: io.NopCloser(strings.NewReader(usageSSE(strings.ReplaceAll(googleUsage, "STOP", "MAX_TOKENS"))))}
	endpoint := &LLMEndpoint{Provider: Google, Model: "test-model", Key: "mock-key"}
	endpoint.httpTransport = usageFixtureTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: body, Request: r}, nil
	})
	c, err := newGenaiClient(endpoint)
	require.NoError(t, err)
	stream := c.client.Models.GenerateContentStream(ctx, endpoint.Model, []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "private prompt"}}}}, nil)
	dp := newDisplayPhases(ctx, "", nil)
	defer dp.CloseAll()
	_, _, err = c.processStreamResponse(stream, dp, func(*genai.GenerateContentResponseUsageMetadata) LLMTokenUsage { return LLMTokenUsage{} })
	require.ErrorContains(t, err, "MAX_TOKENS")
	require.True(t, body.closed)
	require.False(t, body.sawEOF, "consumer exits on terminal failure before draining to EOF")
	spans := usageSpans(recorder)
	require.Len(t, spans, 1)
	attrs := usageAttrs(spans[0])
	require.Equal(t, attribute.StringValue("final"), attrs[telemetryattrs.LLMUsageCompletenessAttr])
	require.Equal(t, attribute.StringValue("error"), attrs[telemetryattrs.LLMUsageOutcomeAttr])
	require.Equal(t, attribute.Int64Value(3), attrs[telemetryattrs.LLMUsageOutputTokensAttr])
}

func TestLLMUsageSDKRetry(t *testing.T) {
	ctx, recorder := usageTestContext(t)
	requests := 0
	endpoint := &LLMEndpoint{Provider: OpenAI, Model: "test-model", BaseURL: "https://fixture.invalid/v1/"}
	endpoint.httpTransport = usageFixtureTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		status, body := 200, strings.Replace(chatUsage, "chat.completion.chunk", "chat.completion", 1)
		if requests == 1 {
			status, body = 429, `{"error":{"message":"private error","type":"rate_limit"}}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}, "Retry-After-Ms": []string{"1"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	_, err := newOpenAIClient(endpoint, "", false).chat.New(ctx, openai.ChatCompletionNewParams{Model: endpoint.Model}, option.WithMaxRetries(1))
	require.NoError(t, err)
	spans := usageSpans(recorder)
	require.Len(t, spans, 2)
	require.Equal(t, attribute.StringValue("unavailable"), usageAttrs(spans[0])[telemetryattrs.LLMUsageCompletenessAttr])
	require.Equal(t, attribute.StringValue("error"), usageAttrs(spans[0])[telemetryattrs.LLMUsageOutcomeAttr])
	require.Equal(t, attribute.StringValue("final"), usageAttrs(spans[1])[telemetryattrs.LLMUsageCompletenessAttr])
	require.NotContains(t, fmt.Sprint(usageAttrs(spans[0])), "private error")
}
