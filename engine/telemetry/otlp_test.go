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
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestOTLPDestinationSnapshotsDoNotWaitForReceiver(t *testing.T) {
	arrived := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var mu sync.Mutex
	var requests []*coltracepb.ExportTraceServiceRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/collector/v1/traces" || r.Header.Get("X-Destination") != "independent" {
			t.Errorf("unexpected destination: %s", r.URL.Path)
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

	destination, err := NewOTLPDestination(t.Context(), OTLPOptions{Endpoint: server.URL + "/collector", Headers: map[string]string{"X-Destination": "independent"}})
	require.NoError(t, err)
	t.Cleanup(func() {
		unblock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, destination.Shutdown(ctx))
	})

	// The ordinary export path must keep delivering completed spans while the
	// additional destination is blocked, not only allow Span.End to return.
	delivered := make(chan string, 3)
	healthy := httptest.NewServer(receiveCompletedSpans(t, delivered))
	t.Cleanup(healthy.Close)
	healthyExporter, err := otlptracehttp.New(t.Context(), otlptracehttp.WithEndpointURL(healthy.URL))
	require.NoError(t, err)
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(destination.SpanProcessor("session")),
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
	workCtx, span := start(t.Context(), "work")
	workID := span.SpanContext().SpanID()
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("no start snapshot")
	}
	finished := make(chan error, 1)
	go func() {
		omittedCtx, omitted := provider.Tracer("unselected.scope").Start(workCtx, "detail",
			trace.WithAttributes(attribute.String(telemetry.DagCallAttr, "call payload")))
		// Lazy work can start after its immediate, omitted parent has ended.
		omitted.End()
		_, short := start(omittedCtx, "short")
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
		t.Fatal("session shutdown waited for the independent destination")
	}
	var completed []string
	for range 3 {
		select {
		case name := <-delivered:
			completed = append(completed, name)
		case <-time.After(5 * time.Second):
			t.Fatal("healthy destination waited for the blocked destination")
		}
	}
	require.ElementsMatch(t, []string{"work", "short", "detail"}, completed)
	unblock()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	require.NoError(t, destination.Shutdown(ctx))

	mu.Lock()
	defer mu.Unlock()
	starts, completions := map[string]int{}, map[string]int{}
	identities := map[string][]byte{}
	for _, req := range requests {
		for _, res := range req.ResourceSpans {
			for _, scope := range res.ScopeSpans {
				require.Empty(t, scope.Scope.Attributes, "unselected scope data must not enter the additional queue")
				for _, span := range scope.Spans {
					if span.Name == "short" {
						require.Equal(t, workID[:], span.ParentSpanId, "omitted parent must not break the operation link")
					}
					if identities[span.Name] == nil {
						identities[span.Name] = span.SpanId
					}
					require.Equal(t, identities[span.Name], span.SpanId)
					preferred := ""
					for _, kv := range span.Attributes {
						require.NotEqual(t, telemetry.DagCallAttr, kv.Key, "call payload must stay out of the additional destination")
						if kv.Key == telemetryattrs.DagContentPreferredDigestAttr {
							preferred = kv.Value.GetStringValue()
						}
					}
					if span.EndTimeUnixNano < span.StartTimeUnixNano {
						starts[span.Name]++
						require.Empty(t, preferred, "completion must not mutate the queued start")
					} else {
						completions[span.Name]++
						require.Equal(t, "preferred-"+span.Name, preferred)
					}
				}
			}
		}
	}
	require.Equal(t, map[string]int{"work": 1, "short": 1}, starts)
	require.Equal(t, starts, completions)
}

func TestOperationLinksSurviveDeferredChildren(t *testing.T) {
	for _, fillAliases := range []bool{false, true} {
		t.Run(map[bool]string{false: "parent alias", true: "parent link after alias limit"}[fillAliases], func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			processor := &operationSpanProcessor{next: recorder, resource: resource.Empty()}
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(processor))
			tracer := provider.Tracer("dagger.io/core")
			rootCtx, root := tracer.Start(t.Context(), "root", trace.WithAttributes(attribute.String(telemetry.DagDigestAttr, "root-recipe")))
			if fillAliases {
				for range maxEndedOperationParents {
					_, detail := tracer.Start(rootCtx, "omitted detail")
					detail.End()
				}
			}
			deferredCtx, parent := tracer.Start(rootCtx, "omitted producer")
			parent.End()
			_, child := tracer.Start(deferredCtx, "child", trace.WithAttributes(attribute.String(telemetry.DagDigestAttr, "child-recipe")))
			child.End()
			root.End()
			require.NoError(t, provider.Shutdown(t.Context()))

			completed := map[trace.SpanID]sdktrace.ReadOnlySpan{}
			for _, span := range recorder.Ended() {
				if !span.EndTime().Before(span.StartTime()) {
					completed[span.SpanContext().SpanID()] = span
				}
			}
			id := child.SpanContext().SpanID()
			visited := map[trace.SpanID]bool{}
			for id != root.SpanContext().SpanID() {
				require.False(t, visited[id], "operation parent graph contains a cycle")
				visited[id] = true
				span, ok := completed[id]
				require.True(t, ok, "deferred child lost its operation parent")
				id = span.Parent().SpanID()
			}
		})
	}
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
							t.Error("additional destination changed the ordinary span payload")
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
