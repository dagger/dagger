package networkmetrics

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dagger/dagger/engine/ebpf/nettracer"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	daggerotel "github.com/dagger/otel-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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
	networkErr error
	path       string
	reads      atomic.Int32
	closed     chan struct{}
	wait       func(context.Context) error
}

func (c *testCommandCounters) CgroupPath() string { return c.path }

func (c *testCommandCounters) WaitEmpty(ctx context.Context) error {
	if c.wait != nil {
		return c.wait(ctx)
	}
	return nil
}

func (c *testCommandCounters) Sample() (nettracer.Sample, error) {
	if c.networkErr != nil {
		return nettracer.Sample{}, c.networkErr
	}
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

func TestCommandPlacementWithoutSpan(t *testing.T) {
	counters := &testCommandCounters{closed: make(chan struct{})}
	prepared := false
	finish := prepareCommandNetwork(t.Context(), &exec.Cmd{}, func(*exec.Cmd) (commandNetworkCounters, error) {
		prepared = true
		return counters, nil
	})
	require.True(t, prepared, "CPU and memory isolation does not require a span")
	finish()
	finish()
	waitCommandClosed(t, counters)
	require.Zero(t, counters.reads.Load(), "do not emit unattributed span metrics")
}

func TestCommandResourcesWithoutNetwork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reader := metric.NewManualReader()
		provider := metric.NewMeterProvider(metric.WithReader(reader))
		t.Cleanup(func() { require.NoError(t, provider.Shutdown(t.Context())) })
		span := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
		ctx := trace.ContextWithSpanContext(daggerotel.WithMeterProvider(t.Context(), provider), span)
		readings := enginetel.NewWorkloadReadings(time.Second)
		ctx = enginetel.WithWorkloadReadings(ctx, readings)
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		path := t.TempDir()
		write := func(file, value string) {
			t.Helper()
			require.NoError(t, os.WriteFile(filepath.Join(path, file), []byte(value), 0o600))
		}
		write("cpu.stat", "usage_usec 30\nuser_usec 20\nsystem_usec 10\n")
		write("memory.current", "4096\n")
		write("memory.peak", "8192\n")
		counters := &testCommandCounters{
			path: path, closed: make(chan struct{}), networkErr: errors.New("eBPF unavailable"),
		}
		// A daemon may do more work after its launching command exits.
		exited := make(chan struct{})
		counters.wait = func(context.Context) error { <-exited; return nil }
		finish := prepareCommandNetwork(ctx, exec.Command("git"), func(*exec.Cmd) (commandNetworkCounters, error) {
			return counters, nil
		})
		release := sync.OnceFunc(func() { close(exited) })
		t.Cleanup(func() { release(); finish(); waitCommandClosed(t, counters) })
		collect := func() map[string]int64 {
			var data metricdata.ResourceMetrics
			require.NoError(t, reader.Collect(t.Context(), &data))
			workloads, err := readings.Produce(t.Context())
			require.NoError(t, err)
			require.Empty(t, workloads, "helper samples must not emit anonymous workload availability")
			got := map[string]int64{}
			for _, scope := range data.ScopeMetrics {
				for _, current := range scope.Metrics {
					points := current.Data.(metricdata.Gauge[int64]).DataPoints
					require.Len(t, points, 1)
					got[current.Name] = points[0].Value
					traceID, ok := points[0].Attributes.Value(daggerotel.MetricsTraceIDAttr)
					require.True(t, ok)
					require.Equal(t, span.TraceID().String(), traceID.AsString())
					spanID, ok := points[0].Attributes.Value(daggerotel.MetricsSpanIDAttr)
					require.True(t, ok)
					require.Equal(t, span.SpanID().String(), spanID.AsString())
					if current.Name == daggerotel.CPUStatUsage {
						require.Equal(t, "us", current.Unit)
					}
				}
			}
			return got
		}
		require.Equal(t, int64(4096), collect()[daggerotel.MemoryCurrentBytes])
		// Change the values after the initial sample. With virtual time,
		// the periodic ticker cannot publish these instead of finish.
		write("cpu.stat", "usage_usec 60\nuser_usec 40\nsystem_usec 20\n")
		write("memory.current", "6144\n")
		write("memory.peak", "12288\n")
		cancel()
		finish() // Must publish immediately without waiting for the daemon.
		require.Equal(t, map[string]int64{
			telemetryattrs.NetworkAvailable: 0,
			daggerotel.CPUStatUsage:         60, daggerotel.CPUStatUser: 40, daggerotel.CPUStatSystem: 20,
			daggerotel.MemoryCurrentBytes: 6144, daggerotel.MemoryPeakBytes: 12288,
		}, collect())
		write("cpu.stat", "usage_usec 90\nuser_usec 60\nsystem_usec 30\n")
		write("memory.current", "0\n")
		write("memory.peak", "16384\n")
		release()
		waitCommandClosed(t, counters)
		require.Equal(t, map[string]int64{
			telemetryattrs.NetworkAvailable: 0,
			daggerotel.CPUStatUsage:         90, daggerotel.CPUStatUser: 60, daggerotel.CPUStatSystem: 30,
			daggerotel.MemoryCurrentBytes: 0, daggerotel.MemoryPeakBytes: 16384,
		}, collect())
	})
}

func TestCommandResourcesHaveDistinctSpans(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() {
		require.NoError(t, provider.Shutdown(context.Background()))
		require.NoError(t, tp.Shutdown(context.Background()))
	})
	ctx, parent := tp.Tracer("test").Start(daggerotel.WithMeterProvider(t.Context(), provider), "changeset")
	defer parent.End()
	for _, usage := range []string{"30", "70"} {
		path := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(path, "cpu.stat"), []byte("usage_usec "+usage+"\n"), 0o600))
		counters := &testCommandCounters{path: path, closed: make(chan struct{})}
		finish := prepareCommandNetwork(ctx, exec.Command("git"), func(*exec.Cmd) (commandNetworkCounters, error) {
			return counters, nil
		})
		finish()
		waitCommandClosed(t, counters)
	}
	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &data))
	values := map[string]int64{}
	for _, scope := range data.ScopeMetrics {
		for _, current := range scope.Metrics {
			if current.Name != daggerotel.CPUStatUsage {
				continue
			}
			for _, point := range current.Data.(metricdata.Gauge[int64]).DataPoints {
				spanID, ok := point.Attributes.Value(daggerotel.MetricsSpanIDAttr)
				require.True(t, ok)
				require.NotEqual(t, parent.SpanContext().SpanID().String(), spanID.AsString())
				values[spanID.AsString()] = point.Value
			}
		}
	}
	require.Len(t, values, 2)
	var usages []int64
	for _, value := range values {
		usages = append(usages, value)
	}
	require.ElementsMatch(t, []int64{30, 70}, usages)
}

func TestCommandSpanEndsBeforeCleanup(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { require.NoError(t, tp.Shutdown(context.Background())) })
	ctx, parent := tp.Tracer("test").Start(t.Context(), "operation")
	defer parent.End()
	exited := make(chan struct{})
	release := sync.OnceFunc(func() { close(exited) })
	counters := &testCommandCounters{
		closed: make(chan struct{}),
		wait:   func(context.Context) error { <-exited; return nil },
	}
	finish := prepareCommandNetwork(ctx, exec.Command("git"), func(*exec.Cmd) (commandNetworkCounters, error) {
		return counters, nil
	})
	t.Cleanup(func() { release(); finish(); waitCommandClosed(t, counters) })
	finish()
	finish()
	ended := recorder.Ended()
	require.Len(t, ended, 1, "finish must end the helper span before returning")
	require.Equal(t, "git resources", ended[0].Name())
	require.Equal(t, parent.SpanContext().SpanID(), ended[0].Parent().SpanID())
	for _, attr := range ended[0].Attributes() {
		if attr.Key == daggerotel.UIInternalAttr {
			require.False(t, attr.Value.AsBool(), "resource rows must not require internal-span verbosity")
		}
	}
	select {
	case <-counters.closed:
		t.Fatal("cleanup must still wait for the surviving helper")
	default:
	}
}
