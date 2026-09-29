package core

import (
	"testing"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"

	"github.com/dagger/dagger/dagql"
)

// TestReseedRewindMarkerThroughLoopSpanCtx is TestReseedPublishesRewindMarker
// with rt.spanCtx set the way AgentRuntime.loop and create set it:
// agentTelemetryContext(loopCtx). The compacted context must still reach the
// loop span's TracerProvider and the agent identity, or the rewind marker that
// inline prompt editing relies on is never exported.
func TestReseedRewindMarkerThroughLoopSpanCtx(t *testing.T) {
	_, ctx := stateRecorderCtx(t)
	ctx = dagql.ContextWithCache(ctx, nil)
	ctx = testAgentContext(t, ctx, "agent-123", "reviewer")
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(spans),
	)
	loopCtx, loop := tp.Tracer("agent-rewind-test").Start(ctx, "agent loop")
	defer loop.End()

	srv := newCoreDagqlServerForTest(t, &Query{})
	srv.InstallObject(dagql.NewClass[*LLM](srv))
	llmFrame := testResultCall("llm", &LLM{}, nil)
	responseFrame := testResultCall("withResponse", &LLM{}, testResultCall("withPrompt", &LLM{}, llmFrame))
	base := llmChainResult(t, srv, llmFrame)
	tip := llmChainResult(t, srv, responseFrame)

	rt := testRuntime(t, loopCtx)
	rt.spanCtx = agentTelemetryContext(loopCtx)
	rt.last = tip
	require.NoError(t, rt.Reseed(ctx, base))

	require.Len(t, spans.Ended(), 1, "rewind marker span must be exported from the loop's telemetry context")
	marker := spans.Ended()[0]
	require.Equal(t, "conversation rewound", marker.Name())
	attrs := map[string]string{}
	for _, attr := range marker.Attributes() {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}
	require.Equal(t, "agent-123", attrs[string(semconv.GenAIAgentIDKey)])
}
