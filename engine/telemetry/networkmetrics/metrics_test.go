package networkmetrics

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(t.Context())) })

	_, err := register(provider.Meter("test"), func() (sample, error) {
		return sample{internalRX: 1, internalTX: 2, externalRX: 3, externalTX: 4}, nil
	})
	require.NoError(t, err)

	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &data))
	require.Len(t, data.ScopeMetrics, 1)
	require.Len(t, data.ScopeMetrics[0].Metrics, 2)

	metrics := map[string]metricdata.Metrics{}
	for _, current := range data.ScopeMetrics[0].Metrics {
		metrics[current.Name] = current
	}
	available := metrics[AvailableName].Data.(metricdata.Gauge[int64])
	require.Equal(t, int64(1), available.DataPoints[0].Value)

	bytes := metrics[BytesName].Data.(metricdata.Sum[int64])
	require.True(t, bytes.IsMonotonic)
	require.Equal(t, metricdata.CumulativeTemporality, bytes.Temporality)
	got := map[string]int64{}
	for _, point := range bytes.DataPoints {
		attrs := point.Attributes.ToSlice()
		require.Len(t, attrs, 2)
		values := map[string]string{}
		for _, attr := range attrs {
			values[string(attr.Key)] = attr.Value.AsString()
		}
		got[values["network.io.direction"]+"/"+values["network.scope"]] = point.Value
	}
	require.Equal(t, map[string]int64{
		"receive/internal":  1,
		"transmit/internal": 2,
		"receive/external":  3,
		"transmit/external": 4,
	}, got)
}

func TestUnavailable(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(t.Context())) })

	_, err := register(provider.Meter("test"), func() (sample, error) {
		return sample{}, errors.New("unavailable")
	})
	require.NoError(t, err)

	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &data))
	require.Len(t, data.ScopeMetrics, 1)
	require.Len(t, data.ScopeMetrics[0].Metrics, 1)
	require.Equal(t, AvailableName, data.ScopeMetrics[0].Metrics[0].Name)
	available := data.ScopeMetrics[0].Metrics[0].Data.(metricdata.Gauge[int64])
	require.Equal(t, int64(0), available.DataPoints[0].Value)
}
