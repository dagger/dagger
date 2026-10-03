package resolver

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/remotes/docker"
	enginetelemetry "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	daggerotel "github.com/dagger/otel-go"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
)

func TestRegistryPullAttributedNetworkBytes(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(t.Context())) })
	ctx := daggerotel.WithMeterProvider(context.Background(), provider)
	ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2},
	}))
	recorder, err := enginetelemetry.NewNetworkAccumulator(ctx, enginetelemetry.NetworkRX)
	require.NoError(t, err)

	payload := []byte(strings.Repeat("compressible registry content", 1024))
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, err = zw.Write(payload)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Contains(t, r.Header.Get("Accept-Encoding"), "gzip")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(compressed.Bytes())
	}))
	t.Cleanup(server.Close)
	host := strings.TrimPrefix(server.URL, "http://")
	hosts := registryHostsWithNetworkRecorder(func(string) ([]docker.RegistryHost, error) {
		return []docker.RegistryHost{{
			Client: server.Client(), Host: host, Scheme: "http", Path: "/v2",
			Capabilities: docker.HostCapabilityPull,
		}}, nil
	}, recorder)
	resolver := docker.NewResolver(docker.ResolverOptions{Hosts: hosts})
	fetcher, err := resolver.Fetcher(ctx, host+"/test/image")
	require.NoError(t, err)
	body, err := fetcher.Fetch(ctx, ocispecs.Descriptor{
		Digest: digest.FromBytes(payload), Size: int64(len(payload)),
		MediaType: ocispecs.MediaTypeImageLayer,
	})
	require.NoError(t, err)
	got, err := io.ReadAll(body)
	require.NoError(t, err)
	require.NoError(t, body.Close())
	require.Equal(t, payload, got)
	require.Less(t, compressed.Len(), len(payload))

	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &data))
	require.Equal(t, telemetryattrs.NetworkEstimatedRxBytes, data.ScopeMetrics[0].Metrics[0].Name)
	gauge := data.ScopeMetrics[0].Metrics[0].Data.(metricdata.Gauge[int64])
	require.EqualValues(t, compressed.Len(), gauge.DataPoints[0].Value)
}
