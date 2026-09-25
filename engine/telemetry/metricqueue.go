package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

type metricCollection struct {
	data   *metricspb.ResourceMetrics
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
	pb, err := ResourceMetricsToPB(data)
	if err != nil {
		return err
	}
	// Histogram slices in the otel-go transform refer to the reader's buffers.
	pb = proto.Clone(pb).(*metricspb.ResourceMetrics)
	points := 0
	for _, scope := range pb.ScopeMetrics {
		for _, m := range scope.Metrics {
			points += len(m.GetGauge().GetDataPoints()) + len(m.GetSum().GetDataPoints()) +
				len(m.GetHistogram().GetDataPoints()) + len(m.GetExponentialHistogram().GetDataPoints()) + len(m.GetSummary().GetDataPoints())
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
	q.queue <- metricCollection{pb, points} // Capacity is also bounded by point count.
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
		data, err := ResourceMetricsFromPB(collection.data)
		if err == nil {
			err = q.next.Export(ctx, data)
		}
		if err != nil {
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
