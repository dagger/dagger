package daggercmd

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
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

// noEnvExporters stands in for an environment configuring no OTEL_*
// exporters, keeping the tests from reaching the real (sync.Once-cached,
// process-wide) ones the test process's own environment configures.
func noEnvExporters(context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter) {
	return nil, nil, nil
}

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
	cfg, cloud, env := engineTelemetryConfigWith(context.Background(), func(context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter, bool) {
		return cloudSpans, cloudLogs, cloudMetrics, true
	}, noEnvExporters)

	require.False(t, cfg.Detect, "OTEL_* exporters are added explicitly, not detected by telemetry.Init")
	require.Equal(t, noTelemetryIndexes, env)
	require.Equal(t, telemetryIndexes{spans: 0, logs: 1, metrics: 1}, cloud,
		"the indexes locate the Cloud exporters, so engine telemetry can be forwarded without them")
	require.Equal(t, []sdklog.Exporter{localLogs}, withoutIndex(cfg.LiveLogExporters, cloud.logs))
	require.Equal(t, []sdkmetric.Exporter{localMetrics}, withoutIndex(cfg.LiveMetricExporters, cloud.metrics))
	require.Empty(t, withoutIndex(cfg.SpanProcessors, cloud.spans))
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
	cfg, _, _ := engineTelemetryConfigWith(withoutFrontendTelemetry(context.Background()), func(context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter, bool) {
		return cloudSpans, cloudLogs, cloudMetrics, true
	}, noEnvExporters)

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
// the SDK-command preflight session opens. Internal plumbing
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
	envExporters := func(context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter) {
		return tracetest.NewInMemoryExporter(), new(countingLogExporter), new(countingMetricExporter)
	}
	noCloud := func(context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter, bool) {
		return nil, nil, nil, false
	}

	skipSharedTelemetryExporters = false
	if _, _, env := engineTelemetryConfigWith(ctx, noCloud, envExporters); !env.configured() {
		t.Fatal("expected the OTEL_* exporters for a normal session")
	}

	skipSharedTelemetryExporters = true
	if _, cloud, env := engineTelemetryConfigWith(ctx, noCloud, envExporters); env != noTelemetryIndexes || cloud.configured() {
		t.Fatal("expected OTEL_* and Cloud exporters to be disabled for an internal silent session")
	}
}

// TestNestedCLIDoesNotForwardEngineTelemetryToParent guards against a nested
// CLI echoing the engine's telemetry back to its parent. The engine already
// routes a nested client's telemetry to every ancestor, so re-sending it
// through the OTEL_* exporters stored every log record twice in the parent's
// store: e.g. a staff_spawn tool result run under a nested `dagger agent`
// showed the sub-agent's task prompt twice, glued together.
//
// Hermetic: the OTEL_* exporters are injected, and the pipeline is built the
// way telemetry.Init builds it but without Init, which would replace this test
// process's own global telemetry.
func TestNestedCLIDoesNotForwardEngineTelemetryToParent(t *testing.T) {
	for _, live := range []bool{false, true} {
		t.Run(fmt.Sprintf("live traces %v", live), func(t *testing.T) {
			testNestedCLIForwarding(t, live)
		})
	}
}

func testNestedCLIForwarding(t *testing.T, liveTraces bool) {
	oldFrontend := Frontend
	oldSkip := skipSharedTelemetryExporters
	oldLive := telemetry.LiveTracesEnabled
	oldSpans, oldLogs, oldMetrics := telemetry.SpanProcessors, telemetry.LogProcessors, telemetry.MetricExporters
	oldCloud, oldEnv := cliCloudTelemetry, cliEnvTelemetry
	t.Cleanup(func() {
		Frontend = oldFrontend
		skipSharedTelemetryExporters = oldSkip
		telemetry.LiveTracesEnabled = oldLive
		telemetry.SpanProcessors, telemetry.LogProcessors, telemetry.MetricExporters = oldSpans, oldLogs, oldMetrics
		cliCloudTelemetry, cliEnvTelemetry = oldCloud, oldEnv
	})
	skipSharedTelemetryExporters = false
	telemetry.LiveTracesEnabled = liveTraces

	localSpans := tracetest.NewInMemoryExporter()
	localLogs := new(countingLogExporter)
	localMetrics := new(countingMetricExporter)
	Frontend = &idtui.FrontendMock{
		SpanExporterFunc:   func() sdktrace.SpanExporter { return localSpans },
		LogExporterFunc:    func() sdklog.Exporter { return localLogs },
		MetricExporterFunc: func() sdkmetric.Exporter { return localMetrics },
	}
	// The exporters the OTEL_* environment configures, which for a nested CLI
	// point at its parent.
	parentSpans := tracetest.NewInMemoryExporter()
	parentLogs := new(countingLogExporter)
	parentMetrics := new(countingMetricExporter)
	cfg, cloud, env := engineTelemetryConfigWith(t.Context(),
		func(context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter, bool) {
			return nil, nil, nil, false
		},
		func(context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter) {
			return parentSpans, parentLogs, parentMetrics
		})
	require.True(t, env.configured())

	// An earlier Init's leftovers: Init appends to these.
	telemetry.LogProcessors = []sdklog.Processor{sdklog.NewSimpleProcessor(new(countingLogExporter))}
	telemetry.MetricExporters = []sdkmetric.Exporter{new(countingMetricExporter)}
	cliCloudTelemetry, cliEnvTelemetry = pipelineIndexes(cloud), pipelineIndexes(env)
	// Build the pipeline as telemetry.Init does, with synchronous processors.
	telemetry.SpanProcessors = slices.Clone(cfg.SpanProcessors)
	for _, exp := range append(slices.Clone(cfg.LiveTraceExporters), cfg.BatchedTraceExporters...) {
		telemetry.SpanProcessors = append(telemetry.SpanProcessors, sdktrace.NewSimpleSpanProcessor(exp))
	}
	for _, exp := range cfg.LiveLogExporters {
		telemetry.LogProcessors = append(telemetry.LogProcessors, sdklog.NewSimpleProcessor(exp))
	}
	telemetry.MetricExporters = append(telemetry.MetricExporters, cfg.LiveMetricExporters...)
	require.Same(t, parentMetrics, telemetry.MetricExporters[cliEnvTelemetry.metrics])

	now := time.Now()
	engineSpan := tracetest.SpanStub{
		Name: "engine span",
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    trace.TraceID{1},
			SpanID:     trace.SpanID{1},
			TraceFlags: trace.FlagsSampled,
		}),
		StartTime: now,
		EndTime:   now,
		Resource:  resource.NewSchemaless(),
	}.Snapshot()
	forward := func(t *testing.T) client.Params {
		t.Helper()
		localSpans.Reset()
		parentSpans.Reset()
		localLogs.exports.Store(0)
		parentLogs.exports.Store(0)
		var params client.Params
		setEngineTelemetryParams(t.Context(), &params)
		var rec sdklog.Record
		rec.SetBody(log.StringValue("prompt"))
		require.NoError(t, params.EngineLogs.Export(t.Context(), []sdklog.Record{rec}))
		require.NoError(t, params.EngineTrace.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{engineSpan}))
		return params
	}

	t.Run("top-level CLI forwards to its OTEL exporters", func(t *testing.T) {
		// The test itself may run nested.
		t.Setenv("DAGGER_SESSION_PORT", "")
		require.NoError(t, os.Unsetenv("DAGGER_SESSION_PORT"))
		params := forward(t)
		require.Len(t, parentSpans.GetSpans(), 1)
		require.Equal(t, int64(1), parentLogs.exports.Load())
		require.True(t, slices.Contains(params.EngineMetrics, sdkmetric.Exporter(parentMetrics)))
		require.Len(t, localSpans.GetSpans(), 1)
		require.Equal(t, int64(1), localLogs.exports.Load())
	})

	t.Run("nested CLI forwards only to its own exporters", func(t *testing.T) {
		t.Setenv("DAGGER_SESSION_PORT", "1234")
		params := forward(t)
		require.Empty(t, parentSpans.GetSpans(), "the engine already delivered these spans to the parent")
		require.Zero(t, parentLogs.exports.Load(), "the engine already delivered these logs to the parent")
		require.False(t, slices.Contains(params.EngineMetrics, sdkmetric.Exporter(parentMetrics)))
		require.True(t, slices.Contains(params.EngineMetrics, sdkmetric.Exporter(localMetrics)))
		require.Len(t, localSpans.GetSpans(), 1)
		require.Equal(t, int64(1), localLogs.exports.Load())
	})
}
func TestWithoutIndexes(t *testing.T) {
	all := []string{"frontend", "cloud", "env"}
	require.Equal(t, []string{"frontend"}, withoutIndexes(all, 2, 1))
	require.Equal(t, []string{"frontend", "env"}, withoutIndexes(all, -1, 1))
	require.Equal(t, all, withoutIndexes(all, -1, 3))
	require.Nil(t, withoutIndexes[string](nil, -1))
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
		value, ok := Resource(t.Context()).Set().Value(attribute.Key(telemetryattrs.CloudEngineAttr))
		require.True(t, ok)
		require.True(t, value.AsBool())
	})

	t.Run("runner host URI", func(t *testing.T) {
		reset()
		engineFlag = "tcp://engine.example.com:1234"
		require.Equal(t, engineFlag, configuredRunnerHost())
		value, ok := Resource(t.Context()).Set().Value(attribute.Key(telemetryattrs.CloudEngineAttr))
		require.True(t, ok)
		require.False(t, value.AsBool())
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
