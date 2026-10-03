package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	enginetelemetry "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	daggerotel "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
)

func TestLLMNetworkClientCountsCompressedResponse(t *testing.T) {
	payload := strings.Repeat("data: model response\n\n", 1024)
	var encoded bytes.Buffer
	zw := gzip.NewWriter(&encoded)
	_, err := io.WriteString(zw, payload)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") != "gzip" {
			t.Error("client must negotiate gzip")
		}
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(encoded.Bytes())
	}))
	defer server.Close()

	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	ctx := daggerotel.WithMeterProvider(t.Context(), provider)
	ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2},
	}))
	ctx, err = enginetelemetry.WithNetworkRecording(ctx)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	resp, err := newLLMNetworkClient().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, payload, string(body))
	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &data))
	var received int64
	for _, scope := range data.ScopeMetrics {
		for _, current := range scope.Metrics {
			if current.Name == telemetryattrs.NetworkEstimatedRxBytes {
				received += current.Data.(metricdata.Gauge[int64]).DataPoints[0].Value
			}
		}
	}
	require.EqualValues(t, encoded.Len(), received)
	require.Less(t, received, int64(len(payload)))
}
