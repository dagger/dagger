package telemetry

import (
	"context"
	"sync"

	"github.com/dagger/dagger/engine/telemetryattrs"
	daggerotel "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const networkInstrumentation = "dagger.io/network"

type NetworkDirection uint8

const (
	NetworkRX NetworkDirection = iota
	NetworkTX
)

// NetworkRecorder records an operation's absolute transferred byte count.
// The owning operation is identified by the span in ctx.
type NetworkRecorder struct {
	ctx   context.Context
	gauge metric.Int64Gauge
	opts  []metric.RecordOption
}

func NewNetworkRecorder(ctx context.Context, direction NetworkDirection) (*NetworkRecorder, error) {
	name := telemetryattrs.NetworkRxBytes
	description := "Total number of bytes received by the operation"
	if direction == NetworkTX {
		name = telemetryattrs.NetworkTxBytes
		description = "Total number of bytes transmitted by the operation"
	}
	gauge, err := daggerotel.Meter(ctx, networkInstrumentation).Int64Gauge(
		name,
		metric.WithDescription(description),
		metric.WithUnit("bytes"),
	)
	if err != nil {
		return nil, err
	}

	spanCtx := trace.SpanContextFromContext(ctx)
	attrs := make([]attribute.KeyValue, 0, 2)
	if spanCtx.HasTraceID() {
		attrs = append(attrs, attribute.String(daggerotel.MetricsTraceIDAttr, spanCtx.TraceID().String()))
	}
	if spanCtx.HasSpanID() {
		attrs = append(attrs, attribute.String(daggerotel.MetricsSpanIDAttr, spanCtx.SpanID().String()))
	}
	return &NetworkRecorder{
		ctx:   ctx,
		gauge: gauge,
		opts:  []metric.RecordOption{metric.WithAttributes(attrs...)},
	}, nil
}

func (r *NetworkRecorder) Record(bytes int64) {
	if r == nil || bytes <= 0 {
		return
	}
	r.gauge.Record(r.ctx, bytes, r.opts...)
}

// NetworkAccumulator combines concurrent transfer streams belonging to one
// operation into a single absolute network metric.
type NetworkAccumulator struct {
	recorder *NetworkRecorder
	mu       sync.Mutex
	total    int64
}

type networkAccumulatorsKey struct{}

type networkAccumulators struct {
	rx *NetworkAccumulator
	tx *NetworkAccumulator
}

func NewNetworkAccumulator(ctx context.Context, direction NetworkDirection) (*NetworkAccumulator, error) {
	recorder, err := NewNetworkRecorder(ctx, direction)
	if err != nil {
		return nil, err
	}
	return &NetworkAccumulator{recorder: recorder}, nil
}

func (a *NetworkAccumulator) Add(bytes int64) {
	if a == nil || bytes <= 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.total += bytes
	a.recorder.Record(a.total)
}

// WithNetworkRecording attaches one RX/TX accumulator pair to ctx. Transports
// beneath the operation can contribute retries and concurrent streams without
// resetting the operation's gauges.
func WithNetworkRecording(ctx context.Context) (context.Context, error) {
	rx, err := NewNetworkAccumulator(ctx, NetworkRX)
	if err != nil {
		return nil, err
	}
	tx, err := NewNetworkAccumulator(ctx, NetworkTX)
	if err != nil {
		return nil, err
	}
	return context.WithValue(ctx, networkAccumulatorsKey{}, networkAccumulators{rx: rx, tx: tx}), nil
}

func RecordNetworkRX(ctx context.Context, bytes int64) {
	if accumulators, ok := ctx.Value(networkAccumulatorsKey{}).(networkAccumulators); ok {
		accumulators.rx.Add(bytes)
	}
}

func RecordNetworkTX(ctx context.Context, bytes int64) {
	if accumulators, ok := ctx.Value(networkAccumulatorsKey{}).(networkAccumulators); ok {
		accumulators.tx.Add(bytes)
	}
}
