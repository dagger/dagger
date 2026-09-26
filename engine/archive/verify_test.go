package archive

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	logapi "go.opentelemetry.io/otel/log"
	collogpb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logpb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

func bootstrapControlLog(t *testing.T, a agentcontrol.Agent) *logpb.LogRecord {
	t.Helper()
	traceID, err := hex.DecodeString(a.Trace)
	require.NoError(t, err)
	out := &logpb.LogRecord{TraceId: traceID, SpanId: []byte{1, 0, 0, 0, 0, 0, 0, 0}, Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{}}}
	r := a.Record()
	r.WalkAttributes(func(kv logapi.KeyValue) bool {
		out.Attributes = append(out.Attributes, &commonpb.KeyValue{Key: kv.Key, Value: telemetry.LogValueToPB(kv.Value)})
		return true
	})
	return out
}

func TestBootstrapRawValidationPrecedesAllCallbacks(t *testing.T) {
	traceID := testTraceA
	id := call.New().Append(&ast.Type{NamedType: "LLM"}, "llm")
	a := agentcontrol.Agent{Key: agentcontrol.Key{Namespace: agentcontrol.Namespace{Session: "session", Trace: traceID, Incarnation: "incarnation"}, Handle: "agent"}, Revision: 4, State: "IDLE", Digest: id.Digest().String()}
	for _, tc := range []string{"valid", "missing payload", "missing final revision", "missing whole agent", "capture failure", "capture failure with stray payload", "recipe corruption", "duplicate attribute"} {
		t.Run(tc, func(t *testing.T) {
			agent := a
			want := agentcontrol.Expectation{Agents: map[agentcontrol.Key]int64{agent.Key: agent.Revision}}
			c := proto.Clone(id.Call()).(*callpbv1.Call)
			if tc == "recipe corruption" {
				c.Field = "different"
			}
			if tc == "missing final revision" {
				want.Agents[agent.Key]++
			}
			if tc == "missing whole agent" {
				key := agent.Key
				key.Handle = "lost"
				want.Agents[key] = 1
			}
			if tc == "capture failure" || tc == "capture failure with stray payload" {
				// A recorded capture failure is a witnessed final revision with
				// no closure root: it verifies, and restore reports it instead.
				agent.Digest = ""
				agent.CaptureError = "snapshot unavailable"
			}
			control := bootstrapControlLog(t, agent)
			if tc == "duplicate attribute" {
				control.Attributes = append(control.Attributes, control.Attributes[0])
			}
			logs := []*logpb.LogRecord{control}
			if tc != "missing payload" && tc != "capture failure" {
				payload, err := proto.Marshal(c)
				require.NoError(t, err)
				logs = append(logs, &logpb.LogRecord{TraceId: control.TraceId, SpanId: control.SpanId, Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: payload}}, Attributes: []*commonpb.KeyValue{{Key: telemetry.ContentTypeAttr, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: telemetryattrs.CallPayloadContentType}}}}})
			}
			req := &collogpb.ExportLogsServiceRequest{ResourceLogs: []*logpb.ResourceLogs{{Resource: &resourcepb.Resource{}, ScopeLogs: []*logpb.ScopeLogs{{LogRecords: logs}}}}}
			payload, err := proto.Marshal(req)
			require.NoError(t, err)
			data, _, err := BuildBootstrap(BootstrapHeader{SourceSession: "session", TraceID: traceID, Generation: "gen", SealAt: time.Now().UTC().Format(time.RFC3339Nano), Completion: Witness(want)}, []BootstrapSignal{{Kind: BootstrapFrameLogs, Payload: payload, Records: int64(len(logs))}})
			require.NoError(t, err)
			client, closeServer := bootstrapTestClient(t, "gen", data)
			defer closeServer()
			callbacks := 0
			_, err = client.Bootstrap(t.Context(), traceID, "gen", func(BootstrapHeader, BootstrapBatch) error { callbacks++; return nil })
			if tc == "valid" || tc == "capture failure" {
				require.NoError(t, err)
				require.Equal(t, 1, callbacks)
			} else {
				require.ErrorIs(t, err, ErrCorrupt)
				require.Zero(t, callbacks)
				require.False(t, IsCleanMiss(err))
			}
		})
	}
}

func TestBootstrapTruncationNeverAppliesPartialSignals(t *testing.T) {
	data, _, err := BuildBootstrap(BootstrapHeader{SourceSession: "session", TraceID: testTraceA, Generation: "gen", SealAt: time.Now().UTC().Format(time.RFC3339Nano)}, []BootstrapSignal{{Kind: BootstrapFrameLogs}})
	require.NoError(t, err)
	client, closeServer := bootstrapTestClient(t, "gen", data[:len(data)-1])
	defer closeServer()
	callbacks := 0
	_, err = client.Bootstrap(context.Background(), testTraceA, "gen", func(BootstrapHeader, BootstrapBatch) error { callbacks++; return nil })
	require.ErrorIs(t, err, ErrTransient)
	require.Zero(t, callbacks)
	require.False(t, IsCleanMiss(err))
}
