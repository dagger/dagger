package client

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine"
	enginetel "github.com/dagger/dagger/engine/telemetry"
)

// The trace stream's first response says whether the engine stores the
// client's own telemetry; only then does the client post it, over its
// session connection. A nested client never does: its process posts its
// telemetry through its exec's OTLP endpoint already.
func TestClientTelemetryIngestFollowsStream(t *testing.T) {
	for _, tc := range []struct {
		name    string
		header  bool
		nested  bool
		ingests bool
	}{
		{name: "engine stores client telemetry", header: true, ingests: true},
		{name: "older engine"},
		{name: "nested client", header: true, nested: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()

			var stream bytes.Buffer
			require.NoError(t, enginetel.WriteLiveHello(&stream, 0))
			require.NoError(t, enginetel.WriteLiveTerminal(&stream, 0))

			var posted []*coltracepb.ExportTraceServiceRequest
			hc := &httpClient{inner: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				header := http.Header{}
				if req.Method == http.MethodPost {
					require.Equal(t, "/v1/traces", req.URL.Path)
					body, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					var msg coltracepb.ExportTraceServiceRequest
					require.NoError(t, proto.Unmarshal(body, &msg))
					posted = append(posted, &msg)
					return &http.Response{StatusCode: http.StatusCreated, Header: header, Body: io.NopCloser(&bytes.Buffer{}), Request: req}, nil
				}
				header.Set("Content-Type", enginetel.LiveContentType)
				if tc.header {
					header.Set(engine.ClientTelemetryIngestHeader, engine.ClientTelemetryIngestStore)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(&stream), Request: req}, nil
			})}}

			c := &Client{
				Params:      Params{EngineTrace: tracetest.NewInMemoryExporter()},
				internalCtx: ctx,
				telemetry:   new(errgroup.Group),
				httpClient:  hc,
			}
			if tc.nested {
				c.nestedSessionPort = 1234
			}
			require.NoError(t, c.exportTraces(ctx, hc))
			require.NoError(t, c.telemetry.Wait())
			require.Equal(t, tc.ingests, c.IngestsTelemetry())

			span := tracetest.SpanStub{
				Name: "cli-span",
				SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
					TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled,
				}),
				StartTime: time.Now(),
				EndTime:   time.Now(),
			}.Snapshot()
			err := c.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span})
			if !tc.ingests {
				require.Error(t, err)
				require.Empty(t, posted)
				return
			}
			require.NoError(t, err)
			require.Len(t, posted, 1)
			require.Equal(t, "cli-span", posted[0].GetResourceSpans()[0].GetScopeSpans()[0].GetSpans()[0].GetName())
		})
	}
}
