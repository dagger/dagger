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
	spanSubs  map[trace.TraceID][]spanSub
	spanSubsL sync.Mutex
	logSubs   map[trace.TraceID][]logSub
	logSubsL  sync.Mutex
}

func NewPubSub() *PubSub {
	return &PubSub{
		spanSubs: map[trace.TraceID][]spanSub{},
		logSubs:  map[trace.TraceID][]logSub{},
	}
}

type spanSub struct {
	exp  sdktrace.SpanExporter
	done chan struct{}
}

type logSub struct {
	exp  sdklog.LogExporter
	done chan struct{}
}

func (ps *PubSub) Drain(traceID trace.TraceID) {
	slog.Debug("draining trace", "trace", traceID.String())
	ps.spanSubsL.Lock()
	for _, s := range ps.spanSubs[traceID] {
		close(s.done)
	}
	for _, s := range ps.logSubs[traceID] {
		close(s.done)
	}
	ps.spanSubsL.Unlock()
}

func (ps *PubSub) SubscribeToSpans(ctx context.Context, traceID trace.TraceID, exp sdktrace.SpanExporter) {
	slog.Debug("subscribing to spans", "trace", traceID.String())

	done := make(chan struct{})

	ps.spanSubsL.Lock()
	ps.spanSubs[traceID] = append(ps.spanSubs[traceID], spanSub{
		exp:  exp,
		done: done,
	})
	ps.spanSubsL.Unlock()
	defer ps.unsubSpans(traceID, exp)

	select {
	case <-done:
		slog.Debug("pubsub spans drained", "trace", traceID)
	case <-ctx.Done():
		slog.Debug("pubsub spans canceled", "trace", traceID)
	}
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
	for i, sub := range subs {
		cp[i] = sub.exp
	}
	return cp
}

func (ps *PubSub) SubscribeToLogs(ctx context.Context, traceID trace.TraceID, exp sdklog.LogExporter) {
	slog.Debug("subscribing to logs", "trace", traceID.String())

	done := make(chan struct{})

	ps.logSubsL.Lock()
	ps.logSubs[traceID] = append(ps.logSubs[traceID], logSub{
		exp:  exp,
		done: done,
	})
	ps.logSubsL.Unlock()
	defer ps.unsubLogs(traceID, exp)

	select {
	case <-done:
		slog.Debug("pubsub logs drained", "trace", traceID)
	case <-ctx.Done():
		slog.Debug("pubsub logs canceled", "trace", traceID)
	}
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
	for i, sub := range subs {
		cp[i] = sub.exp
	}
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
				return se.exp.Shutdown(ctx)
			})
		}
	}
	return eg.Wait()
}

func (ps *PubSub) unsubSpans(traceID trace.TraceID, exp sdktrace.SpanExporter) {
	slog.Debug("unsubscribing from trace", "trace", traceID.String())
	ps.spanSubsL.Lock()
	removed := make([]spanSub, 0, len(ps.spanSubs[traceID])-1)
	for _, s := range ps.spanSubs[traceID] {
		if s.exp != exp {
			removed = append(removed, s)
		}
	}
	ps.spanSubs[traceID] = removed
	ps.spanSubsL.Unlock()
}

func (ps *PubSub) unsubLogs(traceID trace.TraceID, exp sdklog.LogExporter) {
	slog.Debug("unsubscribing from trace", "trace", traceID.String())
	ps.logSubsL.Lock()
	removed := make([]logSub, 0, len(ps.logSubs[traceID])-1)
	for _, s := range ps.logSubs[traceID] {
		if s.exp != exp {
			removed = append(removed, s)
		}
	}
	ps.logSubs[traceID] = removed
	ps.logSubsL.Unlock()
}
