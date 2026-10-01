package resolver

import (
	"context"
	"io"
	"strings"
	"testing"

	enginetelemetry "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	daggerotel "github.com/dagger/otel-go"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
)

type testFetcher struct {
	fetch func(context.Context, ocispecs.Descriptor) (io.ReadCloser, error)
}

func (f testFetcher) Fetch(
	ctx context.Context,
	desc ocispecs.Descriptor,
) (io.ReadCloser, error) {
	return f.fetch(ctx, desc)
}

func TestRegistryPullAttributedNetworkBytes(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(t.Context())) })
	ctx := daggerotel.WithMeterProvider(context.Background(), provider)
	ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2},
	}))
	recorder, err := enginetelemetry.NewNetworkAccumulator(ctx, enginetelemetry.NetworkRX)
	require.NoError(t, err)

	fetcher := networkFetcher{
		Fetcher: testFetcher{fetch: func(context.Context, ocispecs.Descriptor) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("compressed blob")), nil
		}},
		network: recorder,
	}
	body, err := fetcher.Fetch(ctx, ocispecs.Descriptor{})
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, body)
	require.NoError(t, err)
	require.NoError(t, body.Close())

	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &data))
	require.Equal(t, telemetryattrs.NetworkEstimatedRxBytes, data.ScopeMetrics[0].Metrics[0].Name)
	gauge := data.ScopeMetrics[0].Metrics[0].Data.(metricdata.Gauge[int64])
	require.EqualValues(t, len("compressed blob"), gauge.DataPoints[0].Value)
}
