package telemetry

import (
	"context"
	"log/slog"
	"sync"

	"github.com/dagger/dagger/telemetry/sdklog"
	"github.com/moby/buildkit/identity"
	"github.com/sourcegraph/conc/pool"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type PubSub struct {
	spanSubs  map[trace.TraceID][]sdktrace.SpanExporter
	spanSubsL sync.Mutex
	logSubs   map[trace.TraceID][]sdklog.LogExporter
	logSubsL  sync.Mutex
	traces    map[trace.TraceID]*activeTrace
	tracesL   sync.Mutex
}

func NewPubSub() *PubSub {
	return &PubSub{
		spanSubs: map[trace.TraceID][]sdktrace.SpanExporter{},
		logSubs:  map[trace.TraceID][]sdklog.LogExporter{},
		traces:   map[trace.TraceID]*activeTrace{},
	}
}

func (ps *PubSub) Drain(id trace.TraceID, immediate bool) {
	ps.tracesL.Lock()
	trace, ok := ps.traces[id]
	if ok {
		trace.cond.L.Lock()
		trace.draining = true
		trace.drainImmediately = immediate
		trace.cond.Broadcast()
		trace.cond.L.Unlock()
	}
	ps.tracesL.Unlock()
}

func (ps *PubSub) initTrace(id trace.TraceID) *activeTrace {
	if t, ok := ps.traces[id]; ok {
		return t
	}
	t := &activeTrace{
		id:          id,
		cond:        sync.NewCond(&sync.Mutex{}),
		activeSpans: map[trace.SpanID]struct{}{},
	}
	ps.traces[id] = t
	return t
}

func (ps *PubSub) SubscribeToSpans(ctx context.Context, traceID trace.TraceID, exp sdktrace.SpanExporter) error {
	slog.Debug("subscribing to spans", "trace", traceID.String())
	ps.tracesL.Lock()
	trace := ps.initTrace(traceID)
	ps.tracesL.Unlock()
	ps.spanSubsL.Lock()
	ps.spanSubs[traceID] = append(ps.spanSubs[traceID], exp)
	ps.spanSubsL.Unlock()
	defer ps.unsubSpans(traceID, exp)
	trace.wait(ctx)
	return nil
}

var _ sdktrace.SpanExporter = (*PubSub)(nil)

func (ps *PubSub) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	export := identity.NewID()

	slog.Debug("exporting spans to pubsub", "call", export, "spans", len(spans))

	byTrace := map[trace.TraceID][]sdktrace.ReadOnlySpan{}
	conds := map[trace.TraceID]*sync.Cond{}

	ps.tracesL.Lock()
	for _, s := range spans {
		traceID := s.SpanContext().TraceID()
		spanID := s.SpanContext().SpanID()

		slog.Debug("pubsub exporting span",
			"call", export,
			"trace", traceID.String(),
			"id", spanID,
			"span", s.Name(),
			"status", s.Status().Code,
			"endTime", s.EndTime())

		byTrace[traceID] = append(byTrace[traceID], s)

		activeTrace := ps.initTrace(traceID)

		if s.EndTime().Before(s.StartTime()) {
			activeTrace.startSpan(spanID)
		} else {
			activeTrace.finishSpan(spanID)
		}

		conds[traceID] = activeTrace.cond
	}
	ps.tracesL.Unlock()

	eg := pool.New().WithErrors()

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

	// notify anyone waiting to drain
	for _, cond := range conds {
		cond.Broadcast()
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

func (ps *PubSub) SubscribeToLogs(ctx context.Context, traceID trace.TraceID, exp sdklog.LogExporter) error {
	slog.Debug("subscribing to logs", "trace", traceID.String())
	ps.tracesL.Lock()
	trace := ps.initTrace(traceID)
	ps.tracesL.Lock()
	ps.logSubsL.Lock()
	ps.logSubs[traceID] = append(ps.logSubs[traceID], exp)
	ps.logSubsL.Unlock()
	defer ps.unsubLogs(traceID, exp)
	trace.wait(ctx)
	return nil
}

var _ sdklog.LogExporter = (*PubSub)(nil)

func (ps *PubSub) ExportLogs(ctx context.Context, logs []*sdklog.LogData) error {
	slog.Debug("exporting logs to pub/sub", "logs", len(logs))

	byTrace := map[trace.TraceID][]*sdklog.LogData{}
	for _, log := range logs {
		// NB: break glass if stuck troubleshooting otel  stuff
		// slog.Debug("exporting logs", "trace", log.Body().AsString())
		traceID := log.TraceID
		byTrace[traceID] = append(byTrace[traceID], log)
	}

	eg := pool.New().WithErrors()

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
	eg := pool.New().WithErrors()
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

// activeTrace keeps track of in-flight spans so that we can wait for them all
// to complete, ensuring we don't drop the last few spans, which ruins an
// entire trace.
type activeTrace struct {
	id               trace.TraceID
	activeSpans      map[trace.SpanID]struct{}
	draining         bool
	drainImmediately bool
	cond             *sync.Cond
}

func (trace *activeTrace) startSpan(id trace.SpanID) {
	trace.cond.L.Lock()
	trace.activeSpans[id] = struct{}{}
	trace.cond.L.Unlock()
}

func (trace *activeTrace) finishSpan(id trace.SpanID) {
	trace.cond.L.Lock()
	delete(trace.activeSpans, id)
	trace.cond.L.Unlock()
}

func (trace *activeTrace) wait(ctx context.Context) {
	slog := slog.With("trace", trace.id.String())

	go func() {
		// wake up the loop below if ctx context is interrupted
		<-ctx.Done()
		trace.cond.Broadcast()
	}()

	trace.cond.L.Lock()
	for !trace.draining || len(trace.activeSpans) > 0 {
		slog = slog.With(
			"draining", trace.draining,
			"immediate", trace.drainImmediately,
			"activeSpans", len(trace.activeSpans),
		)
		if ctx.Err() != nil {
			slog.Debug("wait interrupted")
			break
		}
		if trace.drainImmediately {
			slog.Debug("draining immediately")
			break
		}
		if trace.draining {
			slog.Debug("waiting for spans", "activeSpans", len(trace.activeSpans))
		}
		trace.cond.Wait()
	}
	slog.Debug("done waiting", "ctxErr", ctx.Err())
	trace.cond.L.Unlock()
}
