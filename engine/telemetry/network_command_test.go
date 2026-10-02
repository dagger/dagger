package telemetry

import (
	"context"
	"os/exec"
	"testing"

	daggerotel "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
)

func TestCommandNetworkHook(t *testing.T) {
	t.Cleanup(func() { SetCommandNetworkHook(nil) })
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(t.Context())) })
	span := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
	ctx := trace.ContextWithSpanContext(daggerotel.WithMeterProvider(t.Context(), provider), span)
	cmd := &exec.Cmd{}

	// The CLI neither changes process placement nor emits kernel availability.
	PrepareCommandNetwork(ctx, cmd)()
	require.Nil(t, cmd.SysProcAttr)
	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &data))
	require.Empty(t, data.ScopeMetrics)

	prepared, finished := false, false
	SetCommandNetworkHook(func(gotCtx context.Context, gotCmd *exec.Cmd) func() {
		require.Equal(t, ctx, gotCtx)
		require.Same(t, cmd, gotCmd)
		prepared = true
		return func() { finished = true }
	})
	finish := PrepareCommandNetwork(ctx, cmd)
	require.True(t, prepared)
	require.False(t, finished)
	finish()
	require.True(t, finished)

	SetCommandNetworkHook(nil)
	prepared = false
	PrepareCommandNetwork(ctx, cmd)()
	require.False(t, prepared)
}
