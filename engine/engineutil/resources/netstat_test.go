package resources

import (
	"testing"

	"github.com/dagger/dagger/engine/telemetryattrs"
	resourcestypes "github.com/dagger/dagger/internal/buildkit/executor/resources/types"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type networkSamples struct {
	samples []*resourcestypes.NetworkSample
}

func (s *networkSamples) Sample() (*resourcestypes.NetworkSample, error) {
	sample := s.samples[0]
	s.samples = s.samples[1:]
	return sample, nil
}

func TestAttributedNetworkMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(t.Context())) })

	sampler, err := newNetNSSampler(&networkSamples{samples: []*resourcestypes.NetworkSample{
		{
			RxBytes: 100, TxBytes: 200, ScopeSupported: true,
			InternalRxBytes: 10, InternalTxBytes: 20,
			ExternalRxBytes: 30, ExternalTxBytes: 40,
		},
		{
			RxBytes: 130, TxBytes: 240, ScopeSupported: true,
			InternalRxBytes: 11, InternalTxBytes: 22,
			ExternalRxBytes: 33, ExternalTxBytes: 44,
		},
	}}, provider.Meter("test"), attribute.NewSet())
	require.NoError(t, err)
	require.NoError(t, sampler.sample(t.Context()))

	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &data))
	got := map[string]int64{}
	for _, current := range data.ScopeMetrics[0].Metrics {
		points := current.Data.(metricdata.Gauge[int64]).DataPoints
		require.Len(t, points, 1)
		got[current.Name] = points[0].Value
	}
	require.Equal(t, map[string]int64{
		telemetryattrs.NetworkRxBytes:         4,
		telemetryattrs.NetworkTxBytes:         6,
		telemetryattrs.NetworkInternalRxBytes: 1,
		telemetryattrs.NetworkInternalTxBytes: 2,
		telemetryattrs.NetworkExternalRxBytes: 3,
		telemetryattrs.NetworkExternalTxBytes: 4,
		telemetryattrs.NetworkAvailable:       1,
	}, got)

	unavailableReader := sdkmetric.NewManualReader()
	unavailableProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(unavailableReader),
	)
	t.Cleanup(func() {
		require.NoError(t, unavailableProvider.Shutdown(t.Context()))
	})
	unavailable, err := newNetNSSampler(
		&networkSamples{samples: []*resourcestypes.NetworkSample{{}, {}}},
		unavailableProvider.Meter("test"),
		attribute.NewSet(),
	)
	require.NoError(t, err)
	require.NoError(t, unavailable.sample(t.Context()))
	data = metricdata.ResourceMetrics{}
	require.NoError(t, unavailableReader.Collect(t.Context(), &data))
	require.Len(t, data.ScopeMetrics[0].Metrics, 1)
	require.Equal(
		t,
		telemetryattrs.NetworkAvailable,
		data.ScopeMetrics[0].Metrics[0].Name,
	)
	available := data.ScopeMetrics[0].Metrics[0].Data.(metricdata.Gauge[int64])
	require.EqualValues(t, 0, available.DataPoints[0].Value)
}
