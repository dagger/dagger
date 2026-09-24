package core

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/dagui"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestAgentTraceCaptureWaitsForCurrentAnchor(t *testing.T) {
	for _, last := range []string{"span", "log"} {
		t.Run(last+"-last", func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				db := dagui.NewDB()
				sink := &agentTraceSink{db: db, logExp: db.LogExporter(), changed: make(chan struct{})}
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				traceID := []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
				loopID := []byte{1, 0, 0, 0, 0, 0, 0, 0}
				attr := func(key, value string) *commonpb.KeyValue {
					return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}}
				}
				export := func(req proto.Message, handler http.HandlerFunc) {
					t.Helper()
					body, err := proto.Marshal(req)
					require.NoError(t, err)
					response := httptest.NewRecorder()
					handler(response, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)))
					require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
				}
				exportSpans := func(spans ...*tracepb.Span) {
					export(&coltracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{Resource: &resourcepb.Resource{}, ScopeSpans: []*tracepb.ScopeSpans{{Spans: spans}}}}}, sink.tracesHandler)
				}
				exportLog := func(body *commonpb.AnyValue, attrs ...*commonpb.KeyValue) {
					if body == nil {
						body = &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{}}
					}
					export(&collogspb.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{Resource: &resourcepb.Resource{}, ScopeLogs: []*logspb.ScopeLogs{{LogRecords: []*logspb.LogRecord{{TraceId: traceID, SpanId: loopID, TimeUnixNano: uint64(time.Now().UnixNano()), Body: body, Attributes: attrs}}}}}}}, sink.logsHandler)
				}
				frame := func(field, receiver string) *callpbv1.Call {
					pb := &callpbv1.Call{Field: field, ReceiverDigest: receiver, Type: &callpbv1.Type{NamedType: "LLM"}}
					pb.Digest = digest.FromString(pb.String()).String()
					return pb
				}
				old := frame("llm", "")
				dependency := frame("otherLLM", "")
				current := frame("withResponse", dependency.Digest)
				callSpan := func(pb *callpbv1.Call, id byte) *tracepb.Span {
					payload, err := pb.Encode()
					require.NoError(t, err)
					// An initial live span is sufficient; completion is not a prerequisite.
					return &tracepb.Span{TraceId: traceID, SpanId: []byte{id, 0, 0, 0, 0, 0, 0, 0}, Name: pb.Field, StartTimeUnixNano: uint64(time.Now().UnixNano()), Attributes: []*commonpb.KeyValue{attr(telemetry.DagDigestAttr, pb.Digest), attr(telemetry.DagCallAttr, payload)}}
				}
				exportSpans(&tracepb.Span{TraceId: traceID, SpanId: loopID, Name: "agent: tests", StartTimeUnixNano: uint64(time.Now().UnixNano()), Attributes: []*commonpb.KeyValue{
					{Key: telemetryattrs.AgentAttr, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: true}}},
					attr(telemetryattrs.AgentIDAttr, "tests"), attr(telemetryattrs.AgentNameAttr, "tests"), attr(telemetryattrs.AgentCallDigestAttr, "agent-call"),
				}})
				exportLog(nil, attr(telemetryattrs.AgentSnapshotDigestAttr, old.Digest))
				// The waiters first observe A without its definition. Replace that anchor
				// only after they block, then complete A while B remains incomplete.
				exportLog(nil, attr(telemetryattrs.AgentStateAttr, "STOPPED"), attr(telemetryattrs.AgentStopReasonAttr, "EXPLICIT"))
				type outcome struct {
					captured restorableTraceCapture
					err      error
				}
				results := make(chan outcome, 2)
				for range 2 {
					go func() {
						captured, err := sink.restorableCapture(ctx, 1, map[string]string{"tests": "STOPPED"})
						results <- outcome{captured, err}
					}()
				}
				assertWaiting := func() {
					t.Helper()
					synctest.Wait()
					select {
					case result := <-results:
						t.Fatalf("capture returned before current closure arrived: %v", result.err)
					default:
					}
				}
				assertWaiting()
				exportLog(nil, attr(telemetryattrs.AgentSnapshotDigestAttr, current.Digest))
				exportSpans(callSpan(old, 2))
				assertWaiting() // Completing A must not satisfy a wait for current B.
				exportCurrent := func() {
					payload, err := proto.Marshal(current)
					require.NoError(t, err)
					exportLog(&commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: payload}}, attr(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
				}
				if last == "span" {
					exportCurrent()
					assertWaiting() // The root alone is not its full recipe closure.
					exportSpans(callSpan(dependency, 3))
				} else {
					exportSpans(callSpan(dependency, 3))
					assertWaiting() // A dependency does not supply the current root.
					exportCurrent()
				}
				synctest.Wait()
				for range 2 {
					result := <-results
					require.NoError(t, result.err)
					// Replay the actual captured requests through the production importer.
					replay := dagui.NewDB()
					importer := enginetel.NewTraceImporter(enginetel.TraceImportSinks{Spans: replay, Logs: replay.LogExporter()})
					for _, req := range result.captured.traces {
						require.NoError(t, importer.ImportSpans(ctx, req))
					}
					for _, req := range result.captured.logs {
						require.NoError(t, importer.ImportLogs(ctx, req))
					}
					agents := replay.Agents()
					require.Len(t, agents, 1)
					require.Equal(t, "STOPPED", agents[0].State)
					require.Equal(t, current.Digest, agents[0].SnapshotDigest)
					_, err := replay.CallIDForDigest(agents[0].SnapshotDigest)
					require.NoError(t, err)
				}
			})
		})
	}
}
