package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/clientdb"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"github.com/vito/go-sse/sse"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	colLogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colMetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	colTraces "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Use the wire key so this regression can run against the implementation before
// the reflection marker was introduced.
const reflectionSessionAttr = "dagger.io/telemetry.session_id"

func TestTelemetrySubscriptionDoesNotReflectIntoParent(t *testing.T) {
	for _, signal := range []string{"traces", "logs", "metrics"} {
		for _, binary := range []bool{false, true} {
			transport := "sse"
			if binary {
				transport = "binary"
			}
			t.Run(signal+"/"+transport, func(t *testing.T) {
				dbs := clientdb.NewDBs(t.TempDir())
				srv := &Server{clientDBs: dbs, wcprofSpanCount: newWcprofSpanCounter()}
				ps := NewPubSub(srv)
				sess := &daggerSession{sessionID: "session", telemetryPubSub: ps, clientRecords: map[string]*clientRecord{}}
				sess.state.Store(sessionStateInitialized)
				srv.daggerSessions = map[string]*daggerSession{sess.sessionID: sess}
				sess.spanExporter = sessionSpanExporter{sess: sess, ps: ps}
				sess.logExporter = sessionLogExporter{sess: sess, ps: ps}
				for _, id := range []string{"parent", "child"} {
					shutdown := make(chan struct{})
					close(shutdown) // Read the complete subscription, then drain it.
					sess.clientRecords[id] = &clientRecord{daggerSession: sess, clientID: id, shutdownCh: shutdown}
				}
				sess.clientRecords["child"].parentClientIDs = []string{"parent"}

				post := func(client string, batch proto.Message) {
					t.Helper()
					body, err := proto.Marshal(batch)
					require.NoError(t, err)
					req := httptest.NewRequest(http.MethodPost, "/v1/"+signal, bytes.NewReader(body))
					req.Header.Set("X-Dagger-Session-ID", sess.sessionID)
					req.Header.Set("X-Dagger-Client-ID", client)
					resp := httptest.NewRecorder()
					ps.ServeHTTP(resp, req)
					require.Equal(t, http.StatusCreated, resp.Code, resp.Body.String())
				}
				load := func(client string) proto.Message {
					t.Helper()
					db, err := dbs.Open(t.Context(), client)
					require.NoError(t, err)
					defer db.Close()
					switch signal {
					case "traces":
						rows, err := db.Read().SelectSpansSince(t.Context(), clientdb.SelectSpansSinceParams{Limit: 100})
						require.NoError(t, err)
						batch := &colTraces.ExportTraceServiceRequest{}
						for _, row := range rows {
							batch.ResourceSpans = append(batch.ResourceSpans, telemetry.SpansToPB([]trace.ReadOnlySpan{row.ReadOnly()})...)
						}
						return batch
					case "logs":
						rows, err := db.Read().SelectLogsSince(t.Context(), clientdb.SelectLogsSinceParams{Limit: 100})
						require.NoError(t, err)
						return &colLogs.ExportLogsServiceRequest{ResourceLogs: clientdb.LogsToPB(rows)}
					default:
						rows, err := db.Read().SelectMetricsSince(t.Context(), clientdb.SelectMetricsSinceParams{Limit: 100})
						require.NoError(t, err)
						return &colMetrics.ExportMetricsServiceRequest{ResourceMetrics: clientdb.MetricsToPB(rows)}
					}
				}

				// Separate exports preserve live and completed versions of the same
				// spans, identical log bodies at distinct times, and metric updates.
				post("child", reflectionBatch(signal, 0))
				post("child", reflectionBatch(signal, 1))
				before := load("parent")
				require.Equal(t, 4, reflectionRecordCount(before))
				assertReflectionSamples(t, before)
				assertReflectionSession(t, before, "")

				req := httptest.NewRequest(http.MethodGet, "/v1/"+signal, nil)
				if binary {
					req.Header.Set("Accept", enginetel.LiveContentType)
				}
				resp := httptest.NewRecorder()
				var err error
				switch signal {
				case "traces":
					err = ps.TracesSubscribeHandler(resp, req, sess.clientRecords["child"])
				case "logs":
					err = ps.LogsSubscribeHandler(resp, req, sess.clientRecords["child"])
				case "metrics":
					err = ps.MetricsSubscribeHandler(resp, req, sess.clientRecords["child"])
				}
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, resp.Code)
				forwarded := before.ProtoReflect().New().Interface()
				if binary {
					for {
						kind, _, payload, err := enginetel.ReadLiveFrame(resp.Body)
						require.NoError(t, err)
						if kind == enginetel.LiveFrameTerminal {
							break
						}
						if kind == enginetel.LiveFrameData {
							batch := before.ProtoReflect().New().Interface()
							require.NoError(t, proto.Unmarshal(payload, batch))
							proto.Merge(forwarded, batch)
						}
					}
				} else {
					reader := sse.NewReadCloser(io.NopCloser(resp.Body))
					for {
						event, err := reader.Next()
						if err == io.EOF {
							break
						}
						require.NoError(t, err)
						if event.Name == signal || (signal == "traces" && event.Name == "spans") {
							batch := before.ProtoReflect().New().Interface()
							require.NoError(t, protojson.Unmarshal(event.Data, batch))
							proto.Merge(forwarded, batch)
						}
					}
				}
				require.Equal(t, 4, reflectionRecordCount(forwarded))
				// Mimic a nested CLI forwarding its subscription through inherited
				// OTLP settings, whose authenticated target is its parent client.
				forwarded = reflectionSDKRoundTrip(t, forwarded)
				post("parent", forwarded)
				after := load("parent")
				require.Equal(t, 4, reflectionRecordCount(after), "forwarded child subscription must not duplicate parent telemetry")
				assertReflectionSamples(t, after)
				assertReflectionSession(t, after, "")
				assertReflectionSession(t, forwarded, sess.sessionID)
				assertReflectionSession(t, load("child"), "")

				// Drop only reflected resource groups, not the entire mixed batch.
				// Fresh CLI telemetry can have the exact same contents, and another
				// session's stream is legitimate incoming telemetry too.
				mixed := proto.Clone(forwarded)
				proto.Merge(mixed, reflectionBatch(signal, 0))
				otherSession := reflectionBatch(signal, 1)
				forEachReflectionResource(otherSession, func(res *resourcepb.Resource) {
					res.Attributes = append(res.Attributes, reflectionStringAttr(reflectionSessionAttr, "other-session"))
				})
				proto.Merge(mixed, otherSession)
				post("parent", mixed)
				require.Equal(t, 8, reflectionRecordCount(load("parent")), "keep unmarked and different-session groups exactly once")
				require.Equal(t, 4, reflectionRecordCount(load("child")), "parent intake must not route back to the child")
			})
		}
	}
}

func TestTelemetryReflectedMetricDeltasAreDroppedBeforeDecoding(t *testing.T) {
	dbs := clientdb.NewDBs(t.TempDir())
	srv := &Server{clientDBs: dbs}
	ps := NewPubSub(srv)
	sess := &daggerSession{sessionID: "session", telemetryPubSub: ps, clientRecords: map[string]*clientRecord{}}
	sess.state.Store(sessionStateInitialized)
	srv.daggerSessions = map[string]*daggerSession{sess.sessionID: sess}
	shutdown := make(chan struct{})
	close(shutdown)
	parent := &clientRecord{daggerSession: sess, clientID: "parent"}
	child := &clientRecord{daggerSession: sess, clientID: "child", parentClientIDs: []string{"parent"}, shutdownCh: shutdown}
	sess.clientRecords[parent.clientID] = parent
	sess.clientRecords[child.clientID] = child

	// The SDK can export delta sums to storage, although the incoming OTLP
	// decoder currently only supports gauges. Reflected deltas must be discarded
	// before that decoder, not fail the entire batch (and trigger OTLP retries).
	for update := range 2 {
		require.NoError(t, (clientMetricExporter{record: child, ps: ps}).Export(t.Context(), &metricdata.ResourceMetrics{
			Resource: resource.Empty(),
			ScopeMetrics: []metricdata.ScopeMetrics{{Metrics: []metricdata.Metrics{{
				Name: "child.delta",
				Data: metricdata.Sum[int64]{Temporality: metricdata.DeltaTemporality, IsMonotonic: true, DataPoints: []metricdata.DataPoint[int64]{{
					StartTime: time.Unix(0, int64(100+update)), Time: time.Unix(0, int64(101+update)), Value: int64(update + 1),
				}}},
			}}}},
		}))
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/metrics", nil)
	req.Header.Set("Accept", enginetel.LiveContentType)
	resp := httptest.NewRecorder()
	require.NoError(t, ps.MetricsSubscribeHandler(resp, req, child))
	forwarded := &colMetrics.ExportMetricsServiceRequest{}
	for {
		kind, _, payload, err := enginetel.ReadLiveFrame(resp.Body)
		require.NoError(t, err)
		if kind == enginetel.LiveFrameTerminal {
			break
		}
		if kind == enginetel.LiveFrameData {
			batch := &colMetrics.ExportMetricsServiceRequest{}
			require.NoError(t, proto.Unmarshal(payload, batch))
			proto.Merge(forwarded, batch)
		}
	}
	values := map[int64]int{}
	for _, res := range forwarded.ResourceMetrics {
		for _, scope := range res.ScopeMetrics {
			for _, metric := range scope.Metrics {
				require.Equal(t, metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA, metric.GetSum().AggregationTemporality)
				for _, point := range metric.GetSum().DataPoints {
					values[point.GetAsInt()]++
				}
			}
		}
	}
	require.Equal(t, map[int64]int{1: 1, 2: 1}, values, "retain both original delta measurements")
	// Include supported fresh telemetry so an early return for the entire
	// request cannot accidentally satisfy this test.
	proto.Merge(forwarded, reflectionBatch("metrics", 0))
	body, err := proto.Marshal(forwarded)
	require.NoError(t, err)
	req = httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(body))
	req.Header.Set("X-Dagger-Session-ID", sess.sessionID)
	req.Header.Set("X-Dagger-Client-ID", parent.clientID)
	resp = httptest.NewRecorder()
	ps.ServeHTTP(resp, req)
	require.Equal(t, http.StatusCreated, resp.Code, resp.Body.String())
	db, err := dbs.Open(t.Context(), parent.clientID)
	require.NoError(t, err)
	defer db.Close()
	rows, err := db.Read().SelectMetricsSince(t.Context(), clientdb.SelectMetricsSinceParams{Limit: 100})
	require.NoError(t, err)
	require.Equal(t, 4, reflectionRecordCount(&colMetrics.ExportMetricsServiceRequest{ResourceMetrics: clientdb.MetricsToPB(rows)}), "two original deltas and two fresh gauge measurements")
}

// Exercise the conversions used by the client's telemetry re-export path: the
// resource marker must survive SDK records, not just a raw protobuf replay.
func reflectionSDKRoundTrip(t *testing.T, batch proto.Message) proto.Message {
	t.Helper()
	switch batch := batch.(type) {
	case *colTraces.ExportTraceServiceRequest:
		return &colTraces.ExportTraceServiceRequest{ResourceSpans: telemetry.SpansToPB(telemetry.SpansFromPB(batch.ResourceSpans))}
	case *colLogs.ExportLogsServiceRequest:
		capture := new(callPayloadRecordCapture)
		require.NoError(t, telemetry.ReexportLogsFromPB(t.Context(), capture, batch))
		return &colLogs.ExportLogsServiceRequest{ResourceLogs: telemetry.LogsToPB(capture.records)}
	case *colMetrics.ExportMetricsServiceRequest:
		result := &colMetrics.ExportMetricsServiceRequest{}
		for _, res := range batch.ResourceMetrics {
			sdk, err := telemetry.ResourceMetricsFromPB(res)
			require.NoError(t, err)
			pb, err := telemetry.ResourceMetricsToPB(sdk)
			require.NoError(t, err)
			result.ResourceMetrics = append(result.ResourceMetrics, pb)
		}
		return result
	default:
		t.Fatalf("unexpected batch type %T", batch)
		return nil
	}
}

func reflectionStringAttr(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}}
}

func reflectionBatch(signal string, update int) proto.Message {
	// Multiple resource groups exercise marking/filtering every group, including
	// a resource with no attributes. Keep the second resource distinguishable.
	resources := []*resourcepb.Resource{{}, {Attributes: []*commonpb.KeyValue{reflectionStringAttr("service.name", "child")}}}
	switch signal {
	case "traces":
		batch := &colTraces.ExportTraceServiceRequest{}
		for i, res := range resources {
			end := uint64(0)
			if update > 0 {
				end = 200
			}
			batch.ResourceSpans = append(batch.ResourceSpans, &tracepb.ResourceSpans{Resource: res, ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{
				TraceId: bytes.Repeat([]byte{1}, 16), SpanId: bytes.Repeat([]byte{byte(i + 1)}, 8), Name: "child-work", StartTimeUnixNano: 100, EndTimeUnixNano: end,
			}}}}})
		}
		return batch
	case "logs":
		batch := &colLogs.ExportLogsServiceRequest{}
		for _, res := range resources {
			batch.ResourceLogs = append(batch.ResourceLogs, &logspb.ResourceLogs{Resource: res, ScopeLogs: []*logspb.ScopeLogs{{LogRecords: []*logspb.LogRecord{{
				TimeUnixNano: uint64(100 + update), TraceId: bytes.Repeat([]byte{1}, 16), SpanId: bytes.Repeat([]byte{1}, 8), Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "same body"}},
			}}}}})
		}
		return batch
	default:
		batch := &colMetrics.ExportMetricsServiceRequest{}
		for _, res := range resources {
			batch.ResourceMetrics = append(batch.ResourceMetrics, &metricspb.ResourceMetrics{Resource: res, ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{{
				Name: "child.work", Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{{
					StartTimeUnixNano: uint64(100 + update), TimeUnixNano: uint64(101 + update), Value: &metricspb.NumberDataPoint_AsInt{AsInt: int64(update + 1)},
				}}}},
			}}}}})
		}
		return batch
	}
}

// All three OTLP export requests have their resource-group list at field 1,
// and each resource group has its resource at field 1.
func forEachReflectionResource(batch proto.Message, fn func(*resourcepb.Resource)) {
	msg := batch.ProtoReflect()
	groups := msg.Get(msg.Descriptor().Fields().ByNumber(1)).List()
	for i := 0; i < groups.Len(); i++ {
		group := groups.Get(i).Message()
		res := group.Mutable(group.Descriptor().Fields().ByNumber(1)).Message().Interface().(*resourcepb.Resource)
		fn(res)
	}
}

func assertReflectionSession(t *testing.T, batch proto.Message, session string) {
	t.Helper()
	forEachReflectionResource(batch, func(res *resourcepb.Resource) {
		var values []string
		for _, attr := range res.Attributes {
			if attr.Key == reflectionSessionAttr {
				values = append(values, attr.Value.GetStringValue())
			}
		}
		if session == "" {
			require.Empty(t, values, "subscription marker must not be stored")
		} else {
			require.Equal(t, []string{session}, values, "each streamed resource must have exactly one session marker")
		}
	})
}

func reflectionRecordCount(batch proto.Message) int {
	count := 0
	// Resource groups contain scopes at field 2; scopes contain records at 2.
	msg := batch.ProtoReflect()
	groups := msg.Get(msg.Descriptor().Fields().ByNumber(1)).List()
	for i := 0; i < groups.Len(); i++ {
		group := groups.Get(i).Message()
		scopes := group.Get(group.Descriptor().Fields().ByNumber(2)).List()
		for j := 0; j < scopes.Len(); j++ {
			scope := scopes.Get(j).Message()
			count += scope.Get(scope.Descriptor().Fields().ByNumber(2)).List().Len()
		}
	}
	return count
}

func assertReflectionSamples(t *testing.T, batch proto.Message) {
	t.Helper()
	switch batch := batch.(type) {
	case *colTraces.ExportTraceServiceRequest:
		ends := map[bool]int{}
		ids := map[string]int{}
		for _, res := range batch.ResourceSpans {
			for _, scope := range res.ScopeSpans {
				for _, span := range scope.Spans {
					ends[int64(span.EndTimeUnixNano) < int64(span.StartTimeUnixNano)]++
					ids[string(span.SpanId)]++
				}
			}
		}
		require.Equal(t, map[bool]int{true: 2, false: 2}, ends)
		require.Len(t, ids, 2)
		for _, updates := range ids {
			require.Equal(t, 2, updates, "both live and final span updates must survive")
		}
	case *colLogs.ExportLogsServiceRequest:
		times := map[uint64]int{}
		for _, res := range batch.ResourceLogs {
			for _, scope := range res.ScopeLogs {
				for _, rec := range scope.LogRecords {
					require.Equal(t, "same body", rec.Body.GetStringValue())
					times[rec.TimeUnixNano]++
				}
			}
		}
		require.Equal(t, map[uint64]int{100: 2, 101: 2}, times)
	case *colMetrics.ExportMetricsServiceRequest:
		values := map[int64]int{}
		for _, res := range batch.ResourceMetrics {
			for _, scope := range res.ScopeMetrics {
				for _, metric := range scope.Metrics {
					for _, point := range metric.GetGauge().DataPoints {
						values[point.GetAsInt()]++
					}
				}
			}
		}
		require.Equal(t, map[int64]int{1: 2, 2: 2}, values)
	}
}
