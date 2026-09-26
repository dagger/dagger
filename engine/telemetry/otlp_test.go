package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	collogpb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
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
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() { unblock(); server.Close() })

	destination, err := NewOTLPDestination(t.Context(), OTLPOptions{Endpoint: server.URL + "/collector", Headers: map[string]string{"X-Destination": "independent"}, Signals: []string{"traces"}})
	require.NoError(t, err)
	t.Cleanup(func() {
		unblock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, destination.Shutdown(ctx))
	})
	require.Nil(t, destination.LogExporter(), "logs require explicit selection")

	// The ordinary export path must keep delivering completed spans while the
	// additional destination is blocked, not only allow Span.End to return.
	delivered := make(chan string, 2)
	healthy := httptest.NewServer(receiveCompletedSpans(t, delivered))
	t.Cleanup(healthy.Close)
	healthyExporter, err := otlptracehttp.New(t.Context(), otlptracehttp.WithEndpointURL(healthy.URL))
	require.NoError(t, err)
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(destination.SpanProcessor()),
		sdktrace.WithSpanProcessor(NewLargeQueueLiveSpanProcessor(healthyExporter)),
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, provider.Shutdown(ctx))
	})
	_, span := provider.Tracer("example", trace.WithSchemaURL("https://example.test/schema"), trace.WithInstrumentationAttributes(attribute.String("library", "example"))).Start(t.Context(), "work", trace.WithAttributes(attribute.String("state", "started")))
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("no start snapshot")
	}
	finished := make(chan error, 1)
	go func() {
		_, short := provider.Tracer("example", trace.WithSchemaURL("https://example.test/schema"), trace.WithInstrumentationAttributes(attribute.String("library", "example"))).Start(t.Context(), "short", trace.WithAttributes(attribute.String("state", "started")))
		short.SetAttributes(attribute.String("state", "finished"))
		short.End()
		span.SetAttributes(attribute.String("state", "finished"))
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
	for range 2 {
		select {
		case name := <-delivered:
			completed = append(completed, name)
		case <-time.After(5 * time.Second):
			t.Fatal("healthy destination waited for the blocked destination")
		}
	}
	require.ElementsMatch(t, []string{"work", "short"}, completed)
	unblock()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	require.NoError(t, destination.Shutdown(ctx))

	mu.Lock()
	defer mu.Unlock()
	require.GreaterOrEqual(t, len(requests), 3, "start retry and completion must arrive")
	require.True(t, proto.Equal(requests[0], requests[1]), "transport retry changed its payload")
	starts, completions := map[string]int{}, map[string]int{}
	identities := map[string][]byte{}
	for _, req := range requests[1:] {
		for _, res := range req.ResourceSpans {
			for _, scope := range res.ScopeSpans {
				require.Equal(t, "example", scope.Scope.Name)
				require.Equal(t, "https://example.test/schema", scope.SchemaUrl)
				require.Len(t, scope.Scope.Attributes, 1)
				require.Equal(t, "example", scope.Scope.Attributes[0].Value.GetStringValue())
				for _, span := range scope.Spans {
					if identities[span.Name] == nil {
						identities[span.Name] = span.SpanId
					}
					require.Equal(t, identities[span.Name], span.SpanId)
					state := ""
					for _, kv := range span.Attributes {
						if kv.Key == "state" {
							state = kv.Value.GetStringValue()
						}
					}
					if span.EndTimeUnixNano < span.StartTimeUnixNano {
						starts[span.Name]++
						require.Equal(t, "started", state)
					} else {
						completions[span.Name]++
						require.Equal(t, "finished", state)
					}
				}
			}
		}
	}
	require.Equal(t, map[string]int{"work": 1, "short": 1}, starts)
	require.Equal(t, starts, completions)
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

func TestOTLPDestinationExportsExplicitlySelectedLogs(t *testing.T) {
	received := make(chan *collogpb.ExportLogsServiceRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/logs" {
			t.Errorf("unexpected signal: %s", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var req collogpb.ExportLogsServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			t.Error(err)
			return
		}
		received <- &req
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer server.Close()
	destination, err := NewOTLPDestination(t.Context(), OTLPOptions{Endpoint: server.URL, Signals: []string{"logs"}})
	require.NoError(t, err)
	require.Nil(t, destination.SpanExporter())
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(destination.LogProcessor()))
	var record log.Record
	record.SetBody(log.StringValue("explicit log export"))
	record.SetTimestamp(time.Now())
	provider.Logger("example").Emit(t.Context(), record)
	require.NoError(t, provider.Shutdown(t.Context()))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, destination.Shutdown(ctx))
	select {
	case req := <-received:
		require.Equal(t, "explicit log export", req.ResourceLogs[0].ScopeLogs[0].LogRecords[0].Body.GetStringValue())
	case <-ctx.Done():
		t.Fatal("selected log did not arrive")
	}
}
