package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestWorkloadExportDoesNotWaitForReceiver(t *testing.T) {
	arrived := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var mu sync.Mutex
	var requests []*coltracepb.ExportTraceServiceRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/collector/v1/traces" || r.Header.Get("X-Destination") != "independent" {
			t.Errorf("unexpected export: %s", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var req coltracepb.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		requests = append(requests, &req)
		first := len(requests) == 1
		mu.Unlock()
		if first {
			close(arrived)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() { unblock(); server.Close() })

	for _, signal := range []string{"TRACES", "METRICS"} {
		for _, setting := range []string{"ENDPOINT", "PROTOCOL", "HEADERS"} {
			t.Setenv("OTEL_EXPORTER_OTLP_"+signal+"_"+setting, "")
		}
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL+"/collector")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "X-Destination=independent")
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "")
	export, err := NewWorkloadExport(t.Context(), "test-engine")
	require.NoError(t, err)
	t.Cleanup(func() {
		unblock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, export.Shutdown(ctx))
	})

	// The ordinary export path must keep delivering completed spans while the
	// workload export is blocked, not only allow Span.End to return.
	delivered := make(chan string, 4)
	healthy := httptest.NewServer(receiveCompletedSpans(t, delivered))
	t.Cleanup(healthy.Close)
	healthyExporter, err := otlptracehttp.New(t.Context(), otlptracehttp.WithEndpointURL(healthy.URL))
	require.NoError(t, err)
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(export.SpanProcessor("session")),
		sdktrace.WithSpanProcessor(NewLargeQueueLiveSpanProcessor(healthyExporter)),
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, provider.Shutdown(ctx))
	})
	tracer := provider.Tracer("dagger.io/core", trace.WithInstrumentationAttributes(attribute.String("unselected", "scope detail")))
	start := func(ctx context.Context, name string) (context.Context, trace.Span) {
		return tracer.Start(ctx, name, trace.WithAttributes(
			attribute.String(telemetry.DagDigestAttr, "recipe-"+name),
			attribute.String(telemetry.DagCallAttr, "call payload"),
		))
	}
	// The first ended span blocks the receiver; later spans queue behind it.
	_, first := start(t.Context(), "first")
	first.SetAttributes(attribute.String(telemetryattrs.DagContentPreferredDigestAttr, "preferred-first"))
	first.End()
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("no export")
	}
	workCtx, span := start(t.Context(), "work")
	var detailID trace.SpanID
	finished := make(chan error, 1)
	go func() {
		detailCtx, detail := provider.Tracer("other.scope").Start(workCtx, "detail",
			trace.WithAttributes(attribute.String(telemetry.DagCallAttr, "call payload")))
		detailID = detail.SpanContext().SpanID()
		// Lazy work can start after its parent has ended.
		detail.End()
		_, short := start(detailCtx, "short")
		short.SetAttributes(attribute.String(telemetryattrs.DagContentPreferredDigestAttr, "preferred-short"))
		short.End()
		span.SetAttributes(attribute.String(telemetryattrs.DagContentPreferredDigestAttr, "preferred-work"))
		span.End()
		finished <- provider.Shutdown(context.Background())
	}()
	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("session shutdown waited for the workload export")
	}
	var completed []string
	for range 4 {
		select {
		case name := <-delivered:
			completed = append(completed, name)
		case <-time.After(5 * time.Second):
			t.Fatal("ordinary export waited for the blocked workload export")
		}
	}
	require.ElementsMatch(t, []string{"first", "work", "short", "detail"}, completed)
	unblock()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	require.NoError(t, export.Shutdown(ctx))

	mu.Lock()
	defer mu.Unlock()
	sent := map[string]int{}
	for _, req := range requests {
		for _, res := range req.ResourceSpans {
			for _, scope := range res.ScopeSpans {
				require.Empty(t, scope.Scope.Attributes, "scope attributes must not enter the workload export")
				for _, span := range scope.Spans {
					sent[span.Name]++
					require.GreaterOrEqual(t, span.EndTimeUnixNano, span.StartTimeUnixNano, "only ended spans are sent")
					if span.Name == "short" {
						require.Equal(t, detailID[:], span.ParentSpanId, "spans keep their real parent")
					}
					preferred := ""
					for _, kv := range span.Attributes {
						require.NotEqual(t, telemetry.DagCallAttr, kv.Key, "call payload must stay out of the workload export")
						if kv.Key == telemetryattrs.DagContentPreferredDigestAttr {
							preferred = kv.Value.GetStringValue()
						}
					}
					if span.Name != "detail" {
						require.Equal(t, "preferred-"+span.Name, preferred)
					}
				}
			}
		}
	}
	require.Equal(t, map[string]int{"first": 1, "work": 1, "short": 1, "detail": 1}, sent)
}

func receiveCompletedSpans(t *testing.T, delivered chan<- string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t.Helper()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var req coltracepb.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for _, res := range req.ResourceSpans {
			for _, scope := range res.ScopeSpans {
				for _, span := range scope.Spans {
					if span.EndTimeUnixNano >= span.StartTimeUnixNano {
						var payload string
						for _, attr := range span.Attributes {
							if attr.Key == telemetry.DagCallAttr {
								payload = attr.Value.GetStringValue()
							}
						}
						if payload != "call payload" {
							t.Error("workload export changed the ordinary span payload")
						}
						select {
						case delivered <- span.Name:
						case <-r.Context().Done():
							return
						}
					}
				}
			}
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
	}
}
