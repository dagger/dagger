package core

import (
	"context"
	"testing"

	daggerotel "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
)

func TestGitProgressDoesNotEstimateNetworkBytes(t *testing.T) {
	match := gitReceivingObjectsRE.FindStringSubmatch("Receiving objects:  42% (1234/2900), 5.6 MiB | 2.3 MiB/s")
	require.Len(t, match, 3)
	require.Nil(t, gitReceivingObjectsRE.FindStringSubmatch("remote: Receiving objects: 100% (1/1), 999 GiB"))

	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	ctx := daggerotel.WithMeterProvider(context.Background(), provider)
	ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1},
		SpanID:  trace.SpanID{2},
	}))

	streams := gitFetchProgressStreams(ctx)
	for _, progress := range []string{
		"Receiving objects: 100% (1/1), 1.0 MiB | 1.0 MiB/s\n",
		"Receiving objects: 100% (1/1), 2.0 MiB | 1.0 MiB/s\n",
	} {
		_, stderr, _ := streams(ctx)
		_, err := stderr.Write([]byte(progress))
		require.NoError(t, err)
		require.NoError(t, stderr.Close())
	}

	var metrics metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &metrics))
	require.Empty(t, metrics.ScopeMetrics)
}
