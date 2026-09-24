package core

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
)

type EngineResourceMetricsSuite struct{}

// TestEngineResourceMetrics verifies that one engine-wide resource stream is
// exported through an explicitly configured standard OTLP metrics endpoint.
func TestEngineResourceMetrics(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(EngineResourceMetricsSuite{})
}

func (EngineResourceMetricsSuite) TestExport(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	sink := newEngineMetricSink(t, ctx)
	clientSink := newEngineMetricSink(t, ctx)
	const otlpPort = 4318
	otlpService := c.Host().Service([]dagger.PortForward{{
		Backend:  sink.port,
		Frontend: otlpPort,
	}})
	clientOTLPService := c.Host().Service([]dagger.PortForward{{
		Backend:  clientSink.port,
		Frontend: otlpPort,
	}})
	// Keep both receivers available through engine shutdown and restart.
	for _, service := range []*dagger.Service{otlpService, clientOTLPService} {
		_, err := service.Start(ctx)
		require.NoError(t, err)
		t.Cleanup(func() { _, _ = service.Stop(context.WithoutCancel(ctx)) })
	}

	engineName := "resource-metrics-" + identity.NewID()
	engineContainer := devEngineContainer(c,
		engineWithConfig(ctx, t, func(_ context.Context, _ *testctx.T, cfg config.Config) config.Config {
			cfg.Telemetry.ResourceMetrics = true
			return cfg
		}),
		func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithServiceBinding("otel-metrics", otlpService).
				WithEnvVariable(engine.DaggerNameEnv, engineName).
				WithEnvVariable("OTEL_METRIC_EXPORT_INTERVAL", "1000").
				// The outer engine injects a per-client telemetry proxy which does
				// not accept OTel sums. Select our standard OTLP receiver at startup.
				WithEnvVariable("TEST_METRICS_ENDPOINT", fmt.Sprintf("http://otel-metrics:%d/v1/metrics", otlpPort)).
				WithEntrypoint([]string{"sh", "-c", `
export OTEL_EXPORTER_OTLP_METRICS_ENDPOINT="$TEST_METRICS_ENDPOINT"
export OTEL_EXPORTER_OTLP_METRICS_PROTOCOL=http/protobuf
exec /usr/local/bin/dagger-entrypoint.sh "$@"
`, "engine-metrics"})
		})
	engineService := engineContainer.AsService(dagger.ContainerAsServiceOpts{
		UseEntrypoint:            true,
		InsecureRootCapabilities: true,
		NoInit:                   true, // Use the image's tini, as Docker does.
	})
	_, err := engineService.Start(ctx)
	require.NoError(t, err)
	engineRunning := true
	t.Cleanup(func() {
		if engineRunning {
			_, _ = engineService.Stop(context.WithoutCancel(ctx), dagger.ServiceStopOpts{Kill: true})
		}
	})

	first := sink.waitForSnapshot(t, ctx, func(snapshot engineMetricSnapshot) bool {
		return snapshot.engineName == engineName && snapshot.cpuTotal >= 0
	})
	require.Equal(t, "dagger-engine", first.serviceName)
	require.NotEmpty(t, first.instanceID)
	require.Equal(t, engine.Version, first.serviceVersion)
	assertEngineMemory(t, first)

	// Hold two independent clients open across collection intervals. Without
	// distinct inputs, Dagger could serve the second CLI exec from cache.
	var clients errgroup.Group
	for range 2 {
		client := engineClientContainer(ctx, t, c, engineService).
			WithServiceBinding("otel-clients", clientOTLPService).
			WithEnvVariable("CLIENT_NONCE", identity.NewID()).
			WithEnvVariable("TEST_CLIENT_METRICS_ENDPOINT", fmt.Sprintf("http://otel-clients:%d/v1/metrics", otlpPort)).
			WithExec([]string{"sh", "-c", `
export OTEL_EXPORTER_OTLP_METRICS_ENDPOINT="$TEST_CLIENT_METRICS_ENDPOINT"
export OTEL_EXPORTER_OTLP_METRICS_PROTOCOL=http/protobuf
exec dagger query
`}, dagger.ContainerWithExecOpts{
				Stdin: `{container{from(address:"` + alpineImage + `"){withExec(args:["sleep","12"]){stdout}}}}`,
			})
		clients.Go(func() error {
			_, err := client.Sync(ctx)
			return err
		})
	}
	require.NoError(t, clients.Wait())

	afterClients := sink.requestCount()
	second := sink.waitForSnapshot(t, ctx, func(snapshot engineMetricSnapshot) bool {
		return snapshot.engineName == engineName && snapshot.sequence > afterClients
	})
	assertEngineMemory(t, second)
	// Check collections during the client executions too, not just after the
	// workload cgroups have been removed.
	previousCPU := int64(0)
	for _, snapshot := range sink.collectedSnapshots() {
		require.Equal(t, first.instanceID, snapshot.instanceID)
		require.GreaterOrEqual(t, snapshot.cpuTotal, previousCPU)
		require.Equal(t, int64(0), snapshot.liveDescendants)
		previousCPU = snapshot.cpuTotal
	}

	// Verify both destinations carry real data, with no cross-routing.
	require.Contains(t, clientSink.collectedMetricNames(), "dagger.io/metrics.cpustat.usage")
	for _, name := range clientSink.collectedMetricNames() {
		require.False(t, strings.HasPrefix(name, "dagger.engine."), "engine metric reached client receiver: %s", name)
	}
	for _, name := range sink.collectedMetricNames() {
		require.True(t, strings.HasPrefix(name, "dagger.engine."), "non-resource metric reached engine receiver: %s", name)
	}

	// A graceful stop must flush one final collection while the callback is
	// still registered.
	beforeStop := sink.requestCount()
	_, err = engineService.Stop(ctx)
	require.NoError(t, err)
	engineRunning = false
	sink.waitForRequestCount(t, ctx, beforeStop+1)

	// A restarted engine is a new cumulative-counter epoch.
	beforeRestart := sink.requestCount()
	_, err = engineService.Start(ctx)
	require.NoError(t, err)
	engineRunning = true
	_, err = engineClientContainer(ctx, t, c, engineService).
		WithEnvVariable("CLIENT_NONCE", identity.NewID()).
		WithExec([]string{"dagger", "core", "version"}).
		Sync(ctx)
	require.NoError(t, err)

	restarted := sink.waitForSnapshot(t, ctx, func(snapshot engineMetricSnapshot) bool {
		return snapshot.engineName == engineName && snapshot.sequence > beforeRestart
	})
	require.NotEmpty(t, restarted.instanceID)
	require.NotEqual(t, first.instanceID, restarted.instanceID)
}

type engineMetricSink struct {
	port int

	mu          sync.Mutex
	requests    int
	snapshots   []engineMetricSnapshot
	metricNames map[string]struct{}
	notify      chan struct{}
}

type engineMetricSnapshot struct {
	sequence        int
	serviceName     string
	serviceVersion  string
	instanceID      string
	engineName      string
	cpuTotal        int64
	memoryCurrent   int64
	memoryPeak      int64
	liveDescendants int64
	availability    map[string]int64
}

func newEngineMetricSink(t *testctx.T, ctx context.Context) *engineMetricSink {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	sink := &engineMetricSink{
		port:        listener.Addr().(*net.TCPAddr).Port,
		metricNames: make(map[string]struct{}),
		notify:      make(chan struct{}, 1),
	}
	server := &http.Server{
		Handler: sink,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() { _ = server.Close() })
	return sink
}

func (sink *engineMetricSink) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.Path != "/v1/metrics" {
		http.NotFound(response, request)
		return
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	var exportRequest colmetricspb.ExportMetricsServiceRequest
	if err := proto.Unmarshal(body, &exportRequest); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}

	sink.mu.Lock()
	sink.requests++
	sequence := sink.requests
	for _, resourceMetrics := range exportRequest.ResourceMetrics {
		for _, scope := range resourceMetrics.ScopeMetrics {
			for _, metric := range scope.Metrics {
				sink.metricNames[metric.Name] = struct{}{}
			}
		}
		if snapshot, ok := parseEngineMetricSnapshot(sequence, resourceMetrics); ok {
			sink.snapshots = append(sink.snapshots, snapshot)
		}
	}
	sink.mu.Unlock()
	select {
	case sink.notify <- struct{}{}:
	default:
	}

	response.Header().Set("Content-Type", "application/x-protobuf")
	responseBody, _ := proto.Marshal(&colmetricspb.ExportMetricsServiceResponse{})
	_, _ = response.Write(responseBody)
}

func (sink *engineMetricSink) waitForSnapshot(t *testctx.T, ctx context.Context, predicate func(engineMetricSnapshot) bool) engineMetricSnapshot {
	t.Helper()
	timer := time.NewTimer(45 * time.Second)
	defer timer.Stop()
	for {
		sink.mu.Lock()
		for _, snapshot := range sink.snapshots {
			if predicate(snapshot) {
				sink.mu.Unlock()
				return snapshot
			}
		}
		sink.mu.Unlock()

		select {
		case <-sink.notify:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-timer.C:
			t.Fatal("timed out waiting for engine resource metrics")
		}
	}
}

func (sink *engineMetricSink) requestCount() int {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.requests
}

func (sink *engineMetricSink) waitForRequestCount(t *testctx.T, ctx context.Context, want int) {
	t.Helper()
	timer := time.NewTimer(45 * time.Second)
	defer timer.Stop()
	for {
		if sink.requestCount() >= want {
			return
		}
		select {
		case <-sink.notify:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-timer.C:
			t.Fatalf("timed out waiting for %d OTLP metric requests", want)
		}
	}
}

func (sink *engineMetricSink) collectedMetricNames() []string {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	names := make([]string, 0, len(sink.metricNames))
	for name := range sink.metricNames {
		names = append(names, name)
	}
	return names
}

func (sink *engineMetricSink) collectedSnapshots() []engineMetricSnapshot {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]engineMetricSnapshot(nil), sink.snapshots...)
}

func parseEngineMetricSnapshot(sequence int, resourceMetrics *metricspb.ResourceMetrics) (engineMetricSnapshot, bool) {
	snapshot := engineMetricSnapshot{
		sequence: sequence, cpuTotal: -1, memoryCurrent: -1, memoryPeak: -1, liveDescendants: -1,
		availability: map[string]int64{},
	}
	for _, attr := range resourceMetrics.GetResource().GetAttributes() {
		switch attr.GetKey() {
		case "service.name":
			snapshot.serviceName = attr.GetValue().GetStringValue()
		case "service.version":
			snapshot.serviceVersion = attr.GetValue().GetStringValue()
		case "service.instance.id":
			snapshot.instanceID = attr.GetValue().GetStringValue()
		case "dagger.io/engine.name":
			snapshot.engineName = attr.GetValue().GetStringValue()
		}
	}

	foundEngineScope := false
	for _, scopeMetrics := range resourceMetrics.GetScopeMetrics() {
		if scopeMetrics.GetScope().GetName() != "dagger.io/engine.resources" {
			continue
		}
		foundEngineScope = true
		for _, metric := range scopeMetrics.GetMetrics() {
			switch metric.GetName() {
			case "dagger.engine.cpu.time":
				snapshot.cpuTotal = sumPoint(metric, "cpu.mode", "total")
			case "dagger.engine.memory.current":
				snapshot.memoryCurrent = gaugePoint(metric, "", "")
			case "dagger.engine.memory.peak":
				snapshot.memoryPeak = gaugePoint(metric, "", "")
			case "dagger.engine.cgroup.descendants":
				snapshot.liveDescendants = gaugePoint(metric, "cgroup.state", "live")
			case "dagger.engine.resource_metrics.available":
				for _, point := range metric.GetGauge().GetDataPoints() {
					for _, attr := range point.GetAttributes() {
						if attr.GetKey() == "source" {
							snapshot.availability[attr.GetValue().GetStringValue()] = point.GetAsInt()
						}
					}
				}
			}
		}
	}
	return snapshot, foundEngineScope
}

func assertEngineMemory(t *testctx.T, snapshot engineMetricSnapshot) {
	t.Helper()
	// A nested test engine may have no memory controller delegated to it.
	// Verify absence explicitly rather than treating a missing metric as zero.
	require.Contains(t, snapshot.availability, "memory.current")
	require.Contains(t, snapshot.availability, "memory.peak")
	if snapshot.availability["memory.current"] == 1 {
		require.Positive(t, snapshot.memoryCurrent)
	} else {
		require.Equal(t, int64(-1), snapshot.memoryCurrent)
		t.Log("memory.current is unavailable in the nested engine cgroup")
	}
	if snapshot.availability["memory.peak"] == 1 {
		require.GreaterOrEqual(t, snapshot.memoryPeak, snapshot.memoryCurrent)
	} else {
		require.Equal(t, int64(-1), snapshot.memoryPeak)
	}
}

func sumPoint(metric *metricspb.Metric, key, value string) int64 {
	for _, point := range metric.GetSum().GetDataPoints() {
		if hasOTLPAttribute(point.GetAttributes(), key, value) {
			return point.GetAsInt()
		}
	}
	return -1
}

func gaugePoint(metric *metricspb.Metric, key, value string) int64 {
	for _, point := range metric.GetGauge().GetDataPoints() {
		if key == "" || hasOTLPAttribute(point.GetAttributes(), key, value) {
			return point.GetAsInt()
		}
	}
	return -1
}

func hasOTLPAttribute(attributes []*commonpb.KeyValue, key, value string) bool {
	for _, attr := range attributes {
		if attr.GetKey() == key && attr.GetValue().GetStringValue() == value {
			return true
		}
	}
	return false
}
