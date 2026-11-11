package daggercmd

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
)

// keptSpans is an in-memory exporter whose shutdown keeps what it got.
type keptSpans struct{ *tracetest.InMemoryExporter }

func (keptSpans) Shutdown(context.Context) error { return nil }

// fakeEngine is an engine session as local telemetry sees it.
type fakeEngine struct {
	ingests, publishes bool
	fail               error

	mu      sync.Mutex
	spans   []string
	logs    int
	metrics int
}

func (e *fakeEngine) IngestsTelemetry() bool          { return e.ingests }
func (e *fakeEngine) PublishesTelemetryToCloud() bool { return e.publishes }

func (e *fakeEngine) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	if e.fail != nil {
		return e.fail
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, span := range spans {
		e.spans = append(e.spans, span.Name())
	}
	return nil
}

func (e *fakeEngine) ExportLogs(_ context.Context, logs []sdklog.Record) error {
	if e.fail != nil {
		return e.fail
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.logs += len(logs)
	return nil
}

func (e *fakeEngine) ExportMetrics(_ context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	if e.fail != nil {
		return e.fail
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.metrics += len(req.GetResourceMetrics())
	return nil
}

var testSpanIDs atomic.Uint64

func testSpan(name string) sdktrace.ReadOnlySpan {
	var spanID trace.SpanID
	binary.BigEndian.PutUint64(spanID[:], testSpanIDs.Add(1))
	return tracetest.SpanStub{
		Name: name,
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: trace.TraceID{1}, SpanID: spanID, TraceFlags: trace.FlagsSampled,
		}),
		StartTime: time.Now(),
		EndTime:   time.Now(),
	}.Snapshot()
}

func testResourceMetrics() *metricdata.ResourceMetrics {
	return &metricdata.ResourceMetrics{ScopeMetrics: []metricdata.ScopeMetrics{{
		Metrics: []metricdata.Metrics{{
			Name: "test.metric",
			Data: metricdata.Gauge[int64]{DataPoints: []metricdata.DataPoint[int64]{{Value: 1}}},
		}},
	}}}
}

type localTelemetryFixture struct {
	lt           *localTelemetry
	cloudSpans   *tracetest.InMemoryExporter
	cloudLogs    *countingLogExporter
	cloudMetrics *countingMetricExporter
}

func newLocalTelemetryFixture(withCloud bool) *localTelemetryFixture {
	f := &localTelemetryFixture{
		cloudSpans:   tracetest.NewInMemoryExporter(),
		cloudLogs:    new(countingLogExporter),
		cloudMetrics: new(countingMetricExporter),
	}
	if withCloud {
		f.lt = newLocalTelemetry(keptSpans{f.cloudSpans}, f.cloudLogs, f.cloudMetrics)
	} else {
		f.lt = newLocalTelemetry(nil, nil, nil)
	}
	return f
}

// emit sends one span, one log record and one metric through the sinks.
func (f *localTelemetryFixture) emit(t *testing.T, name string) {
	t.Helper()
	ctx := t.Context()
	require.NoError(t, f.lt.spanSink.ExportSpans(ctx, []sdktrace.ReadOnlySpan{testSpan(name)}))
	require.NoError(t, f.lt.logSink.Export(ctx, []sdklog.Record{{}}))
	require.NoError(t, f.lt.metricSink.Export(ctx, testResourceMetrics()))
}

func (f *localTelemetryFixture) shutdown(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	require.NoError(t, f.lt.spanSink.Shutdown(ctx))
	require.NoError(t, f.lt.logSink.Shutdown(ctx))
	require.NoError(t, f.lt.metricSink.Shutdown(ctx))
}

// cloudSpanNames flushes what is queued for Cloud and returns the names of
// the spans Cloud got.
func (f *localTelemetryFixture) cloudSpanNames(t *testing.T) []string {
	t.Helper()
	if f.lt.cloud != nil {
		// After shutdown there is nothing left to flush.
		_ = f.lt.cloud.forwardedSpans.ForceFlush(t.Context())
		_ = f.lt.cloud.forwardedLogs.ForceFlush(t.Context())
	}
	var names []string
	for _, span := range f.cloudSpans.GetSpans() {
		names = append(names, span.Name)
	}
	return names
}

// An engine that stores client telemetry and publishes to Cloud takes it
// all, what was held before it connected included; Cloud hears nothing from
// the CLI. What comes after the session is detached reaches Cloud directly.
func TestLocalTelemetryEngineTakesItAll(t *testing.T) {
	f := newLocalTelemetryFixture(true)
	eng := &fakeEngine{ingests: true, publishes: true}

	f.emit(t, "before connect")
	detach := f.lt.attach(t.Context(), eng)
	f.emit(t, "connected")
	detach()
	f.emit(t, "after session")

	require.Equal(t, []string{"before connect", "connected"}, eng.spans)
	require.Equal(t, 2, eng.logs)
	require.Equal(t, 2, eng.metrics)
	require.Empty(t, f.cloudSpanNames(t))
	require.Zero(t, f.cloudLogs.records.Load())
	require.Zero(t, f.cloudMetrics.exports.Load())

	f.shutdown(t)
	require.Equal(t, []string{"after session"}, f.cloudSpanNames(t))
	require.Equal(t, int64(1), f.cloudLogs.records.Load())
	require.Equal(t, int64(1), f.cloudMetrics.exports.Load())
}

// Records held between sessions go to the next one.
func TestLocalTelemetryHeldForNextSession(t *testing.T) {
	f := newLocalTelemetryFixture(true)
	first := &fakeEngine{ingests: true, publishes: true}
	second := &fakeEngine{ingests: true, publishes: true}

	f.lt.attach(t.Context(), first)()
	f.emit(t, "between")
	f.lt.attach(t.Context(), second)()
	f.shutdown(t)

	require.Empty(t, first.spans)
	require.Equal(t, []string{"between"}, second.spans)
	require.Empty(t, f.cloudSpanNames(t))
}

// An engine that stores client telemetry without publishing to Cloud gets it
// all, and so does Cloud, from the CLI.
func TestLocalTelemetryEngineDoesNotPublish(t *testing.T) {
	f := newLocalTelemetryFixture(true)
	eng := &fakeEngine{ingests: true}

	f.emit(t, "before connect")
	detach := f.lt.attach(t.Context(), eng)
	f.emit(t, "connected")
	detach()
	f.shutdown(t)

	require.Equal(t, []string{"before connect", "connected"}, eng.spans)
	require.Equal(t, []string{"before connect", "connected"}, f.cloudSpanNames(t))
	require.Equal(t, int64(2), f.cloudLogs.records.Load())
	require.Equal(t, int64(2), f.cloudMetrics.exports.Load())
}

// An older engine, or a nested client's, does not store client telemetry:
// the CLI publishes it to Cloud as it always did, and holds nothing.
func TestLocalTelemetryOlderEngine(t *testing.T) {
	f := newLocalTelemetryFixture(true)
	eng := &fakeEngine{publishes: true}

	f.emit(t, "before connect")
	detach := f.lt.attach(t.Context(), eng)
	f.emit(t, "connected")
	detach()
	f.emit(t, "after session")
	require.Empty(t, f.lt.heldSpans)
	f.shutdown(t)

	require.Empty(t, eng.spans)
	require.Equal(t, []string{"before connect", "connected", "after session"}, f.cloudSpanNames(t))
}

// Engines of one process's sessions may answer differently. Whatever they
// answer, Cloud gets each record once: from the CLI, or from the engine that
// publishes it.
func TestLocalTelemetryEnginesDisagree(t *testing.T) {
	f := newLocalTelemetryFixture(true)
	first := &fakeEngine{ingests: true}
	second := &fakeEngine{ingests: true, publishes: true}

	f.lt.attach(t.Context(), first)()
	f.emit(t, "between")
	detach := f.lt.attach(t.Context(), second)
	f.emit(t, "second session")
	detach()
	f.shutdown(t)

	require.Equal(t, []string{"between"}, f.cloudSpanNames(t))
	require.Equal(t, []string{"second session"}, second.spans,
		"the publishing engine does not get what Cloud has already")
}

// What an engine fails to take goes to Cloud instead.
func TestLocalTelemetryEngineFailureFallsBackToCloud(t *testing.T) {
	f := newLocalTelemetryFixture(true)
	eng := &fakeEngine{ingests: true, publishes: true}

	detach := f.lt.attach(t.Context(), eng)
	eng.fail = errors.New("session gone")
	require.ErrorContains(t, f.lt.spanSink.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{testSpan("lost")}), "session gone")
	detach()

	require.Equal(t, []string{"lost"}, f.cloudSpanNames(t))
}

// Without Cloud, local telemetry only goes to the engine; what none took is
// dropped at shutdown.
func TestLocalTelemetryWithoutCloud(t *testing.T) {
	f := newLocalTelemetryFixture(false)
	eng := &fakeEngine{ingests: true}

	f.emit(t, "before connect")
	detach := f.lt.attach(t.Context(), eng)
	detach()
	f.emit(t, "after session")
	f.shutdown(t)

	require.Equal(t, []string{"before connect"}, eng.spans)
	require.Empty(t, f.cloudSpanNames(t))
}

// The held backlog is bounded; beyond it records go to Cloud directly.
func TestLocalTelemetryHeldIsBounded(t *testing.T) {
	f := newLocalTelemetryFixture(true)
	spans := make([]sdktrace.ReadOnlySpan, maxHeldLocalTelemetry+1)
	for i := range spans {
		spans[i] = testSpan("held")
	}
	spans[len(spans)-1] = testSpan("overflow")
	require.NoError(t, f.lt.spanSink.ExportSpans(t.Context(), spans))
	require.Len(t, f.lt.heldSpans, maxHeldLocalTelemetry)
	require.Equal(t, []string{"overflow"}, f.cloudSpanNames(t))
}
