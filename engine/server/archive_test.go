package server

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/clientdb"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlpmetricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestBuildArchiveBootstrapSelectsResumeRecords(t *testing.T) {
	registry := clientdb.NewDBs(t.TempDir())
	defer registry.Close()
	db, err := registry.Open(context.Background(), "main")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	traceID := "11111111111111111111111111111111"
	spanID := "1111111111111111"
	attrs, err := clientdb.MarshalProtoJSONs(telemetry.KeyValues([]attribute.KeyValue{
		attribute.Bool(telemetryattrs.AgentAttr, true),
		attribute.String(telemetryattrs.AgentIDAttr, "agent"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := protojson.Marshal(&otlpcommonv1.InstrumentationScope{})
	resource, _ := protojson.Marshal(&otlpresourcev1.Resource{})
	empty, _ := clientdb.MarshalProtoJSONs([]*otlpcommonv1.KeyValue{})
	if _, err := db.AppendSpans([]clientdb.Span{{
		TraceID: traceID, SpanID: spanID, Name: "agent", Attributes: attrs,
		InstrumentationScope: scope, Resource: resource, Events: []byte("[]"), Links: []byte("[]"),
	}}); err != nil {
		t.Fatal(err)
	}
	stateAttrs, _ := clientdb.MarshalProtoJSONs([]*otlpcommonv1.KeyValue{{
		Key:   telemetryattrs.AgentStateAttr,
		Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "idle"}},
	}})
	snapshotAttrs, _ := clientdb.MarshalProtoJSONs([]*otlpcommonv1.KeyValue{{
		Key:   telemetryattrs.AgentSnapshotDigestAttr,
		Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "xxh3:snapshot"}},
	}})
	payloadScope, _ := protojson.Marshal(&otlpcommonv1.InstrumentationScope{Name: telemetryattrs.CallPayloadInstrumentationScope})
	body, _ := proto.Marshal(&otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "record"}})
	logBase := clientdb.Log{
		TraceID: sql.NullString{String: traceID, Valid: true}, SpanID: sql.NullString{String: spanID, Valid: true},
		Body: body, Attributes: empty, InstrumentationScope: scope, Resource: resource,
	}
	ordinary := logBase
	state := logBase
	state.Attributes = stateAttrs
	snapshot := logBase
	snapshot.Attributes = snapshotAttrs
	payload := logBase
	payload.InstrumentationScope = payloadScope
	if _, err := db.AppendLogs([]clientdb.Log{ordinary, state, snapshot, payload}); err != nil {
		t.Fatal(err)
	}
	cut, err := db.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	sealAt := time.Now().UTC()
	data, records, hasAgents, err := buildArchiveBootstrap(context.Background(), db, archive.Manifest{
		Version: archive.ManifestVersion, Generation: "generation", TraceID: traceID,
		MainClientID: "main", BoundarySpanID: "2222222222222222",
	}, cut, sealAt)
	if err != nil {
		t.Fatal(err)
	}
	if !hasAgents || records != 5 {
		t.Fatalf("hasAgents=%v records=%d", hasAgents, records)
	}
	header, terminal, err := archive.VerifyBootstrap(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if header.HighWater.Logs != 4 || header.SealAt != sealAt.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected header: %+v", header)
	}
	if terminal.TraceRecords != 2 || terminal.LogRecords != 3 {
		t.Fatalf("unexpected terminal counts: %+v", terminal)
	}
	if len(terminal.Exclusions.LogRowIDs) != 3 || terminal.Exclusions.LogRowIDs[0] != 2 || terminal.Exclusions.LogRowIDs[1] != 3 || terminal.Exclusions.LogRowIDs[2] != 4 {
		t.Fatalf("ordinary log was included or control rows missing: %+v", terminal.Exclusions)
	}
}

func TestArchiveHTTPTypedStateAndFiniteSignal(t *testing.T) {
	root := t.TempDir()
	registry := clientdb.NewDBs(root)
	defer registry.Close()
	manager, err := archive.NewManager(archive.Config{Root: root + "/archives"})
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{clientDBs: registry, archives: manager}
	record := &clientRecord{clientID: "reader", clientMetadata: &engine.ClientMetadata{}}

	active, err := manager.Register("22222222222222222222222222222222", "active-client")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/telemetry/archives/"+active.TraceID+"/bootstrap", nil)
	if err := srv.serveArchiveHTTP(response, request, record); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusConflict || !bytes.Contains(response.Body.Bytes(), []byte(`"state":"active"`)) {
		t.Fatalf("active response: %d %s", response.Code, response.Body.String())
	}

	closed, err := manager.Register("33333333333333333333333333333333", "closed-client")
	if err != nil {
		t.Fatal(err)
	}
	db, err := registry.Open(context.Background(), closed.MainClientID)
	if err != nil {
		t.Fatal(err)
	}
	metricData, err := protojson.Marshal(&otlpmetricsv1.ResourceMetrics{Resource: &otlpresourcev1.Resource{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AppendMetrics([]clientdb.Metric{{Data: metricData}}); err != nil {
		t.Fatal(err)
	}
	cut, err := db.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := manager.BeginFinalizing(closed.TraceID, closed.Generation); err != nil {
		t.Fatal(err)
	}
	sealAt := time.Now().UTC()
	highWater := archive.HighWater{Spans: cut.Spans, Logs: cut.Logs, Metrics: cut.Metrics}
	sidecar, _, err := archive.BuildBootstrap(archive.BootstrapHeader{
		Generation: closed.Generation, TraceID: closed.TraceID,
		SealAt: sealAt.Format(time.RFC3339Nano), HighWater: highWater,
	}, nil, archive.BootstrapExclusions{})
	if err != nil {
		t.Fatal(err)
	}
	closed, err = manager.Finalize(closed.TraceID, closed.Generation, archive.FinalizeInput{
		HighWater: highWater, SealAt: sealAt, StoreSizeBytes: cut.SizeBytes, BootstrapBytes: sidecar,
	})
	if err != nil {
		t.Fatal(err)
	}

	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/v1/telemetry/archives/"+closed.TraceID+"/metrics", nil)
	if err := srv.serveArchiveHTTP(response, request, record); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || response.Header().Get("X-Dagger-Archive-Generation") != closed.Generation {
		t.Fatalf("metrics response: %d %+v", response.Code, response.Header())
	}
	cursor, _, terminal, err := enginetel.ReadLiveFrame(response.Body)
	if err != nil || terminal || cursor != 1 {
		t.Fatalf("data frame: cursor=%d terminal=%v err=%v", cursor, terminal, err)
	}
	cursor, _, terminal, err = enginetel.ReadLiveFrame(response.Body)
	if err != nil || !terminal || cursor != 1 {
		t.Fatalf("terminal frame: cursor=%d terminal=%v err=%v", cursor, terminal, err)
	}
	if _, _, _, err := enginetel.ReadLiveFrame(response.Body); !errors.Is(err, io.EOF) {
		t.Fatalf("stream has trailing frame: %v", err)
	}
}

func TestArchiveRequestRequiresMainClientSecret(t *testing.T) {
	sess := &daggerSession{
		sessionID: "session", mainClientCallerID: "main",
		clientRecords: map[string]*clientRecord{},
	}
	sess.state.Store(sessionStateInitialized)
	main := &clientRecord{daggerSession: sess, clientID: "main", secretToken: "secret"}
	nested := &clientRecord{daggerSession: sess, clientID: "nested", secretToken: "nested-secret"}
	sess.clientRecords["main"] = main
	sess.clientRecords["nested"] = nested
	srv := &Server{daggerSessions: map[string]*daggerSession{"session": sess}}
	if _, err := srv.archiveRequestRecord("main", "session", "secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.archiveRequestRecord("main", "session", "wrong"); err == nil {
		t.Fatal("wrong secret accepted")
	}
	if _, err := srv.archiveRequestRecord("nested", "session", "nested-secret"); err == nil {
		t.Fatal("nested client accepted")
	}
}
