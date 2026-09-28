package telemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type shutdownCountingMetricExporter struct {
	exports, shutdowns int
}

func (*shutdownCountingMetricExporter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}

func (*shutdownCountingMetricExporter) Aggregation(sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.AggregationDefault{}
}

func (*shutdownCountingMetricExporter) ForceFlush(context.Context) error { return nil }

func (e *shutdownCountingMetricExporter) Export(context.Context, *metricdata.ResourceMetrics) error {
	e.exports++
	return nil
}

func (e *shutdownCountingMetricExporter) Shutdown(context.Context) error {
	e.shutdowns++
	return nil
}

// A client's periodic reader shuts its exporter down with it; the shared
// exporter keeps exporting for the other clients until its owner shuts it
// down.
func TestSharedMetricExporterOutlivesReaders(t *testing.T) {
	t.Parallel()
	inner := &shutdownCountingMetricExporter{}
	for range 2 {
		reader := sdkmetric.NewPeriodicReader(SharedMetricExporter{Exporter: inner})
		provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
		gauge, err := provider.Meter("test").Int64Gauge("g")
		require.NoError(t, err)
		gauge.Record(t.Context(), 1)
		require.NoError(t, provider.Shutdown(t.Context()))
	}
	require.Equal(t, 2, inner.exports, "each reader's shutdown exports its last collection")
	require.Zero(t, inner.shutdowns)
}
