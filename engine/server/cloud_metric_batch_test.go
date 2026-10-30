package server

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	otlpmetricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"

	telemetry "github.com/dagger/otel-go"
)

type metricBatchRecorder struct {
	mu      sync.Mutex
	records []*otlpmetricsv1.ResourceMetrics
	onSend  func(context.Context, int, *metricdata.ResourceMetrics) error
}

func (r *metricBatchRecorder) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(kind)
}

func (r *metricBatchRecorder) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(kind)
}

func (r *metricBatchRecorder) Export(ctx context.Context, metrics *metricdata.ResourceMetrics) error {
	converted, err := telemetry.ResourceMetricsToPB(metrics)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.records = append(r.records, proto.Clone(converted).(*otlpmetricsv1.ResourceMetrics))
	index := len(r.records)
	r.mu.Unlock()
	if r.onSend != nil {
		return r.onSend(ctx, index, metrics)
	}
	return nil
}

func (*metricBatchRecorder) ForceFlush(context.Context) error { return nil }
func (*metricBatchRecorder) Shutdown(context.Context) error   { return nil }

func blockedMetricBatchRecorder(t *testing.T) (*metricBatchRecorder, chan struct{}, chan struct{}) {
	t.Helper()
	entered, release := make(chan struct{}), make(chan struct{})
	recorder := &metricBatchRecorder{onSend: func(ctx context.Context, index int, _ *metricdata.ResourceMetrics) error {
		if index == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}}
	return recorder, entered, release
}

func TestCloudMetricBatchPreservesEveryScopeAndPoint(t *testing.T) {
	recorder, entered, release := blockedMetricBatchRecorder(t)
	q := newCloudMetricQueue(recorder, cloudFlushBound{sessionID: "batch", timeout: time.Second})
	t.Cleanup(func() { require.NoError(t, q.Shutdown(t.Context())) })
	require.NoError(t, q.Export(t.Context(), gaugeMetrics(-1)))
	<-entered
	var expected []*otlpmetricsv1.ScopeMetrics
	const collections = 3 * cloudMetricBatchCollections
	for index := range collections {
		metrics := gaugeMetrics(int64(index))
		metrics.ScopeMetrics[0].Scope.SchemaURL = "https://example.com/scope"
		metrics.ScopeMetrics[0].Metrics[0].Data = metricdata.Gauge[int64]{DataPoints: []metricdata.DataPoint[int64]{{
			StartTime: time.Unix(1, int64(index)), Time: time.Unix(2, int64(index)), Value: int64(index),
			Attributes: attribute.NewSet(attribute.String("client", "native")),
		}}}
		metrics.ScopeMetrics[0].Metrics = append(metrics.ScopeMetrics[0].Metrics,
			metricdata.Metrics{Name: "calls", Unit: "1", Data: metricdata.Sum[int64]{
				Temporality: metricdata.CumulativeTemporality, IsMonotonic: true,
				DataPoints: []metricdata.DataPoint[int64]{{StartTime: time.Unix(1, 0), Time: time.Unix(2, 0), Value: int64(index)}},
			}},
			metricdata.Metrics{Name: "duration", Unit: "s", Data: metricdata.Histogram[float64]{
				Temporality: metricdata.CumulativeTemporality,
				DataPoints: []metricdata.HistogramDataPoint[float64]{{
					StartTime: time.Unix(1, 0), Time: time.Unix(2, 0), Count: 6, Sum: 7.5,
					Bounds: []float64{1, 2}, BucketCounts: []uint64{1, 2, 3},
					Min: metricdata.NewExtrema(0.25), Max: metricdata.NewExtrema(3.5),
				}},
			}},
		)
		pb, err := telemetry.ResourceMetricsToPB(metrics)
		require.NoError(t, err)
		expected = append(expected, pb.ScopeMetrics...)
		require.NoError(t, q.Export(t.Context(), metrics))
	}
	close(release)
	require.NoError(t, q.Shutdown(t.Context()))
	require.Len(t, recorder.records, 4, "one blocked upload plus three bounded batches")
	var actual []*otlpmetricsv1.ScopeMetrics
	for _, record := range recorder.records[1:] {
		actual = append(actual, record.ScopeMetrics...)
	}
	require.Len(t, actual, len(expected))
	for index := range expected {
		require.Equal(t, expected[index].String(), actual[index].String(), "collection %d changed during batching", index)
	}
	require.Zero(t, q.dropped)
}

func TestCloudMetricBatchSeparatesResourcesAndFlushBarriers(t *testing.T) {
	recorder, entered, release := blockedMetricBatchRecorder(t)
	q := newCloudMetricQueue(recorder, cloudFlushBound{sessionID: "batch", timeout: time.Second})
	t.Cleanup(func() { require.NoError(t, q.Shutdown(t.Context())) })
	require.NoError(t, q.Export(t.Context(), gaugeMetrics(-1)))
	<-entered
	require.NoError(t, q.Export(t.Context(), gaugeMetrics(1)))
	require.NoError(t, q.Export(t.Context(), gaugeMetrics(2)))
	flushed := make(chan struct{})
	go func() { q.flush(t.Context()); close(flushed) }()
	require.Eventually(t, func() bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		return slices.ContainsFunc(q.queue, func(entry cloudMetricQueueEntry) bool { return entry.barrier != nil })
	}, time.Second, time.Millisecond)
	// This collection has the same resource but must stay after the barrier.
	require.NoError(t, q.Export(t.Context(), gaugeMetrics(3)))
	other := gaugeMetrics(4)
	other.Resource = resource.NewWithAttributes("https://example.com/resource", attribute.String("service.name", "other"))
	require.NoError(t, q.Export(t.Context(), other))
	// The resource attributes match the preceding collection; its schema does not.
	otherSchema := gaugeMetrics(5)
	otherSchema.Resource = resource.NewWithAttributes("https://example.com/different", attribute.String("service.name", "other"))
	require.NoError(t, q.Export(t.Context(), otherSchema))
	close(release)
	select {
	case <-flushed:
	case <-time.After(time.Second):
		t.Fatal("flush barrier never completed")
	}
	require.NoError(t, q.Shutdown(t.Context()))
	require.Len(t, recorder.records, 5)
	require.Len(t, recorder.records[1].ScopeMetrics, 2, "compatible pre-barrier collections were not batched")
	require.Len(t, recorder.records[2].ScopeMetrics, 1, "batch crossed the flush barrier")
	require.NotEqual(t, recorder.records[3].Resource, recorder.records[2].Resource)
	require.NotEqual(t, recorder.records[3].SchemaUrl, recorder.records[4].SchemaUrl)
	require.Zero(t, q.dropped)
}

func TestCloudMetricBatchCountsFailedCollections(t *testing.T) {
	recorder, entered, release := blockedMetricBatchRecorder(t)
	block := recorder.onSend
	recorder.onSend = func(ctx context.Context, index int, metrics *metricdata.ResourceMetrics) error {
		if err := block(ctx, index, metrics); err != nil {
			return err
		}
		if index > 1 {
			return errors.New("metrics unavailable")
		}
		return nil
	}
	q := newCloudMetricQueue(recorder, cloudFlushBound{sessionID: "batch", timeout: time.Second})
	t.Cleanup(func() { require.NoError(t, q.Shutdown(t.Context())) })
	require.NoError(t, q.Export(t.Context(), gaugeMetrics(-1)))
	<-entered
	for index := range 7 {
		require.NoError(t, q.Export(t.Context(), gaugeMetrics(int64(index))))
	}
	close(release)
	require.NoError(t, q.Shutdown(t.Context()))
	require.Len(t, recorder.records, 2)
	require.Equal(t, 7, q.dropped, "loss accounting must count collections, not uploads")
}

func TestCloudMetricBatchBoundsUploadBytes(t *testing.T) {
	recorder, entered, release := blockedMetricBatchRecorder(t)
	q := newCloudMetricQueue(recorder, cloudFlushBound{sessionID: "batch", timeout: time.Second})
	t.Cleanup(func() { require.NoError(t, q.Shutdown(t.Context())) })
	require.NoError(t, q.Export(t.Context(), gaugeMetrics(-1)))
	<-entered
	for index := range 2 {
		metrics := gaugeMetrics(int64(index))
		gauge := metrics.ScopeMetrics[0].Metrics[0].Data.(metricdata.Gauge[int64])
		gauge.DataPoints[0].Attributes = attribute.NewSet(attribute.String("payload", strings.Repeat("x", 600<<10)))
		metrics.ScopeMetrics[0].Metrics[0].Data = gauge
		require.NoError(t, q.Export(t.Context(), metrics))
	}
	close(release)
	require.NoError(t, q.Shutdown(t.Context()))
	require.Len(t, recorder.records, 3, "two large collections should not share an upload")
	for _, record := range recorder.records {
		require.LessOrEqual(t, proto.Size(record), cloudMetricBatchBytes)
	}
	require.Zero(t, q.dropped)
}

// An oversized original collection remains intact; the batching cap must
// never split, truncate or silently discard its points.
func TestCloudMetricBatchPreservesOversizedCollection(t *testing.T) {
	recorder, entered, release := blockedMetricBatchRecorder(t)
	q := newCloudMetricQueue(recorder, cloudFlushBound{sessionID: "batch", timeout: time.Second})
	t.Cleanup(func() { require.NoError(t, q.Shutdown(t.Context())) })
	require.NoError(t, q.Export(t.Context(), gaugeMetrics(-1)))
	<-entered
	payload := strings.Repeat("x", 2*cloudMetricBatchBytes)
	large := gaugeMetrics(1)
	gauge := large.ScopeMetrics[0].Metrics[0].Data.(metricdata.Gauge[int64])
	gauge.DataPoints[0].Attributes = attribute.NewSet(attribute.String("payload", payload))
	large.ScopeMetrics[0].Metrics[0].Data = gauge
	require.NoError(t, q.Export(t.Context(), large))
	require.NoError(t, q.Export(t.Context(), gaugeMetrics(2)))
	close(release)
	require.NoError(t, q.Shutdown(t.Context()))
	require.Len(t, recorder.records, 3)
	require.Greater(t, proto.Size(recorder.records[1]), cloudMetricBatchBytes)
	actual := recorder.records[1].ScopeMetrics[0].Metrics[0].GetGauge().DataPoints[0].Attributes[0].Value.GetStringValue()
	require.Equal(t, len(payload), len(actual))
	require.True(t, payload == actual)
	require.Zero(t, q.dropped)
}
