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
	"unicode/utf8"

	enginetelemetry "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestLLMAttributedNetworkBytes(t *testing.T) {
	const requestBody = `{"model":"test"}`
	responseBody := strings.Repeat(`{"ok":true}`, 1024)
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, err := io.WriteString(zw, responseBody)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	compressedBody := compressed.Bytes()
	var acceptEncoding string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acceptEncoding = r.Header.Get("Accept-Encoding")
		_, err := io.Copy(io.Discard, r.Body)
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		_, err = w.Write(compressedBody)
		require.NoError(t, err)
	}))
	t.Cleanup(srv.Close)

	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, meterProvider.Shutdown(t.Context())) })
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	t.Cleanup(func() { require.NoError(t, traceProvider.Shutdown(t.Context())) })
	ctx, span := traceProvider.Tracer("test").Start(context.Background(), "llm")
	defer span.End()
	ctx = telemetry.WithMeterProvider(ctx, meterProvider)
	ctx, err = enginetelemetry.WithNetworkRecording(ctx)
	require.NoError(t, err)

	client := &http.Client{Transport: newLLMOTelTransport(nil, "test")}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, srv.URL, strings.NewReader(requestBody),
	)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	gotBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, "gzip", acceptEncoding)
	require.Equal(t, responseBody, string(gotBody))
	require.Less(t, len(compressedBody), len(responseBody))

	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &data))
	got := map[string]int64{}
	for _, current := range data.ScopeMetrics[0].Metrics {
		gauge := current.Data.(metricdata.Gauge[int64])
		got[current.Name] = gauge.DataPoints[0].Value
	}
	require.Equal(t, map[string]int64{
		telemetryattrs.NetworkEstimatedRxBytes: int64(len(compressedBody)),
		telemetryattrs.NetworkEstimatedTxBytes: int64(len(requestBody)),
	}, got)
}

// TestCaptureBodyTruncatesOnRuneBoundary: a body cut at the capture limit
// must stay valid UTF-8 whatever character straddles the limit, since the
// capture is logged as a string.
func TestCaptureBodyTruncatesOnRuneBoundary(t *testing.T) {
	for _, r := range []string{"é", "—", "🙂"} {
		for shift := range len(r) {
			body := strings.Repeat("a", maxBodyCapture-shift) + strings.Repeat(r, 4)
			captured, full, err := captureBody(io.NopCloser(strings.NewReader(body)))
			if err != nil {
				t.Fatal(err)
			}
			if string(full) != body {
				t.Fatalf("%q/%d: full body not preserved", r, shift)
			}
			if !utf8.ValidString(captured) {
				t.Fatalf("%q/%d: captured body is not valid UTF-8", r, shift)
			}
			if !strings.HasSuffix(captured, "\n... (truncated)") {
				t.Fatalf("%q/%d: captured body not marked truncated", r, shift)
			}
		}
	}
}

// TestLLMTransportSpanInternal locks in that LLM HTTP spans are marked
// internal: their stdio is the raw provider wire protocol (SSE event
// streams), which otherwise leaks into enclosing tool-call log captures
// (captureLogs skips subtrees beneath internal spans) and into the TUI.
// Failed requests un-hide themselves, since then the bodies are the
// diagnosis.
func TestLLMTransportSpanInternal(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		body         string
		wantInternal bool
	}{
		{name: "success stays hidden", status: 200, body: `{"ok":true}`, wantInternal: true},
		{name: "error is revealed", status: 500, body: `{"error":{"message":"boom"}}`, wantInternal: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			sr := tracetest.NewSpanRecorder()
			tp := sdktrace.NewTracerProvider(
				sdktrace.WithSampler(sdktrace.AlwaysSample()),
				sdktrace.WithSpanProcessor(sr),
			)
			ctx, root := tp.Tracer("llm-otel-test").Start(context.Background(), "root")
			defer root.End()

			client := &http.Client{Transport: newLLMOTelTransport(nil, "test")}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/messages",
				strings.NewReader(`{"model":"test"}`))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			span := otelprofSpanByName(t, sr.Ended(), "LLM HTTP POST /v1/messages")
			if got := otelprofAttrBool(span, telemetry.UIInternalAttr); got != tc.wantInternal {
				t.Errorf("%s = %v, want %v", telemetry.UIInternalAttr, got, tc.wantInternal)
			}
		})
	}
}

func TestIsStreamingResponse(t *testing.T) {
	for _, tc := range []struct {
		name       string
		accept     string
		reqBody    string
		respCT     string
		statusCode int
		want       bool
	}{
		{
			// OpenAI/Codex: streams via stream:true in the body, and its SSE
			// response does NOT carry a text/event-stream Content-Type. This is
			// the case that regressed — it must be detected as streaming.
			name:       "openai stream body, json response CT",
			reqBody:    `{"model":"gpt-5.5","stream":true,"input":[]}`,
			respCT:     "application/json",
			statusCode: 200,
			want:       true,
		},
		{
			name:       "anthropic accept header + event-stream CT",
			accept:     "text/event-stream",
			reqBody:    `{"model":"claude","stream":true}`,
			respCT:     "text/event-stream; charset=utf-8",
			statusCode: 200,
			want:       true,
		},
		{
			// Google streamGenerateContent: no stream:true in body, relies on CT.
			name:       "response CT fallback",
			reqBody:    `{"contents":[]}`,
			respCT:     "text/event-stream",
			statusCode: 200,
			want:       true,
		},
		{
			name:       "non-streaming request buffers",
			reqBody:    `{"model":"gpt-4.1","messages":[]}`,
			respCT:     "application/json",
			statusCode: 200,
			want:       false,
		},
		{
			// A streaming request that errors returns a JSON error body, which we
			// must buffer (not tee) so the detail can be parsed.
			name:       "streaming request that errors is buffered",
			reqBody:    `{"model":"gpt-5.5","stream":true}`,
			respCT:     "application/json",
			statusCode: 400,
			want:       false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := isStreamingResponse(tc.accept, []byte(tc.reqBody), tc.respCT, tc.statusCode)
			if got != tc.want {
				t.Errorf("isStreamingResponse(%q, %q, %q, %d) = %v, want %v",
					tc.accept, tc.reqBody, tc.respCT, tc.statusCode, got, tc.want)
			}
		})
	}
}
