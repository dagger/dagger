package tracing

import (
	"context"
	"log/slog"
	"sync"

	"github.com/dagger/dagger/telemetry/sdklog"
	"github.com/moby/buildkit/identity"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

type PubSub struct {
	spanSubs  map[trace.TraceID][]sdktrace.SpanExporter
	spanSubsL sync.Mutex
	logSubs   map[trace.TraceID][]sdklog.LogExporter
	logSubsL  sync.Mutex
}

func NewPubSub() *PubSub {
	return &PubSub{
		spanSubs: map[trace.TraceID][]sdktrace.SpanExporter{},
		logSubs:  map[trace.TraceID][]sdklog.LogExporter{},
	}
}

func (ps *PubSub) SubscribeToSpans(ctx context.Context, traceID trace.TraceID, exp sdktrace.SpanExporter) {
	slog.Debug("subscribing to spans", "trace", traceID.String())
	ps.spanSubsL.Lock()
	ps.spanSubs[traceID] = append(ps.spanSubs[traceID], exp)
	ps.spanSubsL.Unlock()
	defer ps.unsubSpans(traceID, exp)
	<-ctx.Done()
}

var _ sdktrace.SpanExporter = (*PubSub)(nil)

func (ps *PubSub) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	export := identity.NewID()

	slog.Debug("exporting spans to pubsub", "call", export, "spans", len(spans))

	byTrace := map[trace.TraceID][]sdktrace.ReadOnlySpan{}
	for _, s := range spans {
		traceID := s.SpanContext().TraceID()
		slog.Debug("pubsub exporting span", "call", export, "trace", traceID.String(), "id", s.SpanContext().SpanID(), "span", s.Name(), "status", s.Status().Code, "endTime", s.EndTime())
		byTrace[traceID] = append(byTrace[traceID], s)
	}

	eg := new(errgroup.Group)

	// export to local subscribers
	for traceID, spans := range byTrace {
		traceID := traceID
		spans := spans
		for _, sub := range ps.SpanSubscribers(traceID) {
			sub := sub
			eg.Go(func() error {
				slog.Debug("exporting spans to subscriber", "trace", traceID.String(), "spans", len(spans))
				return sub.ExportSpans(ctx, spans)
			})
		}
	}

	// export to global subscribers
	for _, sub := range ps.SpanSubscribers(trace.TraceID{}) {
		sub := sub
		eg.Go(func() error {
			slog.Debug("exporting spans to global subscriber", "spans", len(spans))
			return sub.ExportSpans(ctx, spans)
		})
	}

	return eg.Wait()
}

func (ps *PubSub) SpanSubscribers(session trace.TraceID) []sdktrace.SpanExporter {
	ps.spanSubsL.Lock()
	defer ps.spanSubsL.Unlock()
	subs := ps.spanSubs[session]
	cp := make([]sdktrace.SpanExporter, len(subs))
	copy(cp, subs)
	return cp
}

func (ps *PubSub) SubscribeToLogs(ctx context.Context, traceID trace.TraceID, exp sdklog.LogExporter) {
	slog.Debug("subscribing to logs", "trace", traceID.String())
	ps.logSubsL.Lock()
	ps.logSubs[traceID] = append(ps.logSubs[traceID], exp)
	ps.logSubsL.Unlock()
	defer ps.unsubLogs(traceID, exp)
	<-ctx.Done()
}

var _ sdklog.LogExporter = (*PubSub)(nil)

func (ps *PubSub) ExportLogs(ctx context.Context, logs []*sdklog.LogData) error {
	slog.Debug("exporting logs to pub/sub", "logs", len(logs))

	byTrace := map[trace.TraceID][]*sdklog.LogData{}
	for _, s := range logs {
		traceID := s.TraceID
		byTrace[traceID] = append(byTrace[traceID], s)
	}

	eg := new(errgroup.Group)

	// export to local subscribers
	for traceID, logs := range byTrace {
		traceID := traceID
		logs := logs
		for _, sub := range ps.LogSubscribers(traceID) {
			sub := sub
			eg.Go(func() error {
				slog.Debug("exporting logs to subscriber", "trace", traceID.String(), "logs", len(logs))
				return sub.ExportLogs(ctx, logs)
			})
		}
	}

	// export to global subscribers
	for _, sub := range ps.LogSubscribers(trace.TraceID{}) {
		sub := sub
		eg.Go(func() error {
			slog.Debug("exporting logs to global subscriber", "logs", len(logs))
			return sub.ExportLogs(ctx, logs)
		})
	}

	return eg.Wait()
}

func (ps *PubSub) LogSubscribers(session trace.TraceID) []sdklog.LogExporter {
	ps.logSubsL.Lock()
	defer ps.logSubsL.Unlock()
	subs := ps.logSubs[session]
	cp := make([]sdklog.LogExporter, len(subs))
	copy(cp, subs)
	return cp
}

func (ps *PubSub) Shutdown(ctx context.Context) error {
	slog.Debug("shutting down otel pub/sub")
	ps.spanSubsL.Lock()
	defer ps.spanSubsL.Unlock()
	eg := new(errgroup.Group)
	for _, ses := range ps.spanSubs {
		for _, se := range ses {
			se := se
			eg.Go(func() error {
				return se.Shutdown(ctx)
			})
		}
	}
	return eg.Wait()
}

func (ps *PubSub) unsubSpans(traceID trace.TraceID, exp sdktrace.SpanExporter) {
	slog.Debug("unsubscribing from trace", "trace", traceID.String())
	ps.spanSubsL.Lock()
	removed := make([]sdktrace.SpanExporter, 0, len(ps.spanSubs[traceID])-1)
	for _, s := range ps.spanSubs[traceID] {
		if s != exp {
			removed = append(removed, s)
		}
	}
	ps.spanSubs[traceID] = removed
	ps.spanSubsL.Unlock()
}

func (ps *PubSub) unsubLogs(traceID trace.TraceID, exp sdklog.LogExporter) {
	slog.Debug("unsubscribing from logs", "trace", traceID.String())
	ps.logSubsL.Lock()
	removed := make([]sdklog.LogExporter, 0, len(ps.logSubs[traceID])-1)
	for _, s := range ps.logSubs[traceID] {
		if s != exp {
			removed = append(removed, s)
		}
	}
	ps.logSubs[traceID] = removed
	ps.logSubsL.Unlock()
}
