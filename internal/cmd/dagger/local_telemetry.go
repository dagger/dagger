package daggercmd

import (
	"context"
	"errors"
	"sync"
	"time"

	enginetel "github.com/dagger/dagger/engine/telemetry"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	otlpmetricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// localTelemetryEngine is the engine session local telemetry is handed to:
// a *client.Client.
type localTelemetryEngine interface {
	IngestsTelemetry() bool
	PublishesTelemetryToCloud() bool
	ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error
	ExportLogs(context.Context, []sdklog.Record) error
	ExportMetrics(context.Context, *colmetricspb.ExportMetricsServiceRequest) error
}

// maxHeldLocalTelemetry bounds the records of each signal held for an
// engine. Beyond it, records go to Cloud directly, as if no engine took them.
const maxHeldLocalTelemetry = 50_000

// localTelemetryFlushTimeout bounds the final flush into an engine session.
const localTelemetryFlushTimeout = 10 * time.Second

// localTelemetry routes the telemetry this process emits itself -- its command
// span, the output written through SpanStdio, its connect spans, filesync
// metrics, what `dagger run` relays -- to the engine's telemetry store for the
// CLI's client, so the store holds all of the session's telemetry, and to
// Dagger Cloud only when the engine does not publish it there itself.
//
// It sits in the process's telemetry pipeline next to the frontend, which
// keeps receiving these records directly: the engine leaves them out of the
// client's own telemetry streams. Records emitted while no engine session is
// attached -- before the connection exists, between sessions -- are held for
// the next one. What no engine took by shutdown goes to Cloud directly, as
// everything did before engines stored client telemetry.
//
// Whether records go to Cloud directly is decided by each engine attached:
// not when it both stores them and publishes to Cloud. Before the first one,
// Cloud gets nothing from here.
type localTelemetry struct {
	// spans, logs and metrics are the sinks' indexes in the pipeline
	// telemetry.Init builds (see engineTelemetryConfigWithCloud).
	spans, logs, metrics int

	// spanSink, logSink and metricSink are the pipeline's entries.
	spanSink   sdktrace.SpanExporter
	logSink    sdklog.Exporter
	metricSink sdkmetric.Exporter

	// cloud holds the CLI's Cloud exporters; nil when Cloud is not
	// configured.
	cloud *localCloudTelemetry

	// flush flushes the pipeline's processors into the sinks. Set by
	// initEngineTelemetry once the pipeline exists.
	flush func(context.Context) error

	mu sync.Mutex
	// engine is the attached session, nil when none is.
	engine localTelemetryEngine
	// ingest is whether to hold records for an engine: false while the last
	// engine attached did not store them.
	ingest bool
	// decided and cloudDirect: whether records go to Cloud directly, as the
	// last engine attached decided.
	decided     bool
	cloudDirect bool
	// heldSpans, heldLogs and heldMetrics wait for an engine. They went to
	// Cloud already iff decided && cloudDirect when they arrived.
	heldSpans   []sdktrace.ReadOnlySpan
	heldLogs    []sdklog.Record
	heldMetrics []*otlpmetricsv1.ResourceMetrics
}

// localCloudTelemetry is the CLI's Dagger Cloud exporters, behind processors
// that queue what goes to them, so Cloud's latency never holds up the
// command. Engine telemetry the engine does not publish itself goes to Cloud
// through the same processors (see finalizeEngineParams).
type localCloudTelemetry struct {
	metrics sdkmetric.Exporter

	forwardedSpans sdktrace.SpanProcessor
	forwardedLogs  sdklog.Processor
}

func newLocalTelemetry(cloudSpans sdktrace.SpanExporter, cloudLogs sdklog.Exporter, cloudMetrics sdkmetric.Exporter) *localTelemetry {
	lt := &localTelemetry{ingest: true}
	if cloudSpans != nil && cloudLogs != nil && cloudMetrics != nil {
		lt.cloud = &localCloudTelemetry{
			metrics: cloudMetrics,
			// A large queue, so the CLI→Cloud hop does not silently drop
			// spans on a big burst (a cold engine build is ~15k spans).
			forwardedSpans: enginetel.NewLargeQueueLiveSpanProcessor(cloudSpans),
			forwardedLogs:  sdklog.NewBatchProcessor(cloudLogs, sdklog.WithExportInterval(telemetry.NearlyImmediate)),
		}
	}
	lt.spanSink = localSpanSink{lt}
	lt.logSink = localLogSink{lt}
	lt.metricSink = localMetricSink{lt}
	return lt
}

// attach hands local telemetry to an engine session, along with what was
// held for one, until the returned detach is called. detach flushes the
// pipeline into the session first; call it before closing the session.
func (lt *localTelemetry) attach(ctx context.Context, eng localTelemetryEngine) (detach func()) {
	if lt == nil {
		return func() {}
	}
	lt.mu.Lock()
	defer lt.mu.Unlock()
	ingests := eng.IngestsTelemetry()
	direct := !ingests || !eng.PublishesTelemetryToCloud()
	switch {
	case direct && !lt.toCloud():
		// Cloud has not seen the held records.
		lt.heldToCloud(ctx)
	case !direct && lt.toCloud():
		// Cloud has the held records already, and this engine would
		// publish them again.
		lt.dropHeld()
	}
	lt.decided, lt.cloudDirect = true, direct
	lt.ingest = ingests
	if !ingests {
		lt.dropHeld()
		return func() {}
	}
	lt.engine = eng
	// Under the lock, so that the held records reach the engine before any
	// that arrive now.
	if err := lt.heldToEngine(ctx); err != nil {
		otel.Handle(err)
	}
	lt.dropHeld()
	return func() {
		// Like the client's own shutdown, give up rather than hold the
		// command's exit on a stuck engine; Cloud still gets the rest.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), localTelemetryFlushTimeout)
		defer cancel()
		if lt.flush != nil {
			if err := lt.flush(ctx); err != nil {
				otel.Handle(err)
			}
		}
		lt.mu.Lock()
		defer lt.mu.Unlock()
		if lt.engine == eng {
			lt.engine = nil
		}
	}
}

// toCloud reports whether records arriving now go to Cloud directly.
func (lt *localTelemetry) toCloud() bool {
	return lt.cloud != nil && lt.decided && lt.cloudDirect
}

// fallback returns where records an engine did not take go: Cloud, unless
// it has them already. nil when they go nowhere else.
func (lt *localTelemetry) fallback() *localCloudTelemetry {
	if lt.toCloud() {
		return nil
	}
	return lt.cloud
}

// heldToCloud sends the held records to Cloud, which has not seen them.
func (lt *localTelemetry) heldToCloud(ctx context.Context) {
	if lt.cloud == nil {
		return
	}
	lt.cloud.exportSpans(ctx, lt.heldSpans)
	lt.cloud.exportLogs(ctx, lt.heldLogs)
	if err := lt.cloud.exportMetrics(ctx, lt.heldMetrics); err != nil {
		otel.Handle(err)
	}
}

func (lt *localTelemetry) heldToEngine(ctx context.Context) error {
	fallback := lt.fallback()
	var errs error
	if err := lt.engine.ExportSpans(ctx, lt.heldSpans); err != nil {
		errs = errors.Join(errs, err)
		fallback.exportSpans(ctx, lt.heldSpans)
	}
	if err := lt.engine.ExportLogs(ctx, lt.heldLogs); err != nil {
		errs = errors.Join(errs, err)
		fallback.exportLogs(ctx, lt.heldLogs)
	}
	if len(lt.heldMetrics) > 0 {
		req := &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: lt.heldMetrics}
		if err := lt.engine.ExportMetrics(ctx, req); err != nil {
			errs = errors.Join(errs, err, fallback.exportMetrics(ctx, lt.heldMetrics))
		}
	}
	return errs
}

func (lt *localTelemetry) dropHeld() {
	lt.heldSpans, lt.heldLogs, lt.heldMetrics = nil, nil, nil
}

// localRoute is where records arriving at one moment go, snapshotted under
// the lock so that the exports themselves run outside it.
type localRoute struct {
	// cloud, when set, gets them directly.
	cloud *localCloudTelemetry
	// engine, when set, gets them; fallback, when set, gets them if it
	// fails.
	engine   localTelemetryEngine
	fallback *localCloudTelemetry
	// hold is whether to hold them for an engine.
	hold bool
}

func (lt *localTelemetry) route() localRoute {
	r := localRoute{engine: lt.engine, fallback: lt.fallback()}
	if lt.toCloud() {
		r.cloud = lt.cloud
	}
	r.hold = r.engine == nil && lt.ingest
	return r
}

func (lt *localTelemetry) exportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	lt.mu.Lock()
	r := lt.route()
	var overflow []sdktrace.ReadOnlySpan
	if r.hold {
		n := min(max(0, maxHeldLocalTelemetry-len(lt.heldSpans)), len(spans))
		lt.heldSpans = append(lt.heldSpans, spans[:n]...)
		overflow = spans[n:]
	}
	lt.mu.Unlock()

	r.cloud.exportSpans(ctx, spans)
	r.fallback.exportSpans(ctx, overflow)
	if r.engine != nil {
		if err := r.engine.ExportSpans(ctx, spans); err != nil {
			r.fallback.exportSpans(ctx, spans)
			return err
		}
	}
	return nil
}

func (lt *localTelemetry) exportLogs(ctx context.Context, logs []sdklog.Record) error {
	lt.mu.Lock()
	r := lt.route()
	var overflow []sdklog.Record
	if r.hold {
		n := min(max(0, maxHeldLocalTelemetry-len(lt.heldLogs)), len(logs))
		for _, rec := range logs[:n] {
			// The batch processor reuses its records.
			lt.heldLogs = append(lt.heldLogs, rec.Clone())
		}
		overflow = logs[n:]
	}
	lt.mu.Unlock()

	r.cloud.exportLogs(ctx, logs)
	r.fallback.exportLogs(ctx, overflow)
	if r.engine != nil {
		if err := r.engine.ExportLogs(ctx, logs); err != nil {
			r.fallback.exportLogs(ctx, logs)
			return err
		}
	}
	return nil
}

func (lt *localTelemetry) exportMetrics(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	if rm == nil || len(rm.ScopeMetrics) == 0 {
		return nil
	}
	var pb *otlpmetricsv1.ResourceMetrics
	lt.mu.Lock()
	r := lt.route()
	if r.engine != nil || r.hold {
		// The reader reuses rm, so convert it now, which copies it.
		var err error
		if pb, err = telemetry.ResourceMetricsToPB(rm); err != nil {
			lt.mu.Unlock()
			return err
		}
	}
	overflow := false
	if r.hold {
		if len(lt.heldMetrics) < maxHeldLocalTelemetry {
			lt.heldMetrics = append(lt.heldMetrics, pb)
		} else {
			overflow = true
		}
	}
	lt.mu.Unlock()

	var errs error
	if r.cloud != nil {
		errs = r.cloud.metrics.Export(ctx, rm)
	}
	if overflow && r.fallback != nil {
		errs = errors.Join(errs, r.fallback.metrics.Export(ctx, rm))
	}
	if r.engine != nil {
		req := &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: []*otlpmetricsv1.ResourceMetrics{pb}}
		if err := r.engine.ExportMetrics(ctx, req); err != nil {
			errs = errors.Join(errs, err)
			if r.fallback != nil {
				errs = errors.Join(errs, r.fallback.metrics.Export(ctx, rm))
			}
		}
	}
	return errs
}

// shutdownSpans, shutdownLogs and shutdownMetrics send what no engine took
// to Cloud, unless it went there already, and then shut the signal's Cloud
// exporter down. The pipeline shuts the sinks down last thing, when no
// session is attached anymore.
func (lt *localTelemetry) shutdownSpans(ctx context.Context) error {
	lt.mu.Lock()
	lt.fallback().exportSpans(ctx, lt.heldSpans)
	lt.heldSpans = nil
	lt.mu.Unlock()
	if lt.cloud == nil {
		return nil
	}
	return lt.cloud.forwardedSpans.Shutdown(ctx)
}

func (lt *localTelemetry) shutdownLogs(ctx context.Context) error {
	lt.mu.Lock()
	lt.fallback().exportLogs(ctx, lt.heldLogs)
	lt.heldLogs = nil
	lt.mu.Unlock()
	if lt.cloud == nil {
		return nil
	}
	return lt.cloud.forwardedLogs.Shutdown(ctx)
}

func (lt *localTelemetry) shutdownMetrics(ctx context.Context) error {
	lt.mu.Lock()
	err := lt.fallback().exportMetrics(ctx, lt.heldMetrics)
	lt.heldMetrics = nil
	lt.mu.Unlock()
	if lt.cloud == nil {
		return err
	}
	return errors.Join(err, lt.cloud.metrics.Shutdown(ctx))
}

// exportSpans and exportLogs queue records for Cloud; a nil receiver drops
// them.
func (c *localCloudTelemetry) exportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) {
	if c == nil || len(spans) == 0 {
		return
	}
	_ = telemetry.SpanForwarder{Processors: []sdktrace.SpanProcessor{c.forwardedSpans}}.ExportSpans(ctx, spans)
}

func (c *localCloudTelemetry) exportLogs(ctx context.Context, logs []sdklog.Record) {
	if c == nil || len(logs) == 0 {
		return
	}
	_ = telemetry.LogForwarder{Processors: []sdklog.Processor{c.forwardedLogs}}.Export(ctx, logs)
}

// exportMetrics exports held metrics to Cloud; a nil receiver drops them.
func (c *localCloudTelemetry) exportMetrics(ctx context.Context, metrics []*otlpmetricsv1.ResourceMetrics) error {
	if c == nil || len(metrics) == 0 {
		return nil
	}
	return enginetel.ReexportMetricsFromPB(ctx, []sdkmetric.Exporter{c.metrics},
		&colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: metrics})
}

// localSpanSink, localLogSink and localMetricSink are localTelemetry's
// entries in the telemetry pipeline.
type localSpanSink struct{ lt *localTelemetry }

func (s localSpanSink) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	return s.lt.exportSpans(ctx, spans)
}

func (s localSpanSink) Shutdown(ctx context.Context) error { return s.lt.shutdownSpans(ctx) }

type localLogSink struct{ lt *localTelemetry }

func (s localLogSink) Export(ctx context.Context, logs []sdklog.Record) error {
	return s.lt.exportLogs(ctx, logs)
}

func (s localLogSink) Shutdown(ctx context.Context) error { return s.lt.shutdownLogs(ctx) }

func (s localLogSink) ForceFlush(ctx context.Context) error {
	if s.lt.cloud != nil {
		return s.lt.cloud.forwardedLogs.ForceFlush(ctx)
	}
	return nil
}

type localMetricSink struct{ lt *localTelemetry }

func (s localMetricSink) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	return s.lt.exportMetrics(ctx, rm)
}

func (s localMetricSink) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	if s.lt.cloud != nil {
		return s.lt.cloud.metrics.Temporality(kind)
	}
	return metricdata.DeltaTemporality
}

func (s localMetricSink) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	if s.lt.cloud != nil {
		return s.lt.cloud.metrics.Aggregation(kind)
	}
	return sdkmetric.AggregationDefault{}
}

func (s localMetricSink) ForceFlush(ctx context.Context) error {
	if s.lt.cloud != nil {
		return s.lt.cloud.metrics.ForceFlush(ctx)
	}
	return nil
}

func (s localMetricSink) Shutdown(ctx context.Context) error { return s.lt.shutdownMetrics(ctx) }
