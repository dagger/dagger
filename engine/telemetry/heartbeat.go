package telemetry

import (
	"context"
	"sync"
	"time"

	"github.com/dagger/dagger/engine/slog"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const HeartbeatInterval = 30 * time.Second

// SpanHeartbeater is a SpanExporter that keeps track of live spans and
// re-exports them periodically to the underlying SpanExporter to indicate that
// they are indeed still live.
type SpanHeartbeater struct {
	sdktrace.SpanExporter

	activeSpans  map[spanKey]sdktrace.ReadOnlySpan
	activeSpansL *sync.Mutex

	heartbeatCtx    context.Context
	heartbeatCancel func()
}

func NewSpanHeartbeater(exp sdktrace.SpanExporter) *SpanHeartbeater {
	lsp := &SpanHeartbeater{
		SpanExporter: exp,
		activeSpans:  map[spanKey]sdktrace.ReadOnlySpan{},
		activeSpansL: &sync.Mutex{},
	}

	lsp.heartbeatCtx, lsp.heartbeatCancel = context.WithCancel(context.Background())

	go lsp.heartbeat()

	return lsp
}

func (p *SpanHeartbeater) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	p.activeSpansL.Lock()

	// NOTE:intentionally holding lock while we export and while we heartbeat to
	// make sure we don't heartbeat a live span just after it completes.
	defer p.activeSpansL.Unlock()

	for _, span := range spans {
		key := spanKey{
			span.SpanContext().TraceID(),
			span.SpanContext().SpanID(),
		}
		if span.EndTime().After(span.StartTime()) {
			delete(p.activeSpans, key)
		} else {
			p.activeSpans[key] = span
		}
	}

	return p.SpanExporter.ExportSpans(ctx, spans)
}

func (p *SpanHeartbeater) Shutdown(ctx context.Context) error {
	p.heartbeatCancel()
	return p.SpanExporter.Shutdown(ctx)
}

func (p *SpanHeartbeater) heartbeat() {
	ticker := time.NewTicker(HeartbeatInterval)
	for {
		select {
		case <-p.heartbeatCtx.Done():
			return
		case <-ticker.C:
			p.activeSpansL.Lock()
			var stayinAlive []sdktrace.ReadOnlySpan
			for _, span := range p.activeSpans {
				stayinAlive = append(stayinAlive, span)
			}
			if len(stayinAlive) > 0 {
				if err := p.SpanExporter.ExportSpans(p.heartbeatCtx, stayinAlive); err != nil {
					slog.Warn("failed to heartbeat live spans", "error", err)
				}
			}
			p.activeSpansL.Unlock()
		}
	}
}
