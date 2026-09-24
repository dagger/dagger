package client

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine"
	enginetel "github.com/dagger/dagger/engine/telemetry"
)

func liveSpanFrame(t *testing.T, w io.Writer, cursor int64, name string) {
	t.Helper()
	span := tracetest.SpanStub{
		Name: name,
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: trace.TraceID{1}, SpanID: trace.SpanID{byte(cursor)}, TraceFlags: trace.FlagsSampled,
		}),
		StartTime: time.Now(),
		EndTime:   time.Now(),
		Resource:  resource.NewSchemaless(attribute.String("service.name", "dagger-engine")),
	}.Snapshot()
	payload, err := proto.Marshal(&coltracepb.ExportTraceServiceRequest{
		ResourceSpans: telemetry.SpansToPB([]sdktrace.ReadOnlySpan{span}),
	})
	require.NoError(t, err)
	require.NoError(t, enginetel.WriteLiveFrame(w, cursor, payload))
}

// The engine's confirmation on a stream's first response picks where the
// stream's spans go for the whole stream: without this client's Cloud
// exporters only when the client asked the engine to publish and the engine
// confirmed. A reconnect that answers differently changes nothing.
func TestEngineTelemetryForwardingFollowsConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		asked      bool
		confirmed  bool
		wantDirect bool
	}{
		{name: "asked and confirmed", asked: true, confirmed: true, wantDirect: true},
		{name: "asked, older engine", asked: true},
		{name: "confirmed without asking", confirmed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()

			var first, second bytes.Buffer
			require.NoError(t, enginetel.WriteLiveHello(&first, 0))
			liveSpanFrame(t, &first, 1, "before-reconnect")
			// The first response ends without a terminal frame: the client
			// reconnects from cursor 1.
			require.NoError(t, enginetel.WriteLiveHello(&second, 1))
			liveSpanFrame(t, &second, 2, "after-reconnect")
			require.NoError(t, enginetel.WriteLiveTerminal(&second, 2))

			requests := 0
			hc := &httpClient{inner: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				header := http.Header{"Content-Type": {enginetel.LiveContentType}}
				body := &first
				confirm := tc.confirmed
				if requests > 1 {
					body, confirm = &second, !tc.confirmed // a reconnect that disagrees
				}
				if confirm {
					header.Set(engine.CloudTelemetryPublisherHeader, engine.CloudTelemetryPublisherEngine)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(body), Request: req}, nil
			})}}

			withCloud := tracetest.NewInMemoryExporter()
			withoutCloud := tracetest.NewInMemoryExporter()
			c := &Client{Params: Params{
				EngineTrace:             withCloud,
				EngineTraceWithoutCloud: withoutCloud,
				EngineCloudTelemetry:    tc.asked,
			}, internalCtx: ctx, telemetry: new(errgroup.Group)}
			require.NoError(t, c.exportTraces(ctx, hc))
			require.NoError(t, c.telemetry.Wait())
			require.Equal(t, 2, requests, "one reconnect")

			names := func(exp *tracetest.InMemoryExporter) []string {
				var out []string
				for _, span := range exp.GetSpans() {
					out = append(out, span.Name)
				}
				return out
			}
			want := []string{"before-reconnect", "after-reconnect"}
			if tc.wantDirect {
				require.Equal(t, want, names(withoutCloud))
				require.Empty(t, names(withCloud))
			} else {
				require.Equal(t, want, names(withCloud))
				require.Empty(t, names(withoutCloud))
			}
		})
	}
}
