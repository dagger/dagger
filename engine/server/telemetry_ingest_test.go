package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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

// postClientOTLP posts msg as the client itself, over its session connection.
func postClientOTLP(t *testing.T, srv *Server, record *clientRecord, path string, msg proto.Message) *httptest.ResponseRecorder {
	t.Helper()
	body, err := proto.Marshal(msg)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	resp := httptest.NewRecorder()
	ps := srv.telemetryPubSub
	ingest := map[string]func(*http.Request, *clientRecord) error{
		"/v1/traces":  ps.ingestClientTraces,
		"/v1/logs":    ps.ingestClientLogs,
		"/v1/metrics": ps.ingestClientMetrics,
	}[path]
	ps.clientIngestHandler(ingest, record)(resp, req)
	return resp
}

func ingestTestSpans(name string, id byte) *coltracepb.ExportTraceServiceRequest {
	return &coltracepb.ExportTraceServiceRequest{ResourceSpans: telemetry.SpansToPB([]sdktrace.ReadOnlySpan{
		tracetest.SpanStub{
			Name: name,
			SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: trace.TraceID{7}, SpanID: trace.SpanID{id}, TraceFlags: trace.FlagsSampled,
			}),
			StartTime: time.Now(),
			EndTime:   time.Now(),
		}.Snapshot(),
	})}
}

func ingestTestLogs(marker string) *collogspb.ExportLogsServiceRequest {
	return &collogspb.ExportLogsServiceRequest{ResourceLogs: []*otlplogsv1.ResourceLogs{{
		ScopeLogs: []*otlplogsv1.ScopeLogs{{LogRecords: []*otlplogsv1.LogRecord{{
			TimeUnixNano: uint64(time.Now().UnixNano()),
			TraceId:      []byte{7, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			SpanId:       []byte{7, 0, 0, 0, 0, 0, 0, 0},
			Body:         &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: marker}},
		}}}},
	}}}
}

func ingestTestMetrics(name string) *colmetricspb.ExportMetricsServiceRequest {
	return &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: []*otlpmetricsv1.ResourceMetrics{{
		ScopeMetrics: []*otlpmetricsv1.ScopeMetrics{{Metrics: []*otlpmetricsv1.Metric{{
			Name: name,
			Data: &otlpmetricsv1.Metric_Gauge{Gauge: &otlpmetricsv1.Gauge{DataPoints: []*otlpmetricsv1.NumberDataPoint{{
				TimeUnixNano: uint64(time.Now().UnixNano()),
				Value:        &otlpmetricsv1.NumberDataPoint_AsInt{AsInt: 1},
			}}}},
		}}}},
	}}}
}

// A client's own telemetry lands in its store, marked as ingested from it,
// next to what its containers post; the client's streams carry only the
// latter, since the client has its own already.
func TestClientTelemetryIngest(t *testing.T) {
	srv, sess := otlpHandlerFixture(t)
	record := sess.clientRecords["client"]

	for path, msg := range map[string]proto.Message{
		"/v1/traces":  ingestTestSpans("client-span", 1),
		"/v1/logs":    ingestTestLogs("client-log"),
		"/v1/metrics": ingestTestMetrics("client.metric"),
	} {
		resp := postClientOTLP(t, srv, record, path, msg)
		require.Equal(t, http.StatusCreated, resp.Code, "%s: %s", path, resp.Body.String())
	}
	for path, msg := range map[string]proto.Message{
		"/v1/traces":  ingestTestSpans("exec-span", 2),
		"/v1/logs":    ingestTestLogs("exec-log"),
		"/v1/metrics": ingestTestMetrics("exec.metric"),
	} {
		resp := postOTLP(t, srv, sess, path, msg)
		require.Equal(t, http.StatusCreated, resp.Code, "%s: %s", path, resp.Body.String())
	}

	db, err := srv.clientDBs.Open(t.Context(), "client")
	require.NoError(t, err)
	defer db.Close()

	_, all, rows, err := fetchSpanBatch(t.Context(), db, 0, 100, nil)
	require.NoError(t, err)
	require.Equal(t, 2, rows)
	require.ElementsMatch(t, []string{"client-span", "exec-span"}, spanNames(all))
	_, streamed, _, err := fetchSpanBatch(t.Context(), db, 0, 100, streamedSpanRow("client"))
	require.NoError(t, err)
	require.Equal(t, []string{"exec-span"}, spanNames(streamed))
	_, streamedElsewhere, _, err := fetchSpanBatch(t.Context(), db, 0, 100, streamedSpanRow("other"))
	require.NoError(t, err)
	require.Len(t, spanNames(streamedElsewhere), 2, "only the ingesting client's own streams leave it out")

	_, logs, _, err := fetchLogBatch(t.Context(), db, 0, 100, streamedLogRow("client"))
	require.NoError(t, err)
	require.Equal(t, []string{"exec-log"}, logBodies(logs))
	_, logs, _, err = fetchLogBatch(t.Context(), db, 0, 100, nil)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"client-log", "exec-log"}, logBodies(logs))

	metrics, err := db.Read().SelectMetricsSince(t.Context(), clientdb.SelectMetricsSinceParams{Limit: 100})
	require.NoError(t, err)
	require.Len(t, metrics, 2)
	var streamedMetrics []string
	keep := streamedMetricRow("client")
	for i := range metrics {
		if keep(&metrics[i]) {
			streamedMetrics = append(streamedMetrics, metricNames(clientdb.MetricsToPB(metrics[i:i+1]))...)
		}
	}
	require.Equal(t, []string{"exec.metric"}, streamedMetrics)
}

// A client cannot pass its records off as another client's, or as not
// ingested: the engine stamps the marker itself.
func TestClientTelemetryIngestOverridesMarker(t *testing.T) {
	srv, sess := otlpHandlerFixture(t)
	record := sess.clientRecords["client"]

	req := ingestTestSpans("forged", 1)
	req.ResourceSpans[0].Resource = withClientIngested(nil, "someone-else")
	resp := postClientOTLP(t, srv, record, "/v1/traces", req)
	require.Equal(t, http.StatusCreated, resp.Code, resp.Body.String())

	spans, _ := clientRows(t, srv.clientDBs, "client")
	require.Len(t, spans, 1)
	require.True(t, ingestedResourceRow(spans[0].Resource, "client"))
	require.False(t, ingestedResourceRow(spans[0].Resource, "someone-else"))
}

func TestClientTelemetryIngestRejectsGarbage(t *testing.T) {
	srv, sess := otlpHandlerFixture(t)
	ps := srv.telemetryPubSub
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader([]byte("not protobuf")))
	resp := httptest.NewRecorder()
	ps.clientIngestHandler(ps.ingestClientTraces, sess.clientRecords["client"])(resp, req)
	require.Equal(t, http.StatusBadRequest, resp.Code)
}

func spanNames(req *coltracepb.ExportTraceServiceRequest) []string {
	var names []string
	for _, rs := range req.GetResourceSpans() {
		for _, ss := range rs.GetScopeSpans() {
			for _, span := range ss.GetSpans() {
				names = append(names, span.GetName())
			}
		}
	}
	return names
}

func logBodies(req *collogspb.ExportLogsServiceRequest) []string {
	var bodies []string
	for _, rl := range req.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			for _, rec := range sl.GetLogRecords() {
				bodies = append(bodies, rec.GetBody().GetStringValue())
			}
		}
	}
	return bodies
}

func metricNames(rms []*otlpmetricsv1.ResourceMetrics) []string {
	var names []string
	for _, rm := range rms {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				names = append(names, m.GetName())
			}
		}
	}
	return names
}
