package core

import (
	"context"
	"testing"

	enginetelemetry "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	daggerotel "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/stats"
)

func TestHostTunnelAttributedNetworkBytes(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(t.Context())) })
	ctx := daggerotel.WithMeterProvider(context.Background(), provider)
	ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2},
	}))
	ctx, err := withTunnelNetworkRecording(ctx)
	require.NoError(t, err)

	handler := enginetelemetry.NetworkStatsHandler(nil)
	handler.HandleRPC(ctx, &stats.InPayload{WireLength: 23})
	handler.HandleRPC(ctx, &stats.OutPayload{WireLength: 29})

	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &data))
	got := map[string]int64{}
	for _, current := range data.ScopeMetrics[0].Metrics {
		gauge := current.Data.(metricdata.Gauge[int64])
		got[current.Name] = gauge.DataPoints[0].Value
	}
	require.Equal(t, map[string]int64{
		telemetryattrs.NetworkEstimatedRxBytes: 23,
		telemetryattrs.NetworkEstimatedTxBytes: 29,
	}, got)
}
