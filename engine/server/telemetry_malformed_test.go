package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	otlpmetricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine/clientdb"
	telemetry "github.com/dagger/otel-go"
)

// otlpHandlerFixture is the narrowest session the engine's OTLP handlers
// serve: one initialized session with one client, wired to the real session
// span and log exporters and a temp-dir client DB store.
func otlpHandlerFixture(t *testing.T) (*Server, *daggerSession) {
	t.Helper()
	srv := &Server{clientDBs: clientdb.NewDBs(t.TempDir())}
	srv.telemetryPubSub = NewPubSub(srv)
	sess := &daggerSession{sessionID: "session", clientRecords: map[string]*clientRecord{}}
	sess.telemetryPubSub = srv.telemetryPubSub
	sess.clientRecords["client"] = &clientRecord{daggerSession: sess, clientID: "client"}
	sess.spanExporter = sessionSpanExporter{sess: sess, ps: srv.telemetryPubSub}
	sess.logExporter = sessionLogExporter{sess: sess, ps: srv.telemetryPubSub}
	sess.state.Store(sessionStateInitialized)
	srv.daggerSessions = map[string]*daggerSession{sess.sessionID: sess}
	return srv, sess
}

// postOTLP posts msg to the engine's OTLP handler at path, as a process in
// one of the client's containers does through its per-exec endpoint.
func postOTLP(t *testing.T, srv *Server, sess *daggerSession, path string, msg proto.Message) *httptest.ResponseRecorder {
	t.Helper()
	body, err := proto.Marshal(msg)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("X-Dagger-Session-ID", sess.sessionID)
	req.Header.Set("X-Dagger-Client-ID", "client")
	resp := httptest.NewRecorder()
	srv.telemetryPubSub.ServeHTTP(resp, req)
	return resp
}

func valuelessAttr(key string) *otlpcommonv1.KeyValue {
	return &otlpcommonv1.KeyValue{Key: key}
}

func stringAttr(key, value string) *otlpcommonv1.KeyValue {
	return &otlpcommonv1.KeyValue{Key: key, Value: &otlpcommonv1.AnyValue{
		Value: &otlpcommonv1.AnyValue_StringValue{StringValue: value},
	}}
}

// A span with no Resource is legal OTLP, and is what a tracetest.SpanStub
// snapshot exports. It used to crash the whole engine: span conversion is
// lazy, so the nil dereference fired in the span fan-out goroutine.
func TestTracesHandlerToleratesMissingResource(t *testing.T) {
	srv, sess := otlpHandlerFixture(t)

	resourceless := func(name string, id byte) *coltracepb.ExportTraceServiceRequest {
		stub := tracetest.SpanStub{
			Name: name,
			SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: trace.TraceID{9}, SpanID: trace.SpanID{id}, TraceFlags: trace.FlagsSampled,
			}),
			StartTime: time.Now(),
			EndTime:   time.Now(),
		}.Snapshot()
		req := &coltracepb.ExportTraceServiceRequest{ResourceSpans: telemetry.SpansToPB([]sdktrace.ReadOnlySpan{stub})}
		require.Len(t, req.ResourceSpans, 1)
		require.Nil(t, req.ResourceSpans[0].Resource, "the regression needs a resourceless payload")
		return req
	}

	// Exactly what the user's `go test` exported.
	resp := postOTLP(t, srv, sess, "/v1/traces", resourceless("resourceless-span", 1))
	require.Equal(t, http.StatusCreated, resp.Code, resp.Body.String())

	// A valueless attribute panics the same conversion, in the handler.
	req := resourceless("valueless-attr-span", 2)
	span := req.ResourceSpans[0].ScopeSpans[0].Spans[0]
	span.Attributes = append(span.Attributes, valuelessAttr("valueless"))
	resp = postOTLP(t, srv, sess, "/v1/traces", req)
	require.Equal(t, http.StatusCreated, resp.Code, resp.Body.String())

	spans, _ := clientRows(t, srv.clientDBs, "client")
	var names []string
	for _, span := range spans {
		names = append(names, span.Name)
	}
	require.Equal(t, []string{"resourceless-span", "valueless-attr-span"}, names)
}

// Logs with no Resource, no Body, or a valueless attribute are legal OTLP
// too, and are stored rather than panicking the handler.
func TestLogsHandlerToleratesMissingResourceAndBody(t *testing.T) {
	srv, sess := otlpHandlerFixture(t)

	resp := postOTLP(t, srv, sess, "/v1/logs", &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*otlplogsv1.ResourceLogs{{
			// No Resource.
			ScopeLogs: []*otlplogsv1.ScopeLogs{{LogRecords: []*otlplogsv1.LogRecord{{
				TimeUnixNano: uint64(time.Now().UnixNano()),
				TraceId:      []byte{9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
				SpanId:       []byte{9, 0, 0, 0, 0, 0, 0, 0},
				// No Body.
				Attributes: []*otlpcommonv1.KeyValue{
					stringAttr("marker", "bodiless-log"),
					valuelessAttr("valueless"),
				},
			}}}},
		}},
	})
	require.Equal(t, http.StatusCreated, resp.Code, resp.Body.String())

	_, logs := clientRows(t, srv.clientDBs, "client")
	require.Len(t, logs, 1)
	require.Contains(t, string(logs[0].Attributes), "bodiless-log")
}

// Metrics with no Resource and a valueless data point attribute are stored.
func TestMetricsHandlerToleratesMissingResource(t *testing.T) {
	srv, sess := otlpHandlerFixture(t)

	resp := postOTLP(t, srv, sess, "/v1/metrics", &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*otlpmetricsv1.ResourceMetrics{{
			// No Resource.
			ScopeMetrics: []*otlpmetricsv1.ScopeMetrics{{Metrics: []*otlpmetricsv1.Metric{{
				Name: "resourceless.metric",
				Data: &otlpmetricsv1.Metric_Gauge{Gauge: &otlpmetricsv1.Gauge{DataPoints: []*otlpmetricsv1.NumberDataPoint{{
					TimeUnixNano: uint64(time.Now().UnixNano()),
					Attributes:   []*otlpcommonv1.KeyValue{valuelessAttr("valueless")},
					Value:        &otlpmetricsv1.NumberDataPoint_AsInt{AsInt: 1},
				}}}},
			}}}},
		}},
	})
	require.Equal(t, http.StatusCreated, resp.Code, resp.Body.String())

	db, err := srv.clientDBs.Open(t.Context(), "client")
	require.NoError(t, err)
	defer db.Close()
	rows, err := db.Read().SelectMetricsSince(t.Context(), clientdb.SelectMetricsSinceParams{Limit: 100})
	require.NoError(t, err)
	var names []string
	for _, rm := range clientdb.MetricsToPB(rows) {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				names = append(names, m.GetName())
			}
		}
	}
	require.Equal(t, []string{"resourceless.metric"}, names)
}

// panickySpan is a span whose lazily decoded fields blow up, standing in for
// whatever conversion bug the normalization does not anticipate.
type panickySpan struct {
	sdktrace.ReadOnlySpan
}

func (panickySpan) Resource() *resource.Resource { panic("lazy decode exploded") }

// The span fan-out runs on its own goroutines, outside the HTTP handler's
// recovery, so a panic there must fail the batch rather than the process.
func TestSessionSpanExporterRecoversFromPanickingSpan(t *testing.T) {
	_, sess := otlpHandlerFixture(t)
	span := tracetest.SpanStub{
		Name: "panicky",
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: trace.TraceID{9}, SpanID: trace.SpanID{9},
		}),
		StartTime: time.Now(),
		EndTime:   time.Now(),
		Resource:  resource.NewSchemaless(attribute.String("service.name", "test")),
	}.Snapshot()
	exp := sess.postedSpanExporter("client")
	err := exp.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{panickySpan{span}})
	require.ErrorContains(t, err, "lazy decode exploded")
}
