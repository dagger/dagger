package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/dagql/cachefact"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/config"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

type cacheFactExportRequest struct {
	user    string
	logs    *collogspb.ExportLogsServiceRequest
	path    string
	writer  string
	hasAuth bool
}

// With _EXPERIMENTAL_DAGGER_CACHE_FACTS_EXPORT and DAGGER_CLOUD_TOKEN set, the
// engine exports its cache facts to
// DAGGER_CLOUD_URL under the token, with the engine instance in the resource,
// through a logger provider of its own: the process context keeps its own
// provider, so nothing else emitted in the process reaches Cloud.
func TestCacheFactExportSendsOnlyFactsToCloud(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []cacheFactExportRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var logs collogspb.ExportLogsServiceRequest
		require.NoError(t, proto.Unmarshal(body, &logs))
		user, _, ok := r.BasicAuth()
		mu.Lock()
		requests = append(requests, cacheFactExportRequest{user: user, hasAuth: ok, logs: &logs, path: r.URL.Path, writer: r.Header.Get("X-Dagger-Export")})
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	t.Setenv(envCacheFactsExport, "1")
	t.Setenv("DAGGER_CLOUD_TOKEN", "engine-token")
	t.Setenv("DAGGER_CLOUD_URL", srv.URL)

	ctx, res := InitTelemetry(t.Context(), "instance-a")
	export := newCacheFactExport(ctx, res, config.TelemetryConfig{})
	require.True(t, export.Enabled())
	require.NotSame(t, export.provider, telemetry.LoggerProvider(ctx), "the process context keeps its own logger provider")

	var fact log.Record
	fact.SetBody(log.StringValue("fact"))
	export.Logger().Emit(ctx, fact)
	var other log.Record
	other.SetBody(log.StringValue("snapshot progress"))
	telemetry.Logger(ctx, "dagger.io/engine").Emit(ctx, other)
	require.NoError(t, export.Shutdown(context.Background()))
	require.NoError(t, export.Shutdown(context.Background()), "a second shutdown returns the first result")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, requests, 1)
	got := requests[0]
	require.True(t, got.hasAuth)
	require.Equal(t, "engine-token", got.user)
	require.Equal(t, "/v1/logs", got.path)
	require.NotEmpty(t, got.writer)
	require.Len(t, got.logs.ResourceLogs, 1)
	instance := ""
	for _, kv := range got.logs.ResourceLogs[0].Resource.Attributes {
		if kv.Key == cachefact.ResourceEngineInstance {
			instance = kv.Value.GetStringValue()
		}
	}
	require.Equal(t, "instance-a", instance)
	var bodies []string
	for _, scopeLogs := range got.logs.ResourceLogs[0].ScopeLogs {
		require.Equal(t, cachefact.ScopeName, scopeLogs.Scope.Name)
		for _, rec := range scopeLogs.LogRecords {
			bodies = append(bodies, rec.Body.GetStringValue())
		}
	}
	require.Equal(t, []string{"fact"}, bodies)
}

func TestCacheFactExportDisabledWithoutToken(t *testing.T) {
	t.Setenv(envCacheFactsExport, "1")
	t.Setenv("DAGGER_CLOUD_TOKEN", "")
	export := newCacheFactExport(t.Context(), resource.Empty(), config.TelemetryConfig{CacheFacts: true})
	require.False(t, export.Enabled())
	require.NoError(t, export.Shutdown(t.Context()))
	require.Nil(t, serverCacheFactExport(export), "the server gets no export, not a nil pointer")
}

// A client forwards DAGGER_CLOUD_TOKEN into every engine it provisions, so the
// token alone exports nothing: no fact reaches Cloud unless
// _EXPERIMENTAL_DAGGER_CACHE_FACTS_EXPORT is set too.
func TestCacheFactExportDisabledWithTokenAlone(t *testing.T) {
	var (
		mu       sync.Mutex
		requests int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	t.Setenv(envCacheFactsExport, "")
	t.Setenv("DAGGER_CLOUD_TOKEN", "forwarded-token")
	t.Setenv("DAGGER_CLOUD_URL", srv.URL)

	ctx, res := InitTelemetry(t.Context(), "instance-a")
	export := newCacheFactExport(ctx, res, config.TelemetryConfig{})
	require.False(t, export.Enabled())
	require.Nil(t, serverCacheFactExport(export), "the server gets no export, so it creates no emitter")

	// A record in the facts scope on the process's own provider does not
	// reach Cloud either.
	var fact log.Record
	fact.SetBody(log.StringValue("fact"))
	telemetry.Logger(ctx, cachefact.ScopeName).Emit(ctx, fact)
	require.NoError(t, export.Shutdown(context.Background()))

	mu.Lock()
	defer mu.Unlock()
	require.Zero(t, requests, "nothing is sent to Cloud")
}

// The engine config's telemetry.cacheFacts enables the export like the
// environment variable does, and like it, only with DAGGER_CLOUD_TOKEN.
func TestCacheFactExportEnabledByEngineConfig(t *testing.T) {
	t.Setenv(envCacheFactsExport, "")
	enabled := config.TelemetryConfig{CacheFacts: true}

	t.Setenv("DAGGER_CLOUD_TOKEN", "engine-token")
	t.Setenv("DAGGER_CLOUD_URL", "http://127.0.0.1:1")
	export := newCacheFactExport(t.Context(), resource.Empty(), enabled)
	require.True(t, export.Enabled(), "config and token")
	require.NoError(t, export.Shutdown(t.Context()))

	require.False(t, newCacheFactExport(t.Context(), resource.Empty(), config.TelemetryConfig{}).Enabled(),
		"token without either switch")

	t.Setenv("DAGGER_CLOUD_TOKEN", "")
	require.False(t, newCacheFactExport(t.Context(), resource.Empty(), enabled).Enabled(), "config without token")
}

func TestEngineTelemetry(t *testing.T) {
	const childEnv = "_DAGGER_TEST_ENGINE_TELEMETRY"
	const clientEndpointEnv = "_DAGGER_TEST_CLIENT_METRICS_ENDPOINT"
	const engineProbe = "test.engine.collection"
	const clientProbe = "test.client.collection"
	if rawConfig, ok := os.LookupEnv(childEnv); ok {
		cfg, err := config.Load(strings.NewReader(rawConfig))
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		clientExporter, err := otlpmetrichttp.New(ctx,
			otlpmetrichttp.WithEndpointURL(os.Getenv(clientEndpointEnv)),
			otlpmetrichttp.WithHeaders(map[string]string{"test-destination": "client"}),
		)
		require.NoError(t, err)
		clientProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(clientExporter)))
		defer clientProvider.Shutdown(context.Background()) //nolint:errcheck
		ctx = telemetry.WithMeterProvider(ctx, clientProvider)
		ctx, _ = InitTelemetry(ctx, engineInstanceID)
		resources := initResourceMetrics(ctx, cfg.Telemetry)

		// Resource telemetry must neither replace an existing context provider
		// nor receive the metrics recorded through it.
		var clientValue atomic.Int64
		clientValue.Store(11)
		_, err = telemetry.Meter(ctx, "test.client").Int64ObservableGauge(clientProbe,
			metric.WithInt64Callback(func(_ context.Context, observer metric.Int64Observer) error {
				observer.Observe(clientValue.Load())
				return nil
			}),
		)
		require.NoError(t, err)
		require.NoError(t, telemetry.MeterProvider(ctx).ForceFlush(ctx))

		// Probe fresh shutdown collection without requiring a writable cgroup
		// mount on the machine that runs this test.
		var engineValue atomic.Int64
		if resources != nil {
			engineValue.Store(7)
			_, err = resources.Meter("test.engine").Int64ObservableGauge(engineProbe,
				metric.WithInt64Callback(func(_ context.Context, observer metric.Int64Observer) error {
					observer.Observe(engineValue.Load())
					return nil
				}),
			)
			require.NoError(t, err)
			require.NoError(t, resources.ForceFlush(ctx))
			engineValue.Store(19)
		}
		_, span := otel.Tracer("test").Start(ctx, "engine test span")
		span.End()
		var record log.Record
		record.SetBody(log.StringValue("engine test log"))
		telemetry.Logger(ctx, "test").Emit(ctx, record)
		cancel()
		closeResourceMetrics(ctx, resources)

		// Closing the engine resource exporter must not close a client exporter.
		clientValue.Store(23)
		require.NoError(t, clientProvider.ForceFlush(context.WithoutCancel(ctx)))
		telemetry.Close()
		return
	}

	// Initialization changes global trace/log state. Use separate processes
	// to exercise real config loading, exporter env settings, and identity.
	for _, tc := range []struct {
		name     string
		config   string
		endpoint bool
		protocol string
		export   bool
	}{
		{name: "default with injected endpoint", config: `{}`, endpoint: true},
		{name: "explicitly disabled", config: `{"telemetry":{"resourceMetrics":false}}`, endpoint: true},
		{name: "enabled without metrics endpoint", config: `{"telemetry":{"resourceMetrics":true}}`},
		{name: "enabled HTTP", config: `{"telemetry":{"resourceMetrics":true}}`, endpoint: true, protocol: "http/protobuf", export: true},
		{name: "enabled gRPC", config: `{"telemetry":{"resourceMetrics":true}}`, endpoint: true, protocol: "grpc", export: true},
		{name: "unsupported protocol", config: `{"telemetry":{"resourceMetrics":true}}`, endpoint: true, protocol: "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engineSink, engineURL := newTelemetryReceiver(t, tc.protocol == "grpc")
			clientSink, clientURL := newTelemetryReceiver(t, false)
			env := []string{}
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "OTEL_") && !strings.HasPrefix(entry, engine.DaggerNameEnv+"=") {
					env = append(env, entry)
				}
			}
			env = append(env,
				childEnv+"="+tc.config,
				clientEndpointEnv+"="+clientURL+"/v1/metrics",
				engine.DaggerNameEnv+"=test-engine",
				"OTEL_EXPORTER_OTLP_ENDPOINT="+engineURL,
				"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
				"OTEL_EXPORTER_OTLP_HEADERS=test-destination=generic",
				"OTEL_EXPORTER_OTLP_METRICS_HEADERS=test-destination=engine",
				"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT="+clientURL+"/v1/traces",
				"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT="+clientURL+"/v1/logs",
			)
			if tc.endpoint {
				endpoint := engineURL
				if tc.protocol != "grpc" {
					endpoint += "/v1/metrics"
				}
				env = append(env, "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT="+endpoint)
			}
			if tc.protocol != "" {
				env = append(env, "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL="+tc.protocol)
			}
			processes := 1
			if tc.name == "enabled HTTP" {
				processes = 2 // A process restart must change the resource identity.
			}
			var previousID string
			for range processes {
				cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestEngineTelemetry$")
				cmd.Env = env
				out, err := cmd.CombinedOutput()
				require.NoError(t, err, "%s", out)

				clientRequests := clientSink.take()
				var clientValues []int64
				for _, request := range clientRequests {
					require.Equal(t, "/v1/metrics", request.path, "must not enable trace or log export")
					require.Equal(t, "client", request.destination)
					for _, rm := range request.metrics.ResourceMetrics {
						for _, scope := range rm.ScopeMetrics {
							require.Equal(t, "test.client", scope.Scope.Name, "engine metrics must not reach the client endpoint")
							for _, m := range scope.Metrics {
								for _, point := range m.GetGauge().GetDataPoints() {
									clientValues = append(clientValues, point.GetAsInt())
								}
							}
						}
					}
				}
				require.GreaterOrEqual(t, len(clientValues), 2)
				require.Equal(t, int64(11), clientValues[0])
				require.Equal(t, int64(23), clientValues[len(clientValues)-1])

				requests := engineSink.take()
				if !tc.export {
					require.Empty(t, requests)
					continue
				}
				var instanceID string
				var values []int64
				for _, request := range requests {
					require.Equal(t, "engine", request.destination, "signal-specific headers must override generic headers")
					for _, rm := range request.metrics.ResourceMetrics {
						attrs := map[string]string{}
						for _, attr := range rm.GetResource().GetAttributes() {
							attrs[attr.Key] = attr.GetValue().GetStringValue()
						}
						require.Equal(t, "dagger-engine", attrs["service.name"])
						require.Equal(t, engine.Version, attrs["service.version"])
						require.Equal(t, "test-engine", attrs["dagger.io/engine.name"])
						require.NotEmpty(t, attrs["host.name"])
						require.NotEmpty(t, attrs["service.instance.id"])
						if instanceID == "" {
							instanceID = attrs["service.instance.id"]
						}
						require.Equal(t, instanceID, attrs["service.instance.id"])
						for _, scope := range rm.ScopeMetrics {
							require.Contains(t, []string{"dagger.io/engine.resources", "test.engine"}, scope.Scope.Name,
								"client metrics must not reach the engine endpoint")
							for _, m := range scope.Metrics {
								if m.Name == engineProbe {
									for _, point := range m.GetGauge().GetDataPoints() {
										values = append(values, point.GetAsInt())
									}
								}
							}
						}
					}
				}
				require.GreaterOrEqual(t, len(values), 2)
				require.Equal(t, int64(7), values[0])
				require.Equal(t, int64(19), values[len(values)-1], "shutdown must collect after cancellation")
				require.NotEqual(t, previousID, instanceID)
				previousID = instanceID
			}
		})
	}
}

type telemetryRequest struct {
	path        string
	destination string
	metrics     *colmetricspb.ExportMetricsServiceRequest
}

type telemetryReceiver struct {
	colmetricspb.UnimplementedMetricsServiceServer
	mu       sync.Mutex
	requests []telemetryRequest
}

func newTelemetryReceiver(t *testing.T, useGRPC bool) (*telemetryReceiver, string) {
	t.Helper()
	sink := new(telemetryReceiver)
	if useGRPC {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		server := grpc.NewServer()
		colmetricspb.RegisterMetricsServiceServer(server, sink)
		go func() { _ = server.Serve(listener) }()
		t.Cleanup(server.Stop)
		return sink, "http://" + listener.Addr().String()
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := new(colmetricspb.ExportMetricsServiceRequest)
		if r.URL.Path == "/v1/metrics" {
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := proto.Unmarshal(data, req); err != nil {
				t.Error(err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		sink.mu.Lock()
		sink.requests = append(sink.requests, telemetryRequest{r.URL.Path, r.Header.Get("test-destination"), req})
		sink.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	t.Cleanup(server.Close)
	return sink, server.URL
}

func (sink *telemetryReceiver) Export(ctx context.Context, request *colmetricspb.ExportMetricsServiceRequest) (*colmetricspb.ExportMetricsServiceResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	sink.mu.Lock()
	sink.requests = append(sink.requests, telemetryRequest{"grpc", strings.Join(md.Get("test-destination"), ","), request})
	sink.mu.Unlock()
	return new(colmetricspb.ExportMetricsServiceResponse), nil
}

func (sink *telemetryReceiver) take() []telemetryRequest {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	requests := sink.requests
	sink.requests = nil
	return requests
}
