package telemetry

import (
	"context"
	"sync"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type LiveSpanProcessor struct {
	sdktrace.SpanProcessor

	activeSpans  map[spanKey]sdktrace.ReadOnlySpan
	activeSpansL *sync.Mutex

	heartbeatCtx    context.Context
	heartbeatCancel func()
}

type spanKey struct {
	traceID trace.TraceID
	spanID  trace.SpanID
}

const HeartbeatInterval = 30 * time.Second

func NewLiveSpanProcessor(exp sdktrace.SpanExporter) *LiveSpanProcessor {
	lsp := &LiveSpanProcessor{
		SpanProcessor: sdktrace.NewBatchSpanProcessor(
			exp,
			sdktrace.WithBatchTimeout(NearlyImmediate),
		),
		activeSpans:  map[spanKey]sdktrace.ReadOnlySpan{},
		activeSpansL: &sync.Mutex{},
	}

	lsp.heartbeatCtx, lsp.heartbeatCancel = context.WithCancel(context.Background())

	go lsp.heartbeat()

	return lsp
}

func (p *LiveSpanProcessor) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	live := liveSpan{SnapshotSpan(span)}
	p.SpanProcessor.OnEnd(live)
	p.activeSpansL.Lock()
	p.activeSpans[spanKey{
		span.SpanContext().TraceID(),
		span.SpanContext().SpanID(),
	}] = live
	p.activeSpansL.Unlock()
}

func (p *LiveSpanProcessor) OnEnd(span sdktrace.ReadOnlySpan) {
	p.SpanProcessor.OnEnd(span)
	p.activeSpansL.Lock()
	delete(p.activeSpans, spanKey{
		span.SpanContext().TraceID(),
		span.SpanContext().SpanID(),
	})
	p.activeSpansL.Unlock()
}

func (p *LiveSpanProcessor) Shutdown(ctx context.Context) error {
	p.heartbeatCancel()
	return p.SpanProcessor.Shutdown(ctx)
}

func (p *LiveSpanProcessor) heartbeat() {
	ticker := time.NewTicker(HeartbeatInterval)
	for {
		select {
		case <-p.heartbeatCtx.Done():
			return
		case <-ticker.C:
			p.activeSpansL.Lock()
			for _, span := range p.activeSpans {
				p.SpanProcessor.OnEnd(span)
			}
			p.activeSpansL.Unlock()
		}
	}
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
