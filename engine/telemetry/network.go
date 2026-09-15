package telemetry

import (
	"context"

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
