package daggercmd

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type countingLogExporter struct {
	exports atomic.Int64
}

func (e *countingLogExporter) Export(context.Context, []sdklog.Record) error {
	e.exports.Add(1)
	return nil
}

func (e *countingLogExporter) Shutdown(context.Context) error   { return nil }
func (e *countingLogExporter) ForceFlush(context.Context) error { return nil }

type countingMetricExporter struct {
	exports atomic.Int64
}

func (e *countingMetricExporter) Export(context.Context, *metricdata.ResourceMetrics) error {
	e.exports.Add(1)
	return nil
}

func (e *countingMetricExporter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.DeltaTemporality
}

func (e *countingMetricExporter) Aggregation(sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.AggregationDefault{}
}

func (e *countingMetricExporter) Shutdown(context.Context) error   { return nil }
func (e *countingMetricExporter) ForceFlush(context.Context) error { return nil }

func TestEngineTelemetryConfigDefaultsToFrontendAndCloud(t *testing.T) {
	oldFrontend := Frontend
	oldSkip := skipSharedTelemetryExporters
	t.Cleanup(func() {
		Frontend = oldFrontend
		skipSharedTelemetryExporters = oldSkip
	})
	skipSharedTelemetryExporters = false

	localSpans := tracetest.NewInMemoryExporter()
	localLogs := new(countingLogExporter)
	localMetrics := new(countingMetricExporter)
	frontend := &idtui.FrontendMock{
		SpanExporterFunc:   func() sdktrace.SpanExporter { return localSpans },
		LogExporterFunc:    func() sdklog.Exporter { return localLogs },
		MetricExporterFunc: func() sdkmetric.Exporter { return localMetrics },
	}
	Frontend = frontend

	cloudSpans := tracetest.NewInMemoryExporter()
	cloudLogs := new(countingLogExporter)
	cloudMetrics := new(countingMetricExporter)
	cfg := engineTelemetryConfigWithCloud(context.Background(), func(context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter, bool) {
		return cloudSpans, cloudLogs, cloudMetrics, true
	})

	require.True(t, cfg.Detect)
	require.Len(t, frontend.SpanExporterCalls(), 1)
	require.Len(t, frontend.LogExporterCalls(), 1)
	require.Len(t, frontend.MetricExporterCalls(), 1)
	require.Equal(t, []sdktrace.SpanExporter{localSpans}, cfg.LiveTraceExporters)
	require.Equal(t, []sdklog.Exporter{localLogs, cloudLogs}, cfg.LiveLogExporters)
	require.Equal(t, []sdkmetric.Exporter{localMetrics, cloudMetrics}, cfg.LiveMetricExporters)
	require.Len(t, cfg.SpanProcessors, 1, "Cloud spans use their independent large-queue processor")
	t.Cleanup(func() {
		require.NoError(t, cfg.SpanProcessors[0].Shutdown(context.Background()))
	})
}

func TestEngineTelemetryConfigWithoutFrontendStillExportsToCloud(t *testing.T) {
	oldFrontend := Frontend
	oldSkip := skipSharedTelemetryExporters
	t.Cleanup(func() {
		Frontend = oldFrontend
		skipSharedTelemetryExporters = oldSkip
	})
	skipSharedTelemetryExporters = false

	localSpans := tracetest.NewInMemoryExporter()
	localLogs := new(countingLogExporter)
	localMetrics := new(countingMetricExporter)
	frontend := &idtui.FrontendMock{
		SpanExporterFunc:   func() sdktrace.SpanExporter { return localSpans },
		LogExporterFunc:    func() sdklog.Exporter { return localLogs },
		MetricExporterFunc: func() sdkmetric.Exporter { return localMetrics },
	}
	Frontend = frontend

	cloudSpans := tracetest.NewInMemoryExporter()
	cloudLogs := new(countingLogExporter)
	cloudMetrics := new(countingMetricExporter)
	cfg := engineTelemetryConfigWithCloud(withoutFrontendTelemetry(context.Background()), func(context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter, bool) {
		return cloudSpans, cloudLogs, cloudMetrics, true
	})

	require.True(t, cfg.Detect)
	require.Empty(t, frontend.SpanExporterCalls())
	require.Empty(t, frontend.LogExporterCalls())
	require.Empty(t, frontend.MetricExporterCalls())
	require.Empty(t, cfg.LiveTraceExporters)
	require.Equal(t, []sdklog.Exporter{cloudLogs}, cfg.LiveLogExporters)
	require.Equal(t, []sdkmetric.Exporter{cloudMetrics}, cfg.LiveMetricExporters)
	require.Len(t, cfg.SpanProcessors, 1)

	snapshot := tracetest.SpanStub{
		Name: "relayed engine span",
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    trace.TraceID{1},
			SpanID:     trace.SpanID{1},
			TraceFlags: trace.FlagsSampled,
		}),
		StartTime: time.Now(),
		EndTime:   time.Now(),
	}.Snapshot()
	cfg.SpanProcessors[0].OnEnd(snapshot)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cfg.SpanProcessors[0].ForceFlush(ctx))
	t.Cleanup(func() {
		require.NoError(t, cfg.SpanProcessors[0].Shutdown(context.Background()))
	})

	require.NoError(t, cfg.LiveLogExporters[0].Export(ctx, nil))
	require.NoError(t, cfg.LiveMetricExporters[0].Export(ctx, new(metricdata.ResourceMetrics)))

	require.Len(t, cloudSpans.GetSpans(), 1)
	require.Equal(t, int64(1), cloudLogs.exports.Load())
	require.Equal(t, int64(1), cloudMetrics.exports.Load())
	require.Empty(t, localSpans.GetSpans())
	require.Zero(t, localLogs.exports.Load())
	require.Zero(t, localMetrics.exports.Load())
}

// TestEngineTelemetryConfigSkipsSharedExporters guards the fix for the noisy
// "HTTP exporter is shutdown" / "context canceled" telemetry warnings emitted by
// the second engine session that `dagger module init` opens. Internal plumbing
// sessions must not wire up (and later tear down) the process-wide OTLP exporter
// singletons, otherwise the real command that runs next in the same process
// re-exports into already-shut-down exporters. See engineTelemetryConfig.
func TestEngineTelemetryConfigSkipsSharedExporters(t *testing.T) {
	oldFrontend := Frontend
	oldSkip := skipSharedTelemetryExporters
	Frontend = &idtui.FrontendMock{
		SpanExporterFunc:   func() sdktrace.SpanExporter { return tracetest.NewInMemoryExporter() },
		LogExporterFunc:    func() sdklog.Exporter { return new(countingLogExporter) },
		MetricExporterFunc: func() sdkmetric.Exporter { return new(countingMetricExporter) },
	}
	t.Cleanup(func() {
		Frontend = oldFrontend
		skipSharedTelemetryExporters = oldSkip
	})

	ctx := context.Background()

	skipSharedTelemetryExporters = false
	if cfg := engineTelemetryConfig(ctx); !cfg.Detect {
		t.Fatal("expected Detect to be enabled for a normal session")
	}

	skipSharedTelemetryExporters = true
	if cfg := engineTelemetryConfig(ctx); cfg.Detect {
		t.Fatal("expected Detect to be disabled for an internal silent session")
	}
}

func TestConfiguredRunnerHost(t *testing.T) {
	oldEngine := engineFlag
	oldCloud := cloudFlag
	oldCloudEnv := cloudEngineEnvSet
	oldRunnerHost := RunnerHost
	t.Cleanup(func() {
		engineFlag = oldEngine
		cloudFlag = oldCloud
		cloudEngineEnvSet = oldCloudEnv
		RunnerHost = oldRunnerHost
	})
	t.Setenv(engineEnv, "")

	RunnerHost = "image://registry.example.com/dagger-engine:latest"

	reset := func() {
		engineFlag = ""
		cloudFlag = false
		cloudEngineEnvSet = false
	}

	t.Run("runner host fallback", func(t *testing.T) {
		reset()
		require.Equal(t, RunnerHost, configuredRunnerHost())
	})

	t.Run("cloud compatibility alias", func(t *testing.T) {
		reset()
		cloudFlag = true
		require.Equal(t, engine.DefaultCloudRunnerHost, configuredRunnerHost())
	})

	t.Run("cloud engine", func(t *testing.T) {
		reset()
		engineFlag = "cloud"
		require.Equal(t, engine.DefaultCloudRunnerHost, configuredRunnerHost())
	})

	t.Run("runner host URI", func(t *testing.T) {
		reset()
		engineFlag = "tcp://engine.example.com:1234"
		require.Equal(t, engineFlag, configuredRunnerHost())
	})

	t.Run("engine flag overrides cloud alias", func(t *testing.T) {
		reset()
		engineFlag = "tcp://engine.example.com:1234"
		cloudFlag = true
		require.Equal(t, engineFlag, configuredRunnerHost())
	})

	t.Run("engine env var", func(t *testing.T) {
		reset()
		t.Setenv(engineEnv, "tcp://env.example.com:1234")
		require.Equal(t, "tcp://env.example.com:1234", configuredRunnerHost())
	})

	t.Run("engine env var selects cloud", func(t *testing.T) {
		reset()
		t.Setenv(engineEnv, "cloud")
		require.Equal(t, engine.DefaultCloudRunnerHost, configuredRunnerHost())
	})

	t.Run("engine flag overrides engine env var", func(t *testing.T) {
		reset()
		engineFlag = "tcp://flag.example.com:1234"
		t.Setenv(engineEnv, "tcp://env.example.com:1234")
		require.Equal(t, engineFlag, configuredRunnerHost())
	})

	// --cloud is a deprecated alias for --engine=cloud, so it keeps a flag's
	// priority over the environment, even when DAGGER_CLOUD_ENGINE is set too.
	t.Run("cloud flag overrides engine env var", func(t *testing.T) {
		reset()
		cloudFlag = true
		t.Setenv(engineEnv, "tcp://env.example.com:1234")
		require.Equal(t, engine.DefaultCloudRunnerHost, configuredRunnerHost())

		cloudEngineEnvSet = true
		require.Equal(t, engine.DefaultCloudRunnerHost, configuredRunnerHost())
	})

	// DAGGER_CLOUD_ENGINE is deprecated, so DAGGER_ENGINE outranks it.
	t.Run("engine env var overrides deprecated cloud env var", func(t *testing.T) {
		reset()
		cloudEngineEnvSet = true
		t.Setenv(engineEnv, "tcp://env.example.com:1234")
		require.Equal(t, "tcp://env.example.com:1234", configuredRunnerHost())
	})

	t.Run("deprecated cloud env var without engine env var", func(t *testing.T) {
		reset()
		cloudEngineEnvSet = true
		require.Equal(t, engine.DefaultCloudRunnerHost, configuredRunnerHost())
	})
}
