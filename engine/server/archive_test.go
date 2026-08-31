package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/clientdb"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/opencontainers/go-digest"
	"go.opentelemetry.io/otel/attribute"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlpmetricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestBuildArchiveBootstrapSelectsVerifiedResumeClosure(t *testing.T) {
	registry := clientdb.NewDBs(t.TempDir())
	defer registry.Close()
	db, err := registry.Open(context.Background(), "main")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	traceID := "11111111111111111111111111111111"
	spanID := "1111111111111111"
	identityCall := "xxh3:identity-call-not-in-snapshot-closure"
	attrs, err := clientdb.MarshalProtoJSONs(telemetry.KeyValues([]attribute.KeyValue{
		attribute.Bool(telemetryattrs.AgentAttr, true),
		attribute.String(telemetryattrs.AgentIDAttr, "agent"),
		attribute.String(telemetryattrs.AgentNameAttr, "reviewer"),
		attribute.String(telemetryattrs.AgentCallDigestAttr, identityCall),
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
	body, _ := proto.Marshal(&otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "record"}})
	logBase := clientdb.Log{
		TraceID: sql.NullString{String: traceID, Valid: true}, SpanID: sql.NullString{String: spanID, Valid: true},
		Body: body, Attributes: empty, InstrumentationScope: scope, Resource: resource,
	}
	controlAttrs := func(attrs ...attribute.KeyValue) []byte {
		encoded, err := clientdb.MarshalProtoJSONs(telemetry.KeyValues(attrs))
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	ordinary := logBase
	idle := logBase
	idle.Attributes = controlAttrs(
		attribute.String(telemetryattrs.AgentStateAttr, string(core.AgentStateIdle)),
		attribute.String(telemetryattrs.AgentStopReasonAttr, ""),
	)
	stopped := logBase
	stopped.Attributes = controlAttrs(
		attribute.String(telemetryattrs.AgentStateAttr, string(core.AgentStateStopped)),
		attribute.String(telemetryattrs.AgentStopReasonAttr, string(core.AgentStopSession)),
	)

	dependency, dependencyDigest := archiveCallPayloadLog(t, traceID, spanID, &callpbv1.Call{Field: "dependency"})
	rootCall := &callpbv1.Call{Field: "snapshot", ReceiverDigest: dependencyDigest}
	rootPayload, rootDigest := archiveCallPayloadLog(t, traceID, spanID, rootCall)
	unrelated, _ := archiveCallPayloadLog(t, traceID, spanID, &callpbv1.Call{Field: "unrelated"})
	snapshot := logBase
	snapshot.Attributes = controlAttrs(attribute.String(telemetryattrs.AgentSnapshotDigestAttr, rootDigest))
	checkpoint := core.AgentCheckpoint{
		Version: core.AgentCheckpointVersion, Sequence: 1, AgentID: "agent", Name: "reviewer",
		CallDigest: identityCall, SnapshotDigest: rootDigest, State: core.AgentStateIdle,
	}
	nonFinal := archiveCheckpointLog(t, traceID, spanID, checkpoint)
	checkpoint.Sequence = 2
	checkpoint.State = core.AgentStateStopped
	checkpoint.PreTeardownState = core.AgentStateIdle
	checkpoint.StopReason = core.AgentStopSession
	checkpoint.Final = true
	checkpoint.ExpectedFinalSequence = 2
	final := archiveCheckpointLog(t, traceID, spanID, checkpoint)
	if _, err := db.AppendLogs([]clientdb.Log{
		ordinary, idle, stopped, snapshot, rootPayload, dependency, unrelated, nonFinal, final,
	}); err != nil {
		t.Fatal(err)
	}
	cut, err := db.Checkpoint(context.Background())
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
	if !hasAgents || records != 8 {
		t.Fatalf("hasAgents=%v records=%d", hasAgents, records)
	}
	header, terminal, err := archive.VerifyBootstrap(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if header.HighWater.Logs != 9 || header.SealAt != sealAt.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected header: %+v", header)
	}
	if terminal.TraceRecords != 2 || terminal.LogRecords != 6 {
		t.Fatalf("unexpected terminal counts: %+v", terminal)
	}
	wantLogRows := []int64{2, 3, 4, 5, 6, 9}
	if len(terminal.Exclusions.LogRowIDs) != len(wantLogRows) {
		t.Fatalf("unexpected log closure: %+v", terminal.Exclusions.LogRowIDs)
	}
	for i, want := range wantLogRows {
		if terminal.Exclusions.LogRowIDs[i] != want {
			t.Fatalf("log closure = %+v, want %+v", terminal.Exclusions.LogRowIDs, wantLogRows)
		}
	}
}

func TestArchiveFinalCheckpointRejectsMissingSequence(t *testing.T) {
	registry := clientdb.NewDBs(t.TempDir())
	defer registry.Close()
	db, err := registry.Open(t.Context(), "main")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	traceID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	checkpoint := core.AgentCheckpoint{
		Version: core.AgentCheckpointVersion, Sequence: 1, AgentID: "agent", Name: "reviewer",
		CallDigest: "xxh3:agent", SnapshotDigest: "xxh3:snapshot", State: core.AgentStateIdle,
	}
	first := archiveCheckpointLog(t, traceID, "aaaaaaaaaaaaaaaa", checkpoint)
	checkpoint.Sequence = 3
	checkpoint.Final = true
	checkpoint.ExpectedFinalSequence = 3
	final := archiveCheckpointLog(t, traceID, "aaaaaaaaaaaaaaaa", checkpoint)
	if _, err := db.AppendLogs([]clientdb.Log{first, final}); err != nil {
		t.Fatal(err)
	}
	cut, err := db.Checkpoint(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = selectFinalAgentCheckpoints(t.Context(), db, traceID, cut.Logs)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("sequence 2 is missing")) {
		t.Fatalf("missing sequence error = %v", err)
	}
}

func TestArchiveFinalCheckpointRejectsMissingParent(t *testing.T) {
	registry := clientdb.NewDBs(t.TempDir())
	defer registry.Close()
	db, err := registry.Open(t.Context(), "main")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	traceID := "dddddddddddddddddddddddddddddddd"
	checkpoint := core.AgentCheckpoint{
		Version: core.AgentCheckpointVersion, Sequence: 1, AgentID: "worker", Name: "worker",
		CallDigest: "xxh3:worker", ParentAgentID: "absent", SnapshotDigest: "xxh3:snapshot", State: core.AgentStateIdle,
	}
	initial := archiveCheckpointLog(t, traceID, "dddddddddddddddd", checkpoint)
	checkpoint.Sequence = 2
	checkpoint.Final = true
	checkpoint.ExpectedFinalSequence = 2
	final := archiveCheckpointLog(t, traceID, "dddddddddddddddd", checkpoint)
	if _, err := db.AppendLogs([]clientdb.Log{initial, final}); err != nil {
		t.Fatal(err)
	}
	cut, err := db.Checkpoint(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = selectFinalAgentCheckpoints(t.Context(), db, traceID, cut.Logs)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte(`references missing parent agent "absent"`)) {
		t.Fatalf("missing parent error = %v", err)
	}
}

func TestArchiveFinalCheckpointRejectsParentCycle(t *testing.T) {
	registry := clientdb.NewDBs(t.TempDir())
	defer registry.Close()
	db, err := registry.Open(t.Context(), "main")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	traceID := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	a := core.AgentCheckpoint{
		Version: core.AgentCheckpointVersion, Sequence: 1, AgentID: "a", Name: "a",
		CallDigest: "xxh3:a", ParentAgentID: "b", SnapshotDigest: "xxh3:snapshot", State: core.AgentStateIdle,
	}
	b := core.AgentCheckpoint{
		Version: core.AgentCheckpointVersion, Sequence: 2, AgentID: "b", Name: "b",
		CallDigest: "xxh3:b", ParentAgentID: "a", SnapshotDigest: "xxh3:snapshot", State: core.AgentStateIdle,
	}
	rows := []clientdb.Log{
		archiveCheckpointLog(t, traceID, "eeeeeeeeeeeeeeee", a),
		archiveCheckpointLog(t, traceID, "ffffffffffffffff", b),
	}
	a.Sequence, a.Final, a.ExpectedFinalSequence = 3, true, 4
	b.Sequence, b.Final, b.ExpectedFinalSequence = 4, true, 4
	rows = append(rows,
		archiveCheckpointLog(t, traceID, "eeeeeeeeeeeeeeee", a),
		archiveCheckpointLog(t, traceID, "ffffffffffffffff", b),
	)
	if _, err := db.AppendLogs(rows); err != nil {
		t.Fatal(err)
	}
	cut, err := db.Checkpoint(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = selectFinalAgentCheckpoints(t.Context(), db, traceID, cut.Logs)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("parent cycle")) {
		t.Fatalf("parent cycle error = %v", err)
	}
}

func TestArchiveBootstrapRepairsCheckpointParentAcrossMissingAncestor(t *testing.T) {
	registry := clientdb.NewDBs(t.TempDir())
	defer registry.Close()
	db, err := registry.Open(t.Context(), "main")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	traceID := "44444444444444444444444444444444"
	chiefSpanID := "aaaaaaaaaaaaaaaa"
	workerSpanID := "bbbbbbbbbbbbbbbb"
	missingToolSpanID := "cccccccccccccccc"
	identityAttrs := func(agentID, name, callDigest string) []byte {
		attrs, err := clientdb.MarshalProtoJSONs(telemetry.KeyValues([]attribute.KeyValue{
			attribute.Bool(telemetryattrs.AgentAttr, true),
			attribute.String(telemetryattrs.AgentIDAttr, agentID),
			attribute.String(telemetryattrs.AgentNameAttr, name),
			attribute.String(telemetryattrs.AgentCallDigestAttr, callDigest),
		}))
		if err != nil {
			t.Fatal(err)
		}
		return attrs
	}
	scope, _ := protojson.Marshal(&otlpcommonv1.InstrumentationScope{})
	resource, _ := protojson.Marshal(&otlpresourcev1.Resource{})
	if _, err := db.AppendSpans([]clientdb.Span{
		{
			TraceID: traceID, SpanID: chiefSpanID, Name: "chief",
			Attributes: identityAttrs("chief", "chief", "xxh3:chief"), InstrumentationScope: scope,
			Resource: resource, Events: []byte("[]"), Links: []byte("[]"),
		},
		{
			TraceID: traceID, SpanID: workerSpanID, ParentSpanID: sql.NullString{String: missingToolSpanID, Valid: true}, Name: "worker",
			Attributes: identityAttrs("worker", "worker", "xxh3:worker"), InstrumentationScope: scope,
			Resource: resource, Events: []byte("[]"), Links: []byte("[]"),
		},
	}); err != nil {
		t.Fatal(err)
	}
	payload, snapshotDigest := archiveCallPayloadLog(t, traceID, chiefSpanID, &callpbv1.Call{Field: "snapshot"})
	chief := core.AgentCheckpoint{
		Version: core.AgentCheckpointVersion, Sequence: 1, AgentID: "chief", Name: "chief",
		CallDigest: "xxh3:chief", SnapshotDigest: snapshotDigest, State: core.AgentStateIdle,
	}
	worker := core.AgentCheckpoint{
		Version: core.AgentCheckpointVersion, Sequence: 2, AgentID: "worker", Name: "worker",
		CallDigest: "xxh3:worker", ParentAgentID: "chief", SnapshotDigest: snapshotDigest, State: core.AgentStateIdle,
	}
	chiefInitial := archiveCheckpointLog(t, traceID, chiefSpanID, chief)
	workerInitial := archiveCheckpointLog(t, traceID, workerSpanID, worker)
	chief.Sequence = 3
	chief.Final = true
	chief.ExpectedFinalSequence = 4
	worker.Sequence = 4
	worker.Final = true
	worker.ExpectedFinalSequence = 4
	if _, err := db.AppendLogs([]clientdb.Log{
		payload, chiefInitial, workerInitial,
		archiveCheckpointLog(t, traceID, chiefSpanID, chief),
		archiveCheckpointLog(t, traceID, workerSpanID, worker),
	}); err != nil {
		t.Fatal(err)
	}
	cut, err := db.Checkpoint(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	data, _, _, err := buildArchiveBootstrap(t.Context(), db, archive.Manifest{
		Version: archive.ManifestVersion, Generation: "generation", TraceID: traceID,
		MainClientID: "main", BoundarySpanID: "5555555555555555",
	}, cut, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	var workerParents [][]byte
	reader := bytes.NewReader(data)
	for {
		kind, payload, err := archive.ReadBootstrapFrame(reader)
		if err != nil {
			t.Fatal(err)
		}
		if kind == archive.BootstrapFrameTerminal {
			break
		}
		if kind != archive.BootstrapFrameTraces {
			continue
		}
		var request coltracepb.ExportTraceServiceRequest
		if err := proto.Unmarshal(payload, &request); err != nil {
			t.Fatal(err)
		}
		for _, resourceSpans := range request.ResourceSpans {
			for _, scopeSpans := range resourceSpans.ScopeSpans {
				for _, span := range scopeSpans.Spans {
					for _, attr := range span.Attributes {
						if attr.GetKey() == telemetryattrs.AgentIDAttr && attr.GetValue().GetStringValue() == "worker" {
							workerParents = append(workerParents, span.ParentSpanId)
						}
					}
				}
			}
		}
	}
	chiefSpanBytes, _ := hex.DecodeString(chiefSpanID)
	if len(workerParents) != 1 || !bytes.Equal(workerParents[0], chiefSpanBytes) {
		t.Fatalf("worker identity parents = %x, want chief span %x", workerParents, chiefSpanBytes)
	}
}

func archiveCallPayloadLog(t *testing.T, traceID, spanID string, callPB *callpbv1.Call) (clientdb.Log, string) {
	t.Helper()
	if callPB.GetDigest() == "" {
		callPB.Digest = digest.FromString(callPB.String()).String()
	}
	payload, err := (proto.MarshalOptions{Deterministic: true}).Marshal(callPB)
	if err != nil {
		t.Fatal(err)
	}
	body, err := proto.Marshal(&otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_BytesValue{BytesValue: payload}})
	if err != nil {
		t.Fatal(err)
	}
	attrs, err := clientdb.MarshalProtoJSONs(telemetry.KeyValues([]attribute.KeyValue{
		attribute.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType),
	}))
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := protojson.Marshal(&otlpcommonv1.InstrumentationScope{Name: "test.core"})
	resource, _ := protojson.Marshal(&otlpresourcev1.Resource{})
	return clientdb.Log{
		TraceID: sql.NullString{String: traceID, Valid: true}, SpanID: sql.NullString{String: spanID, Valid: true},
		Body: body, Attributes: attrs, InstrumentationScope: scope, Resource: resource,
	}, callPB.GetDigest()
}

func archiveCheckpointLog(t *testing.T, traceID, spanID string, checkpoint core.AgentCheckpoint) clientdb.Log {
	t.Helper()
	payload, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	body, err := proto.Marshal(&otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_BytesValue{BytesValue: payload}})
	if err != nil {
		t.Fatal(err)
	}
	attrs, err := clientdb.MarshalProtoJSONs(telemetry.KeyValues([]attribute.KeyValue{
		attribute.Bool(telemetryattrs.AgentCheckpointAttr, true),
		attribute.String(telemetryattrs.AgentCheckpointContractAttr, telemetryattrs.AgentCheckpointContractV1),
		attribute.String(telemetryattrs.AgentCheckpointSequenceAttr, strconv.FormatUint(checkpoint.Sequence, 10)),
		attribute.Bool(telemetryattrs.AgentCheckpointFinalAttr, checkpoint.Final),
	}))
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := protojson.Marshal(&otlpcommonv1.InstrumentationScope{Name: telemetryattrs.AgentCheckpointInstrumentationScope})
	resource, _ := protojson.Marshal(&otlpresourcev1.Resource{})
	return clientdb.Log{
		TraceID: sql.NullString{String: traceID, Valid: true}, SpanID: sql.NullString{String: spanID, Valid: true},
		Body: body, Attributes: attrs, InstrumentationScope: scope, Resource: resource,
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
	cut, err := db.Checkpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	storeSize, err := db.SizeBytes()
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
		HighWater: highWater, SealAt: sealAt, StoreSizeBytes: storeSize, BootstrapBytes: sidecar,
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
