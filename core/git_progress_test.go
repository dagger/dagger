package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/engine/telemetryattrs"
	daggerotel "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
)

func TestParseGitByteSize(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		value string
		unit  string
		want  int64
		ok    bool
	}{
		{"712", "bytes", 712, true},
		{"712", "B", 712, true},
		{"1.50", "KiB", 1536, true},
		{"5.6", "MiB", 5872026, true},
		{"2", "GiB", 2 << 30, true},
		{"bad", "MiB", 0, false},
		{"1", "wat", 0, false},
	} {
		got, ok := parseGitByteSize(tc.value, tc.unit)
		require.Equal(t, tc.ok, ok)
		require.Equal(t, tc.want, got)
	}
}

func TestGitReceivingObjectsBytes(t *testing.T) {
	t.Parallel()
	match := gitReceivingObjectsRE.FindStringSubmatch("Receiving objects:  42% (1234/2900), 5.6 MiB | 2.3 MiB/s")
	require.Len(t, match, 5)
	require.Equal(t, "1234", match[1])
	require.Equal(t, "2900", match[2])
	require.Equal(t, "5.6", match[3])
	require.Equal(t, "MiB", match[4])
}

func TestGitReceivingObjectsAccumulatesCommands(t *testing.T) {
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
	require.Len(t, metrics.ScopeMetrics, 1)
	require.Len(t, metrics.ScopeMetrics[0].Metrics, 1)
	require.Equal(t, telemetryattrs.NetworkRxBytes, metrics.ScopeMetrics[0].Metrics[0].Name)
	gauge := metrics.ScopeMetrics[0].Metrics[0].Data.(metricdata.Gauge[int64])
	require.EqualValues(t, 3<<20, gauge.DataPoints[0].Value)
}
