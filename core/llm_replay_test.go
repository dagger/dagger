package core

// The replay provider must build the same per-block display spans a streaming
// provider builds. Without them every replayed tool call ran under the shared
// evaluation-loop context, so every replay-driven test exercised a telemetry
// shape production never has (the tool-call Boundary span) -- a blind spot real
// bugs shipped through.

import (
	"context"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/telemetryattrs"
)

func replayTestRecorder(t *testing.T) (*tracetest.SpanRecorder, context.Context) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(sr),
	)
	// core.Tracer resolves the provider off the span in ctx.
	ctx, _ := tp.Tracer("llm-replay-test").Start(context.Background(), "root")
	return sr, ctx
}

func spanAttr(s sdktrace.ReadOnlySpan, key string) (attribute.Value, bool) {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestReplaySendQueryEmitsPerToolCallDisplaySpans(t *testing.T) {
	sr, ctx := replayTestRecorder(t)

	replayer := newHistoryReplay([]*LLMMessage{
		{
			Role: LLMMessageRoleAssistant,
			Content: []*LLMContentBlock{
				{Kind: LLMContentText, Text: "Doing the work."},
				{Kind: LLMContentToolCall, CallID: "call_1", ToolName: "read", Arguments: JSON(`{"path":"main.go"}`)},
				{Kind: LLMContentToolCall, CallID: "call_2", ToolName: "write", Arguments: JSON(`{"content":"hi"}`)},
			},
		},
	})

	res, err := replayer.SendQuery(ctx, nil, nil, &LLMCallOpts{CallDigest: "sha256:deadbeef"})
	require.NoError(t, err)

	// One display span per tool call, keyed by call ID -- so CallBatch parents
	// each tool's execution beneath its own call instead of the shared ctx.
	require.Len(t, res.ToolCallDisplays, 2)
	require.Len(t, res.DisplaySpans, 3) // text + two tool calls

	seen := map[trace.SpanID]bool{}
	for _, callID := range []string{"call_1", "call_2"} {
		tc, ok := res.ToolCallDisplays[callID]
		require.True(t, ok, "no display span for %s", callID)
		sid := trace.SpanContextFromContext(tc.Ctx).SpanID()
		require.True(t, sid.IsValid())
		require.Equal(t, tc.Span.SpanContext().SpanID(), sid)
		require.False(t, seen[sid], "tool calls must not share a display span")
		seen[sid] = true
	}

	// Every display span stays open for the evaluation loop to end, exactly as
	// a streaming provider hands them back.
	require.Empty(t, sr.Ended())

	// The loop ends them exactly as it does for a live provider: CallBatch ends
	// the tool-call spans, step() ends the rest.
	endToolCallDisplay(res.ToolCallDisplays, "call_1", false, "file contents")
	endToolCallDisplay(res.ToolCallDisplays, "call_2", false, "")
	endedByCallBatch := map[trace.Span]bool{}
	for _, tc := range res.ToolCallDisplays {
		endedByCallBatch[tc.Span] = true
	}
	for _, s := range res.DisplaySpans {
		if !endedByCallBatch[s] {
			s.End()
		}
	}

	byName := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range sr.Ended() {
		byName[s.Name()] = s
	}
	for _, toolName := range []string{"read", "write"} {
		s, ok := byName[toolName]
		require.True(t, ok, "no ended span named %q", toolName)

		v, ok := spanAttr(s, telemetry.LLMToolAttr)
		require.True(t, ok, "%s: missing tool attr", toolName)
		require.Equal(t, toolName, v.AsString())

		v, ok = spanAttr(s, telemetry.UIBoundaryAttr)
		require.True(t, ok, "%s: missing boundary attr", toolName)
		require.True(t, v.AsBool())

		v, ok = spanAttr(s, telemetry.LLMRoleAttr)
		require.True(t, ok, "%s: missing role attr", toolName)
		require.Equal(t, telemetry.LLMRoleAssistant, v.AsString())

		v, ok = spanAttr(s, telemetryattrs.LLMCallDigestAttr)
		require.True(t, ok, "%s: missing call digest attr", toolName)
		require.Equal(t, "sha256:deadbeef", v.AsString())

		// Tool calls hang off the assistant text that introduced them, the
		// same anchoring the streaming providers use.
		require.Equal(t, byName["LLM response"].SpanContext().SpanID(), s.Parent().SpanID())
	}

	// The result-size badge lands on the call that produced output.
	v, ok := spanAttr(byName["read"], telemetryattrs.LLMToolResultTokensAttr)
	require.True(t, ok, "read: missing result tokens attr")
	require.Greater(t, v.AsInt64(), int64(0))
}
