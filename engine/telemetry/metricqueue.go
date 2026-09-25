package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type metricCollection struct {
	data   *metricdata.ResourceMetrics
	points int
}

// The SDK's periodic reader otherwise calls the network exporter synchronously,
// including from client shutdown. This queue isolates that wait and freezes the
// collection before the reader can reuse it. The SDK exporter owns retries.
type asyncMetricExporter struct {
	next        sdkmetric.Exporter
	queue       chan metricCollection
	limit       int
	mu          sync.Mutex
	pending     int
	closed      bool
	dropped     uint64
	cancel      context.CancelFunc
	done        chan struct{}
	shutdown    sync.Once
	shutdownErr error
}

func newAsyncMetricExporter(next sdkmetric.Exporter, limit int) *asyncMetricExporter {
	ctx, cancel := context.WithCancel(context.Background())
	q := &asyncMetricExporter{next: next, queue: make(chan metricCollection, limit), limit: limit, cancel: cancel, done: make(chan struct{})}
	go q.run(ctx)
	return q
}

func (q *asyncMetricExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return q.next.Temporality(kind)
}
func (q *asyncMetricExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return q.next.Aggregation(kind)
}
func (*asyncMetricExporter) ForceFlush(context.Context) error { return nil }

func (q *asyncMetricExporter) Export(_ context.Context, data *metricdata.ResourceMetrics) error {
	if data == nil || len(data.ScopeMetrics) == 0 {
		return nil
	}
	// Only our observation producer writes here. Copy its point slices before
	// returning to the reader; no generic SDK metric conversion is needed.
	copied := &metricdata.ResourceMetrics{Resource: data.Resource, ScopeMetrics: slices.Clone(data.ScopeMetrics)}
	points := 0
	for i := range copied.ScopeMetrics {
		scope := &copied.ScopeMetrics[i]
		scope.Metrics = slices.Clone(scope.Metrics)
		for j := range scope.Metrics {
			m := &scope.Metrics[j]
			gauge, ok := m.Data.(metricdata.Gauge[int64])
			if !ok {
				return fmt.Errorf("unexpected operation observation type %T", m.Data)
			}
			gauge.DataPoints = slices.Clone(gauge.DataPoints)
			m.Data = gauge
			points += len(gauge.DataPoints)
		}
	}
	if points == 0 {
		return nil
	}
	q.mu.Lock()
	if q.closed || points > q.limit-q.pending {
		q.mu.Unlock()
		q.loss(points, "metric queue full or closed")
		return nil
	}
	q.pending += points
	q.queue <- metricCollection{copied, points} // Capacity is also bounded by point count.
	q.mu.Unlock()
	return nil
}

func (q *asyncMetricExporter) loss(points int, reason string) {
	q.mu.Lock()
	previous := q.dropped
	q.dropped += uint64(points)
	total := q.dropped
	q.mu.Unlock()
	if previous == 0 || previous/1024 != total/1024 {
		slog.Warn("OTLP metric points lost", "reason", reason, "points", points, "total", total)
	}
}

func (q *asyncMetricExporter) run(ctx context.Context) {
	defer close(q.done)
	for collection := range q.queue {
		q.mu.Lock()
		q.pending -= collection.points
		q.mu.Unlock()
		if ctx.Err() != nil {
			q.loss(collection.points, "shutdown deadline")
			continue
		}
		if err := q.next.Export(ctx, collection.data); err != nil {
			q.loss(collection.points, "export failed")
			slog.Warn("OTLP metric export failed", "error", err)
		}
	}
}

func (q *asyncMetricExporter) Shutdown(ctx context.Context) error {
	q.shutdown.Do(func() {
		q.mu.Lock()
		q.closed = true
		close(q.queue)
		q.mu.Unlock()
		select {
		case <-q.done:
		case <-ctx.Done():
			q.cancel()
			<-q.done
		}
		q.cancel()
		q.shutdownErr = errors.Join(ctx.Err(), q.next.Shutdown(ctx))
	})
	return q.shutdownErr
}
