package networkmetrics

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/ebpf/nettracer"
	"github.com/dagger/dagger/engine/telemetryattrs"
	daggerotel "github.com/dagger/otel-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
)

func TestCommandNetworkUnavailable(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(t.Context())) })
	span := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
	ctx := trace.ContextWithSpanContext(daggerotel.WithMeterProvider(t.Context(), provider), span)
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	finish := PrepareCommandNetwork(ctx, cmd)
	require.Nil(t, cmd.SysProcAttr, "unavailable metrics must not change process placement")
	require.NoError(t, cmd.Run())
	finish()
	finish() // Cleanup is safe for both error and success paths.
	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &data))
	require.Len(t, data.ScopeMetrics, 1)
	require.Len(t, data.ScopeMetrics[0].Metrics, 1)
	current := data.ScopeMetrics[0].Metrics[0]
	require.Equal(t, telemetryattrs.NetworkAvailable, current.Name)
	points := current.Data.(metricdata.Gauge[int64]).DataPoints
	require.Len(t, points, 1)
	require.Zero(t, points[0].Value)
	attr, ok := points[0].Attributes.Value(daggerotel.MetricsSpanIDAttr)
	require.True(t, ok)
	require.Equal(t, span.SpanID().String(), attr.AsString())
}

type testCommandCounters struct {
	reads  atomic.Int32
	closed chan struct{}
	wait   func(context.Context) error
}

func (c *testCommandCounters) WaitEmpty(ctx context.Context) error {
	if c.wait != nil {
		return c.wait(ctx)
	}
	return nil
}

func (c *testCommandCounters) Sample() (nettracer.Sample, error) {
	if c.reads.Add(1) == 1 {
		return nettracer.Sample{}, nil
	}
	return nettracer.Sample{InternalRX: 11, InternalTX: 13, ExternalRX: 17, ExternalTX: 19}, nil
}

func (c *testCommandCounters) Close() error { close(c.closed); return nil }

func waitCommandClosed(t *testing.T, c *testCommandCounters) {
	t.Helper()
	select {
	case <-c.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("accounting cleanup did not finish")
	}
}

func TestCommandNetworkFinalSample(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(t.Context())) })
	span := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
	ctx := trace.ContextWithSpanContext(daggerotel.WithMeterProvider(t.Context(), provider), span)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	counters := &testCommandCounters{closed: make(chan struct{})}
	helperExit := make(chan struct{})
	releaseHelper := sync.OnceFunc(func() { close(helperExit) })
	t.Cleanup(func() {
		releaseHelper()
		waitCommandClosed(t, counters)
	})
	counters.wait = func(context.Context) error {
		<-helperExit
		return nil
	}
	finish := prepareCommandNetwork(ctx, &exec.Cmd{}, func(*exec.Cmd) (commandNetworkCounters, error) {
		return counters, nil
	})
	cancel() // Failed/cancelled operations must still publish actual traffic.
	returned := make(chan struct{})
	go func() {
		finish()
		finish()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("finish blocked on the live helper")
	}
	select {
	case <-counters.closed:
		t.Fatal("counters closed before helper exit")
	default:
	}
	require.GreaterOrEqual(t, counters.reads.Load(), int32(2))
	releaseHelper()
	waitCommandClosed(t, counters)
	require.GreaterOrEqual(t, counters.reads.Load(), int32(3))
	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &data))
	got := map[string]int64{}
	for _, scope := range data.ScopeMetrics {
		for _, current := range scope.Metrics {
			points := current.Data.(metricdata.Gauge[int64]).DataPoints
			require.Len(t, points, 1)
			got[current.Name] = points[0].Value
			attr, ok := points[0].Attributes.Value(daggerotel.MetricsSpanIDAttr)
			require.True(t, ok)
			require.Equal(t, span.SpanID().String(), attr.AsString())
		}
	}
	require.Equal(t, map[string]int64{
		telemetryattrs.NetworkAvailable: 1,
		telemetryattrs.NetworkRxBytes:   28, telemetryattrs.NetworkTxBytes: 32,
		telemetryattrs.NetworkInternalRxBytes: 11, telemetryattrs.NetworkInternalTxBytes: 13,
		telemetryattrs.NetworkExternalRxBytes: 17, telemetryattrs.NetworkExternalTxBytes: 19,
	}, got)
}

func TestCommandNetworkTimeoutPreservesSamples(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(t.Context())) })
	span := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
	ctx := trace.ContextWithSpanContext(daggerotel.WithMeterProvider(t.Context(), provider), span)
	counters := &testCommandCounters{closed: make(chan struct{}), wait: func(ctx context.Context) error {
		_, bounded := ctx.Deadline()
		assert.True(t, bounded)
		return context.DeadlineExceeded
	}}
	finish := prepareCommandNetwork(ctx, &exec.Cmd{}, func(*exec.Cmd) (commandNetworkCounters, error) {
		return counters, nil
	})
	finish()
	waitCommandClosed(t, counters)
	require.GreaterOrEqual(t, counters.reads.Load(), int32(3))
	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &data))
	got := map[string]int64{}
	for _, scope := range data.ScopeMetrics {
		for _, current := range scope.Metrics {
			points := current.Data.(metricdata.Gauge[int64]).DataPoints
			require.Len(t, points, 1)
			got[current.Name] = points[0].Value
		}
	}
	require.Equal(t, map[string]int64{
		telemetryattrs.NetworkAvailable: 1,
		telemetryattrs.NetworkRxBytes:   28, telemetryattrs.NetworkTxBytes: 32,
		telemetryattrs.NetworkInternalRxBytes: 11, telemetryattrs.NetworkInternalTxBytes: 13,
		telemetryattrs.NetworkExternalRxBytes: 17, telemetryattrs.NetworkExternalTxBytes: 19,
	}, got)
}
