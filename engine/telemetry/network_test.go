package telemetry

import (
	"context"
	"testing"

	"github.com/dagger/dagger/engine/telemetryattrs"
	daggerotel "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/stats"
)

func TestNetworkRecorder(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	ctx := daggerotel.WithMeterProvider(context.Background(), provider)
	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1},
		SpanID:  trace.SpanID{2},
	})
	ctx = trace.ContextWithSpanContext(ctx, spanCtx)

	recorder, err := NewNetworkRecorder(ctx, NetworkRX)
	require.NoError(t, err)
	recorder.Record(123)

	var metrics metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &metrics))
	require.Len(t, metrics.ScopeMetrics, 1)
	require.Len(t, metrics.ScopeMetrics[0].Metrics, 1)
	require.Equal(t, telemetryattrs.NetworkEstimatedRxBytes, metrics.ScopeMetrics[0].Metrics[0].Name)
	gauge := metrics.ScopeMetrics[0].Metrics[0].Data.(metricdata.Gauge[int64])
	require.EqualValues(t, 123, gauge.DataPoints[0].Value)
	spanID, ok := gauge.DataPoints[0].Attributes.Value(daggerotel.MetricsSpanIDAttr)
	require.True(t, ok)
	require.Equal(t, spanCtx.SpanID().String(), spanID.AsString())
}

func TestNetworkStatsHandler(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(t.Context())) })
	ctx := daggerotel.WithMeterProvider(context.Background(), provider)
	ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2},
	}))
	ctx, err := WithNetworkRecording(ctx)
	require.NoError(t, err)

	handler := NetworkStatsHandler(nil)
	handler.HandleRPC(ctx, &stats.InPayload{WireLength: 11})
	handler.HandleRPC(ctx, &stats.OutPayload{WireLength: 13})

	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &data))
	got := map[string]int64{}
	for _, current := range data.ScopeMetrics[0].Metrics {
		gauge := current.Data.(metricdata.Gauge[int64])
		got[current.Name] = gauge.DataPoints[0].Value
	}
	require.Equal(t, map[string]int64{
		telemetryattrs.NetworkEstimatedRxBytes: 11,
	}, got)
}
