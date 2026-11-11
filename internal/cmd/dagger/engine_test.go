package daggercmd

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/util/cleanups"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type countingLogExporter struct {
	exports atomic.Int64
	records atomic.Int64
}

func (e *countingLogExporter) Export(_ context.Context, logs []sdklog.Record) error {
	e.exports.Add(1)
	e.records.Add(int64(len(logs)))
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
	collectorSpans := tracetest.NewInMemoryExporter()
	collectorLogs := new(countingLogExporter)
	collectorMetrics := new(countingMetricExporter)
	cfg, local, collector, collectorCounts := engineTelemetryConfigWithExporters(context.Background(), func(context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter, bool) {
		return cloudSpans, cloudLogs, cloudMetrics, true
	}, func(context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter) {
		return collectorSpans, collectorLogs, collectorMetrics
	})

	require.NotNil(t, local)
	require.Equal(t, [3]int{1, 0, 1}, [3]int{local.spans, local.logs, local.metrics},
		"the indexes locate local telemetry, so engine telemetry can be forwarded without it")
	require.NotNil(t, local.cloud)
	require.False(t, cfg.Detect, "the user's collector is configured here, not by telemetry.Init")
	require.NotNil(t, collectorCounts, "the collector's spans are counted, to report drops")
	require.Equal(t, collectorTelemetryRanges{
		spans:   indexRange{2, 3},
		logs:    indexRange{1, 2},
		metrics: indexRange{2, 3},
	}, collector, "the ranges locate the collector exporters, so a nested client can forward engine telemetry without them")
	require.Equal(t, []sdklog.Exporter{collectorLogs}, withoutIndex(cfg.LiveLogExporters, local.logs), "frontend logs are installed separately with a blocking processor")
	require.Equal(t, []sdkmetric.Exporter{localMetrics, collectorMetrics}, withoutIndex(cfg.LiveMetricExporters, local.metrics))
	require.Len(t, withoutIndex(cfg.SpanProcessors, local.spans), 2, "frontend and collector spans each use their own processor")
	require.Len(t, frontend.SpanExporterCalls(), 1)
	require.Empty(t, frontend.LogExporterCalls(), "frontend logs are installed separately with a blocking processor")
	require.Len(t, frontend.MetricExporterCalls(), 1)
	require.Empty(t, cfg.LiveTraceExporters)
	require.Empty(t, cfg.BatchedTraceExporters)
	require.Equal(t, []sdklog.Exporter{local.logSink, collectorLogs}, cfg.LiveLogExporters)
	require.Equal(t, []sdkmetric.Exporter{localMetrics, local.metricSink, collectorMetrics}, cfg.LiveMetricExporters)
	require.Len(t, cfg.SpanProcessors, 3, "Cloud, frontend and collector spans each use their own processor")
	t.Cleanup(func() {
		for _, processor := range cfg.SpanProcessors {
			require.NoError(t, processor.Shutdown(context.Background()))
		}
	})
}

// slowSpanExporter is a frontend that takes a while with each batch, and
// records the spans it has seen end.
type slowSpanExporter struct {
	delay time.Duration

	mu    sync.Mutex
	ended map[trace.SpanID]struct{}
}

func (e *slowSpanExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	time.Sleep(e.delay)
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, span := range spans {
		if !span.EndTime().Before(span.StartTime()) {
			e.ended[span.SpanContext().SpanID()] = struct{}{}
		}
	}
	return nil
}

func (e *slowSpanExporter) endedCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.ended)
}

func (e *slowSpanExporter) Shutdown(context.Context) error   { return nil }
func (e *slowSpanExporter) ForceFlush(context.Context) error { return nil }

// TestEngineSpanBurstReachesFrontend forwards a burst of finished engine
// spans, more than any span queue holds, the way the CLI forwards what the
// engine streams to it. Every one must reach the frontend finished: a span
// lost on the way leaves its check or test looking like it never finished.
func TestEngineSpanBurstReachesFrontend(t *testing.T) {
	oldFrontend := Frontend
	oldSkip := skipSharedTelemetryExporters
	t.Cleanup(func() {
		Frontend = oldFrontend
		skipSharedTelemetryExporters = oldSkip
	})
	// Only the frontend: no Cloud or OTEL_* exporters.
	skipSharedTelemetryExporters = true

	frontendSpans := &slowSpanExporter{
		delay: 10 * time.Millisecond,
		ended: map[trace.SpanID]struct{}{},
	}
	Frontend = &idtui.FrontendMock{
		SpanExporterFunc:   func() sdktrace.SpanExporter { return frontendSpans },
		LogExporterFunc:    func() sdklog.Exporter { return new(countingLogExporter) },
		MetricExporterFunc: func() sdkmetric.Exporter { return new(countingMetricExporter) },
	}
	cfg, _, _, _ := engineTelemetryConfig(context.Background()) //nolint:dogsled // Only the config is needed here.
	// The span processors telemetry.Init builds from cfg, without installing
	// them globally.
	processors := slices.Clone(cfg.SpanProcessors)
	for _, exporter := range cfg.LiveTraceExporters {
		processors = append(processors, telemetry.NewLiveSpanProcessor(exporter))
	}
	forwarder := telemetry.SpanForwarder{Processors: processors}

	const burst = 2*enginetel.LargeSpanQueueSize + 1
	now := time.Now()
	spans := make([]sdktrace.ReadOnlySpan, 0, burst)
	for i := range burst {
		var spanID trace.SpanID
		binary.BigEndian.PutUint64(spanID[:], uint64(i+1))
		spans = append(spans, tracetest.SpanStub{
			Name: "check",
			SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
				TraceID:    trace.TraceID{1},
				SpanID:     spanID,
				TraceFlags: trace.FlagsSampled,
			}),
			StartTime: now,
			EndTime:   now,
		}.Snapshot())
	}
	require.NoError(t, forwarder.ExportSpans(context.Background(), spans))
	require.NoError(t, forwarder.Shutdown(context.Background()))

	require.Equal(t, burst, frontendSpans.endedCount())
}

func noCollectorExporters(context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter) {
	return nil, nil, nil
}

// finishedSpans returns n finished, sampled span snapshots.
func finishedSpans(n int) []sdktrace.ReadOnlySpan {
	now := time.Now()
	spans := make([]sdktrace.ReadOnlySpan, 0, n)
	for i := range n {
		var spanID trace.SpanID
		binary.BigEndian.PutUint64(spanID[:], uint64(i+1))
		spans = append(spans, tracetest.SpanStub{
			Name: "check",
			SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
				TraceID:    trace.TraceID{1},
				SpanID:     spanID,
				TraceFlags: trace.FlagsSampled,
			}),
			StartTime: now,
			EndTime:   now,
		}.Snapshot())
	}
	return spans
}

// TestEngineSpanBurstReachesCollector forwards a burst of finished engine
// spans to the user's own collector (OTEL_EXPORTER_OTLP_*), the way the CLI
// forwards what the engine streams to it, while the collector takes a while
// with each batch. A burst that fits the CLI's span queues must arrive whole,
// whether spans are exported as they end or also as they start. One that
// doesn't fit loses spans, and the user is told how many.
func TestEngineSpanBurstReachesCollector(t *testing.T) {
	oldSkip := skipSharedTelemetryExporters
	oldLive := telemetry.LiveTracesEnabled
	t.Cleanup(func() {
		skipSharedTelemetryExporters = oldSkip
		telemetry.LiveTracesEnabled = oldLive
	})
	skipSharedTelemetryExporters = false

	for _, live := range []bool{false, true} {
		for _, burst := range []int{enginetel.LargeSpanQueueSize, 2*enginetel.LargeSpanQueueSize + 1} {
			t.Run(fmt.Sprintf("live=%t/burst=%d", live, burst), func(t *testing.T) {
				telemetry.LiveTracesEnabled = live
				collectorSpans := &slowSpanExporter{
					delay: 10 * time.Millisecond,
					ended: map[trace.SpanID]struct{}{},
				}
				// Only the user's collector: no Cloud or frontend.
				cfg, _, _, counts := engineTelemetryConfigWithExporters(withoutFrontendTelemetry(context.Background()), func(context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter, bool) {
					return nil, nil, nil, false
				}, func(context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter) {
					return collectorSpans, nil, nil
				})
				forwarder := telemetry.SpanForwarder{Processors: cfg.SpanProcessors}

				require.NoError(t, forwarder.ExportSpans(context.Background(), finishedSpans(burst)))
				require.NoError(t, forwarder.Shutdown(context.Background()))

				var report strings.Builder
				reportCollectorSpanDrops(&report, counts)
				received := collectorSpans.endedCount()
				if burst <= enginetel.LargeSpanQueueSize {
					require.Equal(t, burst, received)
					require.Empty(t, report.String())
					return
				}
				require.Less(t, received, burst)
				require.Contains(t, report.String(), fmt.Sprintf("%d of %d spans were not sent", burst-received, burst))
			})
		}
	}
}

// Without a frontend, and before any engine takes it, local telemetry is
// held; at shutdown it goes to Cloud, as it always did.
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
	cfg, local, _, collectorCounts := engineTelemetryConfigWithExporters(withoutFrontendTelemetry(context.Background()), func(context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter, bool) {
		return keptSpans{cloudSpans}, cloudLogs, cloudMetrics, true
	}, noCollectorExporters)

	require.Nil(t, collectorCounts)
	require.Empty(t, frontend.SpanExporterCalls())
	require.Empty(t, frontend.LogExporterCalls())
	require.Empty(t, frontend.MetricExporterCalls())
	require.Empty(t, cfg.LiveTraceExporters)
	require.Equal(t, []sdklog.Exporter{local.logSink}, cfg.LiveLogExporters)
	require.Equal(t, []sdkmetric.Exporter{local.metricSink}, cfg.LiveMetricExporters)
	require.Len(t, cfg.SpanProcessors, 1)

	snapshot := tracetest.SpanStub{
		Name: "local span",
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
	require.NoError(t, cfg.LiveLogExporters[0].Export(ctx, []sdklog.Record{{}}))
	require.NoError(t, cfg.LiveMetricExporters[0].Export(ctx, testResourceMetrics()))
	require.Empty(t, cloudSpans.GetSpans(), "held for an engine")
	require.Zero(t, cloudLogs.exports.Load())
	require.Zero(t, cloudMetrics.exports.Load())

	require.NoError(t, cfg.SpanProcessors[0].Shutdown(ctx))
	require.NoError(t, cfg.LiveLogExporters[0].Shutdown(ctx))
	require.NoError(t, cfg.LiveMetricExporters[0].Shutdown(ctx))

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
	var cloudCalls, collectorCalls int
	cloudExporters := func(context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter, bool) {
		cloudCalls++
		return tracetest.NewInMemoryExporter(), new(countingLogExporter), new(countingMetricExporter), true
	}
	collectorExporters := func(context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter) {
		collectorCalls++
		return nil, new(countingLogExporter), nil
	}

	skipSharedTelemetryExporters = false
	cfg, local, _, _ := engineTelemetryConfigWithExporters(ctx, cloudExporters, collectorExporters)
	require.NotNil(t, local, "a normal session exports to Cloud")
	require.Equal(t, 1, collectorCalls, "a normal session exports to the user's collector")
	require.Len(t, cfg.LiveLogExporters, 2)
	for _, processor := range cfg.SpanProcessors {
		require.NoError(t, processor.Shutdown(ctx))
	}

	skipSharedTelemetryExporters = true
	cfg, local, _, _ = engineTelemetryConfigWithExporters(ctx, cloudExporters, collectorExporters)
	require.False(t, local != nil, "an internal silent session doesn't export to Cloud")
	require.Equal(t, 1, cloudCalls, "an internal silent session doesn't set up the Cloud exporters")
	require.Equal(t, 1, collectorCalls, "an internal silent session doesn't set up the user's collector")
	require.False(t, cfg.Detect)
	require.Empty(t, cfg.LiveLogExporters)
	for _, processor := range cfg.SpanProcessors {
		require.NoError(t, processor.Shutdown(ctx))
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

// A full local queue must not hang EOF emission or report successful output
// delivery. Even a late successful export must not resume the canceled queue.
func TestCLIOutputDrainFailure(t *testing.T) {
	exporter := &cliDrainBlockedExporter{
		entered: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{}),
	}
	release := sync.OnceFunc(func() { close(exporter.release) })
	defer release()
	processor := enginetel.NewBlockingLogProcessor(exporter, 1, 1, 0)
	ctx := telemetry.WithLoggerProvider(t.Context(), sdklog.NewLoggerProvider(sdklog.WithProcessor(processor)))
	t.Cleanup(func() {
		release()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		processor.Shutdown(cleanupCtx)
	})
	var record sdklog.Record
	require.NoError(t, processor.OnEmit(ctx, &record))
	select {
	case <-exporter.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("export did not start")
	}
	require.NoError(t, processor.OnEmit(ctx, &record)) // fill the waiting queue
	result := make(chan error, 1)
	go func() {
		result <- idtui.NewLogs(io.Discard).Run(ctx, dagui.FrontendOpts{Silent: true}, func(ctx context.Context) (cleanups.CleanupF, error) {
			return func() error {
				drainCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
				return &idtui.OutputDrainError{Err: drainCLIOutput(drainCtx, processor)}
			}, nil
		})
	}()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.DeadlineExceeded)
		var exit idtui.ExitError
		require.ErrorAs(t, err, &exit)
		require.Equal(t, 1, exit.Code(), "incomplete output must fail the command")
	case <-time.After(5 * time.Second):
		t.Fatal("EOF emission or final rendering exceeded the drain deadline")
	}
	release()
	select {
	case <-exporter.done:
	case <-time.After(5 * time.Second):
		t.Fatal("exporter cleanup did not finish")
	}
	require.Equal(t, int64(1), exporter.calls.Load(), "late success must not resume the canceled queue")
}

type cliDrainBlockedExporter struct {
	entered, release, done chan struct{}
	calls                  atomic.Int64
}

func (e *cliDrainBlockedExporter) Export(context.Context, []sdklog.Record) error {
	if e.calls.Add(1) == 1 {
		close(e.entered)
	}
	<-e.release // deliberately ignore cancellation, like blocked terminal I/O
	return nil
}

func (e *cliDrainBlockedExporter) Shutdown(context.Context) error {
	close(e.done)
	return nil
}
func (*cliDrainBlockedExporter) ForceFlush(context.Context) error { return nil }
