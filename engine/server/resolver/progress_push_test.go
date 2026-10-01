package resolver

import (
	"context"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	enginetelemetry "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	daggerotel "github.com/dagger/otel-go"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
)

type testContentWriter struct {
	written int64
}

func (w *testContentWriter) Write(p []byte) (int, error) {
	w.written += int64(len(p))
	return len(p), nil
}

func (*testContentWriter) Close() error          { return nil }
func (*testContentWriter) Digest() digest.Digest { return "" }
func (*testContentWriter) Truncate(int64) error  { return nil }
func (w *testContentWriter) Status() (content.Status, error) {
	return content.Status{Offset: w.written}, nil
}
func (*testContentWriter) Commit(context.Context, int64, digest.Digest, ...content.Opt) error {
	return nil
}

func TestRegistryPushAttributedNetworkBytes(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(t.Context())) })
	ctx := daggerotel.WithMeterProvider(context.Background(), provider)
	ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2},
	}))
	recorder, err := enginetelemetry.NewNetworkAccumulator(ctx, enginetelemetry.NetworkTX)
	require.NoError(t, err)

	w := &attributedWriter{Writer: &testContentWriter{}, network: recorder}
	_, err = w.Write([]byte("compressed blob"))
	require.NoError(t, err)
	require.NoError(t, w.Commit(ctx, 0, ""))

	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &data))
	require.Equal(t, telemetryattrs.NetworkTxBytes, data.ScopeMetrics[0].Metrics[0].Name)
	gauge := data.ScopeMetrics[0].Metrics[0].Data.(metricdata.Gauge[int64])
	require.EqualValues(t, len("compressed blob"), gauge.DataPoints[0].Value)
}
