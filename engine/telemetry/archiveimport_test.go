package telemetry

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	collogpb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logpb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

type archiveBarrierFunc func(context.Context) error

func (f archiveBarrierFunc) WaitForEventLoop(ctx context.Context) error { return f(ctx) }

type importLogCounter struct {
	exports int
	fail    bool
}

func (c *importLogCounter) Export(context.Context, []sdklog.Record) error {
	if c.fail {
		return errors.New("enqueue failed")
	}
	c.exports++
	return nil
}
func (*importLogCounter) Shutdown(context.Context) error   { return nil }
func (*importLogCounter) ForceFlush(context.Context) error { return nil }

type importMetricCounter struct{ exports int }

func (c *importMetricCounter) Export(context.Context, *metricdata.ResourceMetrics) error {
	c.exports++
	return nil
}
func (*importMetricCounter) Shutdown(context.Context) error   { return nil }
func (*importMetricCounter) ForceFlush(context.Context) error { return nil }
func (*importMetricCounter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.DeltaTemporality
}
func (*importMetricCounter) Aggregation(sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.AggregationDefault{}
}

type importSpanCounter struct {
	exports int
	fail    bool
	last    []sdktrace.ReadOnlySpan
}

func (c *importSpanCounter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	if c.fail {
		return errors.New("span enqueue failed")
	}
	c.exports++
	c.last = spans
	return nil
}
func (*importSpanCounter) Shutdown(context.Context) error { return nil }

func archiveImportCut() ArchiveCut {
	return ArchiveCut{HighWater: ArchiveHighWater{Spans: 10, Logs: 10, Metrics: 10}, SealAt: time.Unix(20, 0)}
}
func archiveImportLogs() *collogpb.ExportLogsServiceRequest {
	return &collogpb.ExportLogsServiceRequest{ResourceLogs: []*logpb.ResourceLogs{{Resource: &resourcepb.Resource{}, ScopeLogs: []*logpb.ScopeLogs{{LogRecords: []*logpb.LogRecord{{Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "history"}}}}}}}}}
}

func TestArchiveImportWaitsForApplication(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var waited atomic.Bool
	imp, err := NewArchiveTraceImporter(TraceImportSinks{Logs: &importLogCounter{}, Barrier: archiveBarrierFunc(func(ctx context.Context) error {
		waited.Store(true)
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})}, archiveImportCut())
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		done <- imp.ImportAndWait(t.Context(), archiveImportCut(), ArchiveImportBatch{Logs: archiveImportLogs()})
	}()
	<-entered
	require.True(t, waited.Load())
	select {
	case err := <-done:
		t.Fatalf("returned before application acknowledgment: %v", err)
	default:
	}
	close(release)
	require.NoError(t, <-done)
}

func TestArchiveImportCursorRetriesOnlyBarrier(t *testing.T) {
	logs := &importLogCounter{}
	metrics := &importMetricCounter{}
	barrierErr := errors.New("application interrupted")
	imp, err := NewArchiveTraceImporter(TraceImportSinks{Logs: logs, Metrics: metrics, Barrier: archiveBarrierFunc(func(context.Context) error { return barrierErr })}, archiveImportCut())
	require.NoError(t, err)
	batch := ArchiveImportBatch{Cursor: 3, Logs: archiveImportLogs()}
	require.ErrorIs(t, imp.ImportAndWait(t.Context(), archiveImportCut(), batch), barrierErr)
	require.Equal(t, 1, logs.exports)
	barrierErr = nil
	require.NoError(t, imp.ImportAndWait(t.Context(), archiveImportCut(), batch))
	require.Equal(t, 1, logs.exports, "successful enqueue is not repeated after barrier failure")
	logs.fail = true
	batch.Cursor = 4
	require.ErrorContains(t, imp.ImportAndWait(t.Context(), archiveImportCut(), batch), "enqueue failed")
	logs.fail = false
	require.NoError(t, imp.ImportAndWait(t.Context(), archiveImportCut(), batch))
	require.Equal(t, 2, logs.exports)
	// A reconnect replay does not duplicate log messages.
	batch.Cursor = 3
	require.NoError(t, imp.ImportAndWait(t.Context(), archiveImportCut(), batch))
	require.Equal(t, 2, logs.exports)
	metricBatch := ArchiveImportBatch{Cursor: 3, Metrics: &colmetricpb.ExportMetricsServiceRequest{ResourceMetrics: []*metricpb.ResourceMetrics{{Resource: &resourcepb.Resource{}, ScopeMetrics: []*metricpb.ScopeMetrics{{Metrics: []*metricpb.Metric{{Name: "history", Data: &metricpb.Metric_Gauge{Gauge: &metricpb.Gauge{DataPoints: []*metricpb.NumberDataPoint{{Value: &metricpb.NumberDataPoint_AsInt{AsInt: 1}}}}}}}}}}}}}
	require.NoError(t, imp.ImportAndWait(t.Context(), archiveImportCut(), metricBatch))
	require.Equal(t, 1, metrics.exports, "signal cursors are independent")
	require.NoError(t, imp.ImportAndWait(t.Context(), archiveImportCut(), metricBatch))
	require.Equal(t, 1, metrics.exports)
	require.NoError(t, imp.ImportAndWait(t.Context(), archiveImportCut(), ArchiveImportBatch{Cursor: 8, Logs: &collogpb.ExportLogsServiceRequest{}}))
	require.EqualValues(t, 8, imp.enqueued[ArchiveLogs], "empty data frame advances its cursor")
	wrong := archiveImportCut()
	wrong.SealAt = wrong.SealAt.Add(time.Second)
	require.ErrorIs(t, imp.ImportAndWait(t.Context(), wrong, batch), ErrArchiveCutMismatch)
	require.Error(t, imp.ImportAndWait(t.Context(), archiveImportCut(), ArchiveImportBatch{Cursor: 11, Logs: archiveImportLogs()}))
}

func TestArchiveSealRetriesFailedExporter(t *testing.T) {
	spans := &importSpanCounter{}
	cut := archiveImportCut()
	imp, err := NewArchiveTraceImporter(TraceImportSinks{Spans: spans, Barrier: archiveBarrierFunc(func(context.Context) error { return nil })}, cut)
	require.NoError(t, err)
	req := &coltracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{Resource: &resourcepb.Resource{}, ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{TraceId: []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, SpanId: []byte{1, 0, 0, 0, 0, 0, 0, 0}, Name: "unfinished", StartTimeUnixNano: uint64(time.Unix(10, 0).UnixNano())}}}}}}}
	require.NoError(t, imp.ImportAndWait(t.Context(), cut, ArchiveImportBatch{Cursor: 4, Spans: req}))
	spans.fail = true
	require.Error(t, imp.CompleteRemainder(t.Context(), cut, ArchiveSpans, cut.HighWater.Spans))
	require.Len(t, imp.trace.unfinished, 1)
	spans.fail = false
	require.NoError(t, imp.CompleteRemainder(t.Context(), cut, ArchiveSpans, cut.HighWater.Spans))
	require.Empty(t, imp.trace.unfinished)
	require.Equal(t, cut.SealAt, spans.last[0].EndTime())
	require.ErrorIs(t, imp.ImportAndWait(t.Context(), cut, ArchiveImportBatch{Cursor: 5, Spans: req}), ErrArchiveSignalClosed)
}
