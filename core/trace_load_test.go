package core

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/dagger/dagger/internal/cloud/otlpstream"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"golang.org/x/oauth2"
	"google.golang.org/protobuf/proto"
)

func TestNormalizeTraceArg(t *testing.T) {
	const id = "2f81064627bbd17b45441b93ac4fc8cf"
	for _, arg := range []string{id, strings.ToUpper(id), " dagger trace " + id + "\n", "https://dagger.cloud/org/traces/" + id, "https://dagger.cloud/org/traces/" + id + "/spans/123?focus=true"} {
		got, err := normalizeTraceArg(arg)
		require.NoError(t, err, arg)
		require.Equal(t, id, got)
	}
	for _, arg := range []string{"", "00000000000000000000000000000000", "bad", "dagger trace " + id + "; echo secret", "https://evil.example/org/traces/" + id, "https://dagger.cloud@evil.example/traces/" + id, "http://dagger.cloud/org/traces/" + id} {
		_, err := normalizeTraceArg(arg)
		require.Error(t, err, arg)
		require.NotContains(t, err.Error(), arg+";")
	}
}

// Exercise the actual registered tools, the Cloud framed transport, and the
// session telemetry seam. Recipes travel partly in binary log payloads; the
// fake server supplies telemetry only, so no recipe evaluation can be needed.
func TestLoadTraceInspectionTools(t *testing.T) {
	const traceID = "000102030405060708090a0b0c0d0e0f"
	const rootID = "0000000000000001"
	const callID = "0000000000000002"
	const unfinishedID = "0000000000000003"
	const secret = "test-cloud-credential-never-in-output"
	fixture, digest, _ := callInspectStore(t, callInspectRecipe(t))
	rows, err := fixture.SelectSpansLatest(t.Context(), fixture.SpanIDs())
	require.NoError(t, err)
	var spans []sdktrace.ReadOnlySpan
	for i := range rows {
		spans = append(spans, rows[i].ReadOnly())
	}
	spanReq := &coltracepb.ExportTraceServiceRequest{ResourceSpans: telemetry.SpansToPB(spans)}
	for _, resource := range spanReq.ResourceSpans {
		for _, scope := range resource.ScopeSpans {
			for _, span := range scope.Spans {
				span.StartTimeUnixNano = 100
				span.EndTimeUnixNano = 200
				key, value := telemetry.CheckNameAttr, "cloud:check"
				if hex.EncodeToString(span.SpanId) == callID {
					key, value = "test.case.name", "TestCloud"
				}
				span.Attributes = append(span.Attributes, &otlpcommonv1.KeyValue{Key: key, Value: stringLogBody(value)})
			}
		}
	}
	tid, err := hex.DecodeString(traceID)
	require.NoError(t, err)
	sid, err := hex.DecodeString(unfinishedID)
	require.NoError(t, err)
	parent, err := hex.DecodeString(rootID)
	require.NoError(t, err)
	spanReq.ResourceSpans[0].ScopeSpans[0].Spans = append(spanReq.ResourceSpans[0].ScopeSpans[0].Spans,
		&tracepb.Span{TraceId: tid, SpanId: sid, ParentSpanId: parent, Name: "left running", StartTimeUnixNano: 150})
	_, err = fixture.AppendLogs([]clientdb.Log{persistedCaptureLog(t, traceID, callID, "stdout", stringLogBody("cloud build output\n"))})
	require.NoError(t, err)
	logs, err := fixture.SelectLogsSince(t.Context(), clientdb.SelectLogsSinceParams{Limit: 100})
	require.NoError(t, err)
	require.Len(t, logs, 5)
	for i := range logs {
		if len(logs[i].Resource) == 0 {
			logs[i].Resource = []byte("{}")
		}
		if len(logs[i].InstrumentationScope) == 0 {
			logs[i].InstrumentationScope = []byte("{}")
		}
	}
	logReq := &collogspb.ExportLogsServiceRequest{ResourceLogs: clientdb.LogsToPB(logs)}
	var requests atomic.Int32
	var denied atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "missing credential", http.StatusUnauthorized)
			return
		}
		if denied.Load() {
			http.Error(w, secret, http.StatusForbidden)
			return
		}
		var msg proto.Message
		switch r.URL.Path {
		case "/v1/traces/" + traceID:
			msg = spanReq
		case "/v1/logs/" + traceID:
			msg = logReq
		case "/v1/metrics/" + traceID:
			msg = &colmetricspb.ExportMetricsServiceRequest{}
		default:
			http.NotFound(w, r)
			return
		}
		data, err := proto.Marshal(msg)
		if err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", otlpstream.ContentType)
		fw := otlpstream.NewFrameWriter(w)
		if err := fw.WriteData(data); err != nil {
			t.Error(err)
			return
		}
		if err := fw.WriteTerminal(); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	t.Setenv("DAGGER_CLOUD_URL", server.URL)
	dbs := clientdb.NewDBs(t.TempDir())
	live, err := dbs.Open(t.Context(), "capture-test")
	require.NoError(t, err)
	defer live.Close()
	_, err = live.AppendSpans([]clientdb.Span{{TraceID: "11111111111111111111111111111111", SpanID: "1111111111111111", Name: "live session", Attributes: []byte("[]"), Events: []byte("[]"), Links: []byte("[]"), Resource: []byte("{}"), InstrumentationScope: []byte("{}")}})
	require.NoError(t, err)
	meta := &engine.ClientMetadata{CloudAuth: &auth.Cloud{Token: &oauth2.Token{TokenType: "Bearer", AccessToken: secret}}}
	ctx := ContextWithQuery(t.Context(), &Query{Server: &logCaptureTestServer{mockServer: &mockServer{clientMetadata: meta}, dbs: dbs}})
	m := newMCP()
	tools := NewLLMToolSet()
	m.loadBuiltins(&dagql.Server{}, tools)
	invoke := func(name string, args map[string]any) string {
		t.Helper()
		tool, err := m.LookupTool(name, tools.Order)
		require.NoError(t, err)
		result, err := tool.Call(ctx, args)
		require.NoError(t, err, name)
		text := fmt.Sprint(result)
		require.NotContains(t, text, secret)
		return text
	}
	loadTool, err := m.LookupTool("LoadTrace", tools.Order)
	require.NoError(t, err)
	mcpTool, err := genMcpTool(*loadTool)
	require.NoError(t, err)
	require.Equal(t, "LoadTrace", mcpTool.Name)
	_, err = loadTool.Call(ctx, map[string]any{"trace": "not a trace"})
	require.ErrorContains(t, err, "invalid trace")
	require.Zero(t, requests.Load())
	loaded := invoke("LoadTrace", map[string]any{"trace": "dagger trace " + traceID})
	require.Contains(t, loaded, rootID)
	require.Contains(t, loaded, "3 spans")
	require.Equal(t, int32(3), requests.Load())
	require.Equal(t, loaded, invoke("LoadTrace", map[string]any{"trace": traceID}))
	require.Equal(t, int32(3), requests.Load(), "repeated loads must not duplicate logs or fetch again")
	require.Contains(t, invoke("FindSpans", map[string]any{}), "live session")
	require.Contains(t, invoke("FindSpans", map[string]any{"query": "Container"}), callID)
	require.NotContains(t, invoke("FindSpans", map[string]any{"span": rootID}), "live session")
	require.Contains(t, invoke("ReadTrace", map[string]any{"check": "cloud:check", "view": "inspect"}), rootID)
	require.Contains(t, invoke("ReadTrace", map[string]any{"test": "TestCloud", "view": "inspect"}), callID)
	callDetail := invoke("ReadTrace", map[string]any{"span": callID, "view": "inspect"})
	require.Contains(t, callDetail, digest)
	require.Contains(t, callDetail, "duration: 100ns (own span wall interval)")
	unfinishedDetail := invoke("ReadTrace", map[string]any{"span": unfinishedID, "view": "inspect"})
	require.Contains(t, unfinishedDetail, "ended:    (completion unrecorded; synthetic end:")
	require.Contains(t, unfinishedDetail, "duration: unknown (own span wall interval; completion unrecorded)")
	require.Contains(t, unfinishedDetail, "leftRunning")
	require.NotContains(t, unfinishedDetail, "still running")
	for _, minDuration := range []string{"0s", "1h"} {
		timings := invoke("ReadTrace", map[string]any{"span": rootID, "view": "timings", "minDuration": minDuration})
		require.Contains(t, timings, "Scope: raw parent edges only")
		require.Contains(t, timings, unfinishedID+"  "+rootID+"  50ns  unknown (completion unrecorded)  \"left running\"")
		require.NotContains(t, timings, "(so far)")
	}
	require.Contains(t, invoke("ReadTrace", map[string]any{"span": rootID}), "cloud:check")
	require.Contains(t, invoke("ReadTrace", map[string]any{"span": callID}), "cloud build output")
	require.Equal(t, "     1→cloud build output", invoke("ReadLogs", map[string]any{"span": rootID}))
	require.Contains(t, invoke("ReadLogs", map[string]any{"span": unfinishedID}), "no logs beneath")
	require.Contains(t, invoke("FindCalls", map[string]any{"query": "example.com/repo"}), "git(")
	require.Contains(t, invoke("InspectCall", map[string]any{"span": callID, "view": "tree"}), "https://example.com/repo")
	require.Contains(t, invoke("InspectCall", map[string]any{"digest": digest, "view": "stats"}), "distinct calls: 5")
	imported := inspectionStoreForSpan(live, unfinishedID)
	importedRows, err := imported.SelectSpansLatest(ctx, map[string]struct{}{unfinishedID: {}})
	require.NoError(t, err)
	require.True(t, importedRows[0].EndTime.Valid, "historical work must not remain live")
	require.Contains(t, string(importedRows[0].Attributes), telemetry.CanceledAttr)
	// The export cursors see only this run. Neither historical spans nor binary
	// call payloads are re-exported, and a different client's store sees nothing.
	liveRows, err := live.SelectSpansSince(ctx, clientdb.SelectSpansSinceParams{Limit: 100})
	require.NoError(t, err)
	require.Len(t, liveRows, 1)
	liveLogs, err := live.SelectLogsSince(ctx, clientdb.SelectLogsSinceParams{Limit: 100})
	require.NoError(t, err)
	require.Empty(t, liveLogs)
	other, err := dbs.Open(ctx, "other-session")
	require.NoError(t, err)
	defer other.Close()
	require.Len(t, other.InspectionStores(), 1)
	require.False(t, inspectionStoreForSpan(other, rootID).HasSpan(rootID))

	// Credentials and Cloud response bodies must not reach the model on errors.
	denied.Store(true)
	freshDBs := clientdb.NewDBs(t.TempDir())
	fresh, err := freshDBs.Open(ctx, "capture-test")
	require.NoError(t, err)
	defer fresh.Close()
	freshCtx := ContextWithQuery(t.Context(), &Query{Server: &logCaptureTestServer{mockServer: &mockServer{clientMetadata: meta}, dbs: freshDBs}})
	_, err = m.loadTraceTool(&dagql.Server{})(freshCtx, map[string]any{"trace": traceID})
	require.ErrorContains(t, err, "no data was imported")
	require.NotContains(t, err.Error(), secret)
	require.Len(t, fresh.InspectionStores(), 1)
	meta.CloudAuth = nil
	_, err = m.loadTraceTool(&dagql.Server{})(freshCtx, map[string]any{"trace": traceID})
	require.ErrorContains(t, err, "dagger login")
}

func TestImportedTraceCannotOverrideLive(t *testing.T) {
	ctx := t.Context()
	live, digest, frames := callInspectStore(t, callInspectRecipe(t))
	rows, err := live.SelectSpansLatest(ctx, live.SpanIDs())
	require.NoError(t, err)
	for i := range rows {
		rows[i].Name = "historical impostor"
		if rows[i].SpanID == "0000000000000002" {
			frame := frames["withDirectory"]
			frame.Field = "impostor"
			encoded, err := frame.Encode()
			require.NoError(t, err)
			rows[i].Attributes = marshalSpanAttrs(t,
				&otlpcommonv1.KeyValue{Key: telemetry.DagDigestAttr, Value: stringLogBody(digest)},
				&otlpcommonv1.KeyValue{Key: telemetry.DagCallAttr, Value: stringLogBody(encoded)})
		}
	}
	imported, err := live.ImportTrace(ctx, "historical", func(dst *clientdb.DB) error {
		_, err := dst.AppendSpans(rows)
		return err
	})
	require.NoError(t, err)
	require.Same(t, live, inspectionStoreForSpan(live, "0000000000000002"))
	got, err := findSpansIn(ctx, live, "", "", 100, imported)
	require.NoError(t, err)
	require.Contains(t, got, "Container.withDirectory")
	require.NotContains(t, got, "impostor")
	got, err = findSpansIn(ctx, live, "impostor", "", 100, imported)
	require.NoError(t, err)
	require.Contains(t, got, "no spans matching")
	got, err = findCallsIn(ctx, live, regexp.MustCompile(".*"), 100)
	require.NoError(t, err)
	require.Contains(t, got, "withDirectory")
	require.NotContains(t, got, "impostor")
	got, err = inspectCallIn(ctx, live, digest, callInspectOpts{View: callViewTree})
	require.NoError(t, err)
	require.Contains(t, got, "withDirectory")
	require.NotContains(t, got, "impostor")
}

func TestLoadTraceFailedImportIsRetryable(t *testing.T) {
	live, err := clientdb.NewDBs(t.TempDir()).Open(t.Context(), "live")
	require.NoError(t, err)
	defer live.Close()
	const id = "000102030405060708090a0b0c0d0e0f"
	partial := func(ctx context.Context, sink cloud.TraceImportSink) error {
		tid, err := hex.DecodeString(id)
		require.NoError(t, err)
		return sink.ImportSpans(ctx, &coltracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{TraceId: tid, SpanId: []byte{0, 0, 0, 0, 0, 0, 0, 1}, Name: "partial", StartTimeUnixNano: 100}}}}}}})
	}
	for _, failure := range []error{errors.New("secret response"), context.Canceled} {
		ctx := t.Context()
		var cancel context.CancelFunc
		if errors.Is(failure, context.Canceled) {
			ctx, cancel = context.WithCancel(ctx)
		}
		_, err := loadCloudTrace(ctx, live, id, func(ctx context.Context, _ string, sink cloud.TraceImportSink) error {
			require.NoError(t, partial(ctx, sink))
			if cancel != nil {
				cancel()
			}
			return failure
		})
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret response")
		require.Len(t, live.InspectionStores(), 1)
	}
	result, err := loadCloudTrace(t.Context(), live, id, func(ctx context.Context, _ string, sink cloud.TraceImportSink) error {
		if err := partial(ctx, sink); err != nil {
			return err
		}
		return sink.Seal(ctx)
	})
	require.NoError(t, err)
	require.Contains(t, result, "1 spans")
	require.Len(t, live.InspectionStores(), 2)
}
