package telemetry

import (
	"context"
	"sync"

	"github.com/dagger/dagger/engine/telemetryattrs"
	daggerotel "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/stats"
)

const networkInstrumentation = "dagger.io/network"

type NetworkDirection uint8

const (
	NetworkRX NetworkDirection = iota
	NetworkTX
)

// NetworkRecorder records an operation's absolute attributed byte count.
// The owning operation is identified by the span in ctx. Callers record bytes
// at a protocol boundary, so the result is a lower bound on network-layer
// traffic rather than an exact packet count.
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
	spanID trace.SpanID
	rx     *NetworkAccumulator
	tx     *NetworkAccumulator
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
	spanID := trace.SpanContextFromContext(ctx).SpanID()
	if accumulators, ok := ctx.Value(networkAccumulatorsKey{}).(networkAccumulators); ok && accumulators.spanID == spanID {
		return ctx, nil
	}
	rx, err := NewNetworkAccumulator(ctx, NetworkRX)
	if err != nil {
		return nil, err
	}
	tx, err := NewNetworkAccumulator(ctx, NetworkTX)
	if err != nil {
		return nil, err
	}
	return context.WithValue(ctx, networkAccumulatorsKey{}, networkAccumulators{spanID: spanID, rx: rx, tx: tx}), nil
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

// NetworkStatsHandler records compressed gRPC message bytes for operations
// that opted in with WithNetworkRecording. HTTP/2 framing and connection-level
// traffic remain in the engine-wide eBPF total.
func NetworkStatsHandler(inner stats.Handler) stats.Handler {
	return &networkStatsHandler{inner: inner}
}

type networkStatsHandler struct {
	inner stats.Handler
}

func (h *networkStatsHandler) TagRPC(
	ctx context.Context,
	info *stats.RPCTagInfo,
) context.Context {
	if h.inner != nil {
		ctx = h.inner.TagRPC(ctx, info)
	}
	return ctx
}

func (h *networkStatsHandler) HandleRPC(ctx context.Context, event stats.RPCStats) {
	if h.inner != nil {
		h.inner.HandleRPC(ctx, event)
	}
	switch event := event.(type) {
	case *stats.InPayload:
		RecordNetworkRX(ctx, int64(event.WireLength))
	case *stats.OutPayload:
		RecordNetworkTX(ctx, int64(event.WireLength))
	}
}

func (h *networkStatsHandler) TagConn(
	ctx context.Context,
	info *stats.ConnTagInfo,
) context.Context {
	if h.inner != nil {
		return h.inner.TagConn(ctx, info)
	}
	return ctx
}

func (h *networkStatsHandler) HandleConn(ctx context.Context, event stats.ConnStats) {
	if h.inner != nil {
		h.inner.HandleConn(ctx, event)
	}
}
