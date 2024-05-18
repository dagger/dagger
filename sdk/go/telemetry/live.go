package telemetry

import (
	"context"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type LiveSpanProcessor struct {
	sdktrace.SpanProcessor
}

func NewLiveSpanProcessor(exp sdktrace.SpanExporter) *LiveSpanProcessor {
	return &LiveSpanProcessor{
		SpanProcessor: sdktrace.NewBatchSpanProcessor(
			exp,
			sdktrace.WithBatchTimeout(NearlyImmediate),
		),
	}
}

func (p LiveSpanProcessor) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	p.SpanProcessor.OnEnd(liveSpan{span})
}

// liveSpan is a poor man's span snapshot, ensuring that we're adding an
// unfinished span to the queue so that it can be filtered out before sending
// along to an exporter that doesn't handle live spans.
//
// Without this, the same span may be queued twice in the same batch; we need
// to make sure the original one from OnStart maintains EndTime as zero so that
// it will be filtered out.
type liveSpan struct {
	sdktrace.ReadOnlySpan
}

// EndTime unconditionally returns the zero time, indicating that the span is
// "live."
func (s liveSpan) EndTime() time.Time {
	return time.Time{}
}
