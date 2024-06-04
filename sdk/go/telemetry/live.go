package telemetry

import (
	"context"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type LiveSpanProcessor struct {
	sdktrace.SpanProcessor
}

type spanKey struct {
	traceID trace.TraceID
	spanID  trace.SpanID
}

func NewLiveSpanProcessor(exp sdktrace.SpanExporter) *LiveSpanProcessor {
	return &LiveSpanProcessor{
		SpanProcessor: sdktrace.NewBatchSpanProcessor(
			// NOTE: intentionally doesn't inject SpanHeartbeater; that is handled at
			// a higher level (in the engine and in the CLI) so that SDKs don't have
			// to implement it.
			exp,
			sdktrace.WithBatchTimeout(NearlyImmediate),
		),
	}
}

func (p *LiveSpanProcessor) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	// Send a read-only snapshot of the live span downstream so it can be
	// filtered out by FilterLiveSpansExporter. Otherwise the span can complete
	// before being exported, resulting in two completed spans being sent, which
	// will confuse traditional OpenTelemetry services.
	p.SpanProcessor.OnEnd(SnapshotSpan(span))
}
