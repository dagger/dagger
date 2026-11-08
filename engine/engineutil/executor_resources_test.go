package engineutil

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
)

func TestResourceMetricsViewDropsOnlyExecExemplars(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
		sdkmetric.WithView(ResourceMetricsView()),
	)
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(t.Context())) })
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled,
	}))

	execGauge, err := provider.Meter(InstrumentationLibrary).Int64Gauge("exec")
	require.NoError(t, err)
	otherGauge, err := provider.Meter("other").Int64Gauge("other")
	require.NoError(t, err)
	execGauge.Record(ctx, 23)
	otherGauge.Record(ctx, 29)

	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &data))
	points := map[string]metricdata.DataPoint[int64]{}
	for _, scope := range data.ScopeMetrics {
		for _, current := range scope.Metrics {
			points[current.Name] = current.Data.(metricdata.Gauge[int64]).DataPoints[0]
		}
	}
	require.Equal(t, int64(23), points["exec"].Value)
	require.Empty(t, points["exec"].Exemplars)
	require.Equal(t, int64(29), points["other"].Value)
	require.Len(t, points["other"].Exemplars, 1)
}
