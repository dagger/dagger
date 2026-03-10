package server

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/internal/odag/derive"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestTraceAPIProjection(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	traceIDHex := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	rootSpanHex := "bbbbbbbbbbbbbbbb"
	childSpanHex := "cccccccccccccccc"

	reqPB := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/dagql"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, rootSpanHex),
								Name:              "Query.container",
								StartTimeUnixNano: 10,
								EndTimeUnixNano:   20,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.DagDigestAttr, "call-1"),
									kvString(t, telemetry.DagOutputAttr, "state-a"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-1",
										Field:  "container",
										Type:   &callpbv1.Type{NamedType: "Container"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, childSpanHex),
								ParentSpanId:      mustDecodeHex(t, rootSpanHex),
								Name:              "Container.from",
								StartTimeUnixNano: 25,
								EndTimeUnixNano:   40,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.DagDigestAttr, "call-2"),
									kvString(t, telemetry.DagOutputAttr, "state-b"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-2",
										ReceiverDigest: "state-a",
										Field:          "from",
										Type:           &callpbv1.Type{NamedType: "Container"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
				},
			},
		},
	}

	ingestBody, err := proto.Marshal(reqPB)
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}
	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(ingestBody))
	ingestRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("ingest failed: status=%d body=%s", ingestRec.Code, ingestRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/traces", nil)
	listRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list traces failed: status=%d body=%s", listRec.Code, listRec.Body.String())
	}

	snapReq := httptest.NewRequest(http.MethodGet, "/api/traces/"+traceIDHex+"/snapshot", nil)
	snapRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(snapRec, snapReq)
	if snapRec.Code != http.StatusOK {
		t.Fatalf("snapshot failed: status=%d body=%s", snapRec.Code, snapRec.Body.String())
	}

	var snapResp struct {
		Snapshot struct {
			Objects []struct {
				TypeName     string `json:"typeName"`
				StateHistory []struct {
					StateDigest string `json:"stateDigest"`
				} `json:"stateHistory"`
			} `json:"objects"`
			Events []struct {
				Kind   string `json:"kind"`
				SpanID string `json:"spanID"`
			} `json:"events"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal(snapRec.Body.Bytes(), &snapResp); err != nil {
		t.Fatalf("decode snapshot response: %v", err)
	}

	if len(snapResp.Snapshot.Objects) != 1 {
		t.Fatalf("expected 1 object, got %d", len(snapResp.Snapshot.Objects))
	}
	obj := snapResp.Snapshot.Objects[0]
	if obj.TypeName != "Container" {
		t.Fatalf("unexpected object type: %q", obj.TypeName)
	}
	if len(obj.StateHistory) != 2 {
		t.Fatalf("expected 2 state entries, got %d", len(obj.StateHistory))
	}
	if obj.StateHistory[0].StateDigest != "state-a" || obj.StateHistory[1].StateDigest != "state-b" {
		t.Fatalf("unexpected state history: %#v", obj.StateHistory)
	}
	if len(snapResp.Snapshot.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(snapResp.Snapshot.Events))
	}
	if snapResp.Snapshot.Events[0].Kind != "create" || snapResp.Snapshot.Events[1].Kind != "mutate" {
		t.Fatalf("unexpected event kinds: %#v", snapResp.Snapshot.Events)
	}

	stepReq := httptest.NewRequest(http.MethodGet, "/api/traces/"+traceIDHex+"/snapshot?step=0", nil)
	stepRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(stepRec, stepReq)
	if stepRec.Code != http.StatusOK {
		t.Fatalf("step snapshot failed: status=%d body=%s", stepRec.Code, stepRec.Body.String())
	}

	var stepResp struct {
		Snapshot struct {
			Events []struct {
				Kind   string `json:"kind"`
				SpanID string `json:"spanID"`
			} `json:"events"`
			Objects []struct {
				StateHistory []struct {
					StateDigest string `json:"stateDigest"`
				} `json:"stateHistory"`
			} `json:"objects"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal(stepRec.Body.Bytes(), &stepResp); err != nil {
		t.Fatalf("decode step snapshot response: %v", err)
	}
	if len(stepResp.Snapshot.Events) != 1 {
		t.Fatalf("expected 1 event at step 0, got %d", len(stepResp.Snapshot.Events))
	}
	if stepResp.Snapshot.Events[0].Kind != "create" {
		t.Fatalf("expected step 0 event to be create, got %#v", stepResp.Snapshot.Events[0])
	}
	if len(stepResp.Snapshot.Objects) != 1 || len(stepResp.Snapshot.Objects[0].StateHistory) != 1 {
		t.Fatalf("expected single-state object at step 0, got %#v", stepResp.Snapshot.Objects)
	}

	v2CallsReq := httptest.NewRequest(http.MethodGet, "/api/v2/calls?traceID="+traceIDHex, nil)
	v2CallsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(v2CallsRec, v2CallsReq)
	if v2CallsRec.Code != http.StatusOK {
		t.Fatalf("v2 calls failed: status=%d body=%s", v2CallsRec.Code, v2CallsRec.Body.String())
	}
	var v2CallsResp struct {
		DerivationVersion string `json:"derivationVersion"`
		Items             []struct {
			SpanID           string   `json:"spanID"`
			DerivedOperation string   `json:"derivedOperation"`
			ReceiverDagqlID  string   `json:"receiverDagqlID"`
			ReceiverIsQuery  bool     `json:"receiverIsQuery"`
			ArgDagqlIDs      []string `json:"argDagqlIDs"`
			OutputDagqlID    string   `json:"outputDagqlID"`
		} `json:"items"`
	}
	if err := json.Unmarshal(v2CallsRec.Body.Bytes(), &v2CallsResp); err != nil {
		t.Fatalf("decode v2 calls: %v", err)
	}
	if v2CallsResp.DerivationVersion == "" || len(v2CallsResp.Items) != 2 {
		t.Fatalf("unexpected v2 calls response: %#v", v2CallsResp)
	}
	if v2CallsResp.Items[0].DerivedOperation != "create" || v2CallsResp.Items[1].DerivedOperation != "mutate" {
		t.Fatalf("unexpected v2 call operations: %#v", v2CallsResp.Items)
	}
	if !v2CallsResp.Items[0].ReceiverIsQuery || v2CallsResp.Items[0].ReceiverDagqlID != "" || v2CallsResp.Items[0].OutputDagqlID != "state-a" {
		t.Fatalf("unexpected Query receiver metadata: %#v", v2CallsResp.Items[0])
	}
	if v2CallsResp.Items[1].ReceiverIsQuery || v2CallsResp.Items[1].ReceiverDagqlID != "state-a" || v2CallsResp.Items[1].OutputDagqlID != "state-b" {
		t.Fatalf("unexpected nested receiver metadata: %#v", v2CallsResp.Items[1])
	}

	v2BindingsReq := httptest.NewRequest(http.MethodGet, "/api/v2/object-bindings?traceID="+traceIDHex, nil)
	v2BindingsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(v2BindingsRec, v2BindingsReq)
	if v2BindingsRec.Code != http.StatusOK {
		t.Fatalf("v2 object-bindings failed: status=%d body=%s", v2BindingsRec.Code, v2BindingsRec.Body.String())
	}
	var v2BindingsResp struct {
		Items []struct {
			BindingID string   `json:"bindingID"`
			ObjectID  string   `json:"objectID"`
			TraceID   string   `json:"traceID"`
			Archived  bool     `json:"archived"`
			History   []string `json:"dagqlHistory"`
		} `json:"items"`
	}
	if err := json.Unmarshal(v2BindingsRec.Body.Bytes(), &v2BindingsResp); err != nil {
		t.Fatalf("decode v2 object-bindings: %v", err)
	}
	if len(v2BindingsResp.Items) != 1 {
		t.Fatalf("expected 1 v2 binding, got %#v", v2BindingsResp.Items)
	}
	if v2BindingsResp.Items[0].TraceID != traceIDHex || len(v2BindingsResp.Items[0].History) != 2 || v2BindingsResp.Items[0].ObjectID == "" {
		t.Fatalf("unexpected v2 binding content: %#v", v2BindingsResp.Items[0])
	}

	v2SnapshotsReq := httptest.NewRequest(http.MethodGet, "/api/v2/object-snapshots?traceID="+traceIDHex, nil)
	v2SnapshotsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(v2SnapshotsRec, v2SnapshotsReq)
	if v2SnapshotsRec.Code != http.StatusOK {
		t.Fatalf("v2 object-snapshots failed: status=%d body=%s", v2SnapshotsRec.Code, v2SnapshotsRec.Body.String())
	}
	var v2SnapshotsResp struct {
		Items []struct {
			DagqlID string `json:"dagqlID"`
		} `json:"items"`
	}
	if err := json.Unmarshal(v2SnapshotsRec.Body.Bytes(), &v2SnapshotsResp); err != nil {
		t.Fatalf("decode v2 object-snapshots: %v", err)
	}
	if len(v2SnapshotsResp.Items) != 2 {
		t.Fatalf("expected 2 v2 snapshots, got %#v", v2SnapshotsResp.Items)
	}

	v2MutationsReq := httptest.NewRequest(http.MethodGet, "/api/v2/mutations?traceID="+traceIDHex, nil)
	v2MutationsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(v2MutationsRec, v2MutationsReq)
	if v2MutationsRec.Code != http.StatusOK {
		t.Fatalf("v2 mutations failed: status=%d body=%s", v2MutationsRec.Code, v2MutationsRec.Body.String())
	}
	var v2MutationsResp struct {
		Items []struct {
			Kind string `json:"kind"`
		} `json:"items"`
	}
	if err := json.Unmarshal(v2MutationsRec.Body.Bytes(), &v2MutationsResp); err != nil {
		t.Fatalf("decode v2 mutations: %v", err)
	}
	if len(v2MutationsResp.Items) != 2 || v2MutationsResp.Items[0].Kind != "create" || v2MutationsResp.Items[1].Kind != "mutate" {
		t.Fatalf("unexpected v2 mutations: %#v", v2MutationsResp.Items)
	}

	v2SpansReq := httptest.NewRequest(http.MethodGet, "/api/v2/spans?traceID="+traceIDHex, nil)
	v2SpansRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(v2SpansRec, v2SpansReq)
	if v2SpansRec.Code != http.StatusOK {
		t.Fatalf("v2 spans failed: status=%d body=%s", v2SpansRec.Code, v2SpansRec.Body.String())
	}
	var v2SpansResp struct {
		Items []struct {
			TraceID string `json:"traceID"`
			SpanID  string `json:"spanID"`
		} `json:"items"`
	}
	if err := json.Unmarshal(v2SpansRec.Body.Bytes(), &v2SpansResp); err != nil {
		t.Fatalf("decode v2 spans: %v", err)
	}
	if len(v2SpansResp.Items) != 2 {
		t.Fatalf("expected 2 v2 spans, got %#v", v2SpansResp.Items)
	}
}

func TestV2DerivesSessionsClientsAndCallOwnership(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	traceIDHex := "abababababababababababababababab"
	rootConnectHex := "1111111111111111"
	rootWorkHex := "2222222222222222"
	rootCallHex := "3333333333333333"
	childConnectHex := "4444444444444444"
	childCallHex := "5555555555555555"

	reqPB := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						kvString(t, "service.name", "dagger-cli"),
					},
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/engine.client"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, rootConnectHex),
								Name:              "connect",
								StartTimeUnixNano: 1,
								EndTimeUnixNano:   2,
								Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/http"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, rootWorkHex),
								Name:              "POST /query",
								StartTimeUnixNano: 5,
								EndTimeUnixNano:   30,
								Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/dagql"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, rootCallHex),
								Name:              "Query.container",
								StartTimeUnixNano: 10,
								EndTimeUnixNano:   11,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.DagDigestAttr, "call-root"),
									kvString(t, telemetry.DagOutputAttr, "state-root"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-root",
										Field:  "container",
										Type:   &callpbv1.Type{NamedType: "Container"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
				},
			},
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						kvString(t, "service.name", "module-client"),
					},
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/engine.client"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, childConnectHex),
								ParentSpanId:      mustDecodeHex(t, rootWorkHex),
								Name:              "connect",
								StartTimeUnixNano: 12,
								EndTimeUnixNano:   13,
								Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/dagql"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, childCallHex),
								Name:              "Container.withExec",
								StartTimeUnixNano: 15,
								EndTimeUnixNano:   16,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.DagDigestAttr, "call-child"),
									kvString(t, telemetry.DagOutputAttr, "state-child"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-child",
										ReceiverDigest: "state-root",
										Field:          "withExec",
										Type:           &callpbv1.Type{NamedType: "Container"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
				},
			},
		},
	}

	ingestBody, err := proto.Marshal(reqPB)
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}
	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(ingestBody))
	ingestRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("ingest failed: status=%d body=%s", ingestRec.Code, ingestRec.Body.String())
	}

	rootClientID := derive.ClientID(traceIDHex, rootConnectHex)
	childClientID := derive.ClientID(traceIDHex, childConnectHex)
	sessionID := derive.SessionID(rootClientID)

	sessionsReq := httptest.NewRequest(http.MethodGet, "/api/sessions?traceID="+traceIDHex, nil)
	sessionsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(sessionsRec, sessionsReq)
	if sessionsRec.Code != http.StatusOK {
		t.Fatalf("v2 sessions failed: status=%d body=%s", sessionsRec.Code, sessionsRec.Body.String())
	}
	var sessionsResp struct {
		Items []struct {
			ID           string `json:"id"`
			RootClientID string `json:"rootClientID"`
		} `json:"items"`
	}
	if err := json.Unmarshal(sessionsRec.Body.Bytes(), &sessionsResp); err != nil {
		t.Fatalf("decode v2 sessions: %v", err)
	}
	if len(sessionsResp.Items) != 1 || sessionsResp.Items[0].ID != sessionID || sessionsResp.Items[0].RootClientID != rootClientID {
		t.Fatalf("unexpected sessions response: %#v", sessionsResp.Items)
	}

	clientsReq := httptest.NewRequest(http.MethodGet, "/api/v2/clients?traceID="+traceIDHex, nil)
	clientsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(clientsRec, clientsReq)
	if clientsRec.Code != http.StatusOK {
		t.Fatalf("v2 clients failed: status=%d body=%s", clientsRec.Code, clientsRec.Body.String())
	}
	var clientsResp struct {
		Items []struct {
			ID             string `json:"id"`
			SessionID      string `json:"sessionID"`
			ParentClientID string `json:"parentClientID"`
		} `json:"items"`
	}
	if err := json.Unmarshal(clientsRec.Body.Bytes(), &clientsResp); err != nil {
		t.Fatalf("decode v2 clients: %v", err)
	}
	if len(clientsResp.Items) != 2 {
		t.Fatalf("expected 2 clients, got %#v", clientsResp.Items)
	}
	byID := map[string]struct {
		SessionID      string
		ParentClientID string
	}{}
	for _, item := range clientsResp.Items {
		byID[item.ID] = struct {
			SessionID      string
			ParentClientID string
		}{
			SessionID:      item.SessionID,
			ParentClientID: item.ParentClientID,
		}
	}
	if byID[rootClientID].SessionID != sessionID || byID[rootClientID].ParentClientID != "" {
		t.Fatalf("unexpected root client row: %#v", byID[rootClientID])
	}
	if byID[childClientID].SessionID != sessionID || byID[childClientID].ParentClientID != rootClientID {
		t.Fatalf("unexpected child client row: %#v", byID[childClientID])
	}

	callsReq := httptest.NewRequest(http.MethodGet, "/api/v2/calls?traceID="+traceIDHex, nil)
	callsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(callsRec, callsReq)
	if callsRec.Code != http.StatusOK {
		t.Fatalf("v2 calls failed: status=%d body=%s", callsRec.Code, callsRec.Body.String())
	}
	var callsResp struct {
		Items []struct {
			SpanID    string `json:"spanID"`
			SessionID string `json:"sessionID"`
			ClientID  string `json:"clientID"`
		} `json:"items"`
	}
	if err := json.Unmarshal(callsRec.Body.Bytes(), &callsResp); err != nil {
		t.Fatalf("decode v2 calls: %v", err)
	}
	if len(callsResp.Items) != 2 {
		t.Fatalf("expected 2 calls, got %#v", callsResp.Items)
	}
	callOwners := map[string]struct {
		SessionID string
		ClientID  string
	}{}
	for _, item := range callsResp.Items {
		callOwners[item.SpanID] = struct {
			SessionID string
			ClientID  string
		}{
			SessionID: item.SessionID,
			ClientID:  item.ClientID,
		}
	}
	if callOwners[rootCallHex].ClientID != rootClientID || callOwners[rootCallHex].SessionID != sessionID {
		t.Fatalf("unexpected root call owner: %#v", callOwners[rootCallHex])
	}
	if callOwners[childCallHex].ClientID != childClientID || callOwners[childCallHex].SessionID != sessionID {
		t.Fatalf("unexpected child call owner: %#v", callOwners[childCallHex])
	}

	filteredReq := httptest.NewRequest(http.MethodGet, "/api/v2/calls?clientID="+childClientID, nil)
	filteredRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(filteredRec, filteredReq)
	if filteredRec.Code != http.StatusOK {
		t.Fatalf("filtered v2 calls failed: status=%d body=%s", filteredRec.Code, filteredRec.Body.String())
	}
	var filteredResp struct {
		Items []struct {
			SpanID string `json:"spanID"`
		} `json:"items"`
	}
	if err := json.Unmarshal(filteredRec.Body.Bytes(), &filteredResp); err != nil {
		t.Fatalf("decode filtered calls: %v", err)
	}
	if len(filteredResp.Items) != 1 || filteredResp.Items[0].SpanID != childCallHex {
		t.Fatalf("unexpected filtered calls: %#v", filteredResp.Items)
	}
}

func TestV2APIUsesExplicitExecutionScopeIDs(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	trace1Hex := "11111111111111111111111111111111"
	trace2Hex := "22222222222222222222222222222222"
	connect1Hex := "aaaaaaaaaaaaaaaa"
	connect2Hex := "bbbbbbbbbbbbbbbb"
	call1Hex := "cccccccccccccccc"
	call2Hex := "dddddddddddddddd"

	sessionID := "session-explicit"
	clientID := "client-explicit"

	reqPB := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/engine.client"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, trace1Hex),
								SpanId:            mustDecodeHex(t, connect1Hex),
								Name:              "connect",
								StartTimeUnixNano: 1,
								EndTimeUnixNano:   2,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.EngineClientKindAttr, "root"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, trace2Hex),
								SpanId:            mustDecodeHex(t, connect2Hex),
								Name:              "connect",
								StartTimeUnixNano: 101,
								EndTimeUnixNano:   102,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.EngineClientKindAttr, "root"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/dagql"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, trace1Hex),
								SpanId:            mustDecodeHex(t, call1Hex),
								ParentSpanId:      mustDecodeHex(t, connect1Hex),
								Name:              "Query.container",
								StartTimeUnixNano: 10,
								EndTimeUnixNano:   20,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-1"),
									kvString(t, telemetry.DagOutputAttr, "state-1"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-1",
										Field:  "container",
										Type:   &callpbv1.Type{NamedType: "Container"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, trace2Hex),
								SpanId:            mustDecodeHex(t, call2Hex),
								ParentSpanId:      mustDecodeHex(t, connect2Hex),
								Name:              "Query.directory",
								StartTimeUnixNano: 110,
								EndTimeUnixNano:   120,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-2"),
									kvString(t, telemetry.DagOutputAttr, "state-2"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-2",
										Field:  "directory",
										Type:   &callpbv1.Type{NamedType: "Directory"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
				},
			},
		},
	}

	ingestBody, err := proto.Marshal(reqPB)
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}
	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(ingestBody))
	ingestRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("ingest failed: status=%d body=%s", ingestRec.Code, ingestRec.Body.String())
	}

	callsReq := httptest.NewRequest(http.MethodGet, "/api/v2/calls?sessionID="+sessionID, nil)
	callsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(callsRec, callsReq)
	if callsRec.Code != http.StatusOK {
		t.Fatalf("calls by explicit session failed: status=%d body=%s", callsRec.Code, callsRec.Body.String())
	}
	var callsResp struct {
		Items []struct {
			TraceID   string `json:"traceID"`
			SessionID string `json:"sessionID"`
			ClientID  string `json:"clientID"`
			SpanID    string `json:"spanID"`
		} `json:"items"`
	}
	if err := json.Unmarshal(callsRec.Body.Bytes(), &callsResp); err != nil {
		t.Fatalf("decode explicit-session calls: %v", err)
	}
	if len(callsResp.Items) != 2 {
		t.Fatalf("expected 2 calls across explicit-session traces, got %#v", callsResp.Items)
	}
	for _, item := range callsResp.Items {
		if item.SessionID != sessionID || item.ClientID != clientID {
			t.Fatalf("unexpected explicit-session call item: %#v", item)
		}
	}

	clientsReq := httptest.NewRequest(http.MethodGet, "/api/v2/clients?traceID="+trace1Hex, nil)
	clientsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(clientsRec, clientsReq)
	if clientsRec.Code != http.StatusOK {
		t.Fatalf("clients by trace failed: status=%d body=%s", clientsRec.Code, clientsRec.Body.String())
	}
	var clientsResp struct {
		Items []struct {
			ID         string `json:"id"`
			SessionID  string `json:"sessionID"`
			ClientKind string `json:"clientKind"`
		} `json:"items"`
	}
	if err := json.Unmarshal(clientsRec.Body.Bytes(), &clientsResp); err != nil {
		t.Fatalf("decode explicit clients: %v", err)
	}
	if len(clientsResp.Items) != 1 {
		t.Fatalf("expected 1 explicit client, got %#v", clientsResp.Items)
	}
	if clientsResp.Items[0].ID != clientID || clientsResp.Items[0].SessionID != sessionID || clientsResp.Items[0].ClientKind != "root" {
		t.Fatalf("unexpected explicit client payload: %#v", clientsResp.Items[0])
	}
}

func TestV2CLIRuns(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	traceIDHex := "33333333333333333333333333333333"
	connectHex := "1111111111111111"
	dirCallHex := "2222222222222222"
	changesHex := "3333333333333333"
	analyzeHex := "4444444444444444"
	applyHex := "5555555555555555"

	sessionID := "session-cli-run"
	clientID := "client-cli-run"
	commandArgs := []string{"/usr/local/bin/dagger", "call", "directory", "changes", "--auto-apply"}

	reqPB := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						kvString(t, "service.name", "dagger-cli"),
						kvStringList(t, "process.command_args", commandArgs),
					},
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/engine.client"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, connectHex),
								Name:              "connect",
								StartTimeUnixNano: 1,
								EndTimeUnixNano:   2,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.EngineClientKindAttr, "root"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/dagql"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, dirCallHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Query.directory",
								StartTimeUnixNano: 10,
								EndTimeUnixNano:   20,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-dir"),
									kvString(t, telemetry.DagOutputAttr, "state-dir"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-dir",
										Field:  "directory",
										Type:   &callpbv1.Type{NamedType: "Directory"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, changesHex),
								ParentSpanId:      mustDecodeHex(t, dirCallHex),
								Name:              "Directory.changes",
								StartTimeUnixNano: 30,
								EndTimeUnixNano:   40,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-changes"),
									kvString(t, telemetry.DagOutputAttr, "state-changes"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-changes",
										ReceiverDigest: "state-dir",
										Field:          "changes",
										Type:           &callpbv1.Type{NamedType: "Changeset"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/cli"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, analyzeHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "analyzing changes",
								StartTimeUnixNano: 50,
								EndTimeUnixNano:   60,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, applyHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "applying changes",
								StartTimeUnixNano: 61,
								EndTimeUnixNano:   75,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR},
							},
						},
					},
				},
			},
		},
	}

	ingestBody, err := proto.Marshal(reqPB)
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}
	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(ingestBody))
	ingestRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("ingest failed: status=%d body=%s", ingestRec.Code, ingestRec.Body.String())
	}

	runsReq := httptest.NewRequest(http.MethodGet, "/api/pipelines?traceID="+traceIDHex, nil)
	runsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(runsRec, runsReq)
	if runsRec.Code != http.StatusOK {
		t.Fatalf("cli-runs failed: status=%d body=%s", runsRec.Code, runsRec.Body.String())
	}

	var runsResp struct {
		DerivationVersion string `json:"derivationVersion"`
		Items             []struct {
			ID                    string   `json:"id"`
			TraceID               string   `json:"traceID"`
			SessionID             string   `json:"sessionID"`
			ClientID              string   `json:"clientID"`
			Name                  string   `json:"name"`
			ChainLabel            string   `json:"chainLabel"`
			Status                string   `json:"status"`
			ChainDepth            int      `json:"chainDepth"`
			CallCount             int      `json:"callCount"`
			TerminalCallName      string   `json:"terminalCallName"`
			TerminalReturnType    string   `json:"terminalReturnType"`
			TerminalOutputDagqlID string   `json:"terminalOutputDagqlID"`
			FollowupSpanCount     int      `json:"followupSpanCount"`
			PostProcessKinds      []string `json:"postProcessKinds"`
			Evidence              []struct {
				Kind string `json:"kind"`
				Note string `json:"note"`
			} `json:"evidence"`
			Relations []struct {
				Relation string `json:"relation"`
				Target   string `json:"target"`
			} `json:"relations"`
		} `json:"items"`
	}
	if err := json.Unmarshal(runsRec.Body.Bytes(), &runsResp); err != nil {
		t.Fatalf("decode cli-runs: %v", err)
	}
	if runsResp.DerivationVersion == "" || len(runsResp.Items) != 1 {
		t.Fatalf("unexpected cli-runs response: %#v", runsResp)
	}

	run := runsResp.Items[0]
	if run.TraceID != traceIDHex || run.SessionID != sessionID || run.ClientID != clientID {
		t.Fatalf("unexpected cli-run scope: %#v", run)
	}
	if run.ChainLabel == "" || !strings.Contains(run.ChainLabel, "directory") {
		t.Fatalf("expected chain label, got %#v", run)
	}
	if run.Name == "" || run.TerminalCallName != "Directory.changes" || run.TerminalReturnType != "Changeset" || run.TerminalOutputDagqlID != "state-changes" {
		t.Fatalf("unexpected terminal run fields: %#v", run)
	}
	if run.Status != "ready" || run.ChainDepth != 2 || run.CallCount != 2 || run.FollowupSpanCount != 2 {
		t.Fatalf("unexpected cli-run counts: %#v", run)
	}
	if !sameStrings(run.PostProcessKinds, []string{"changeset-apply", "changeset-auto-apply", "changeset-preview"}) {
		t.Fatalf("unexpected post-process kinds: %#v", run.PostProcessKinds)
	}
	if len(run.Evidence) < 4 {
		t.Fatalf("expected evidence rows, got %#v", run.Evidence)
	}
	if len(run.Relations) < 3 {
		t.Fatalf("expected relations, got %#v", run.Relations)
	}
}

func TestV2CLIRunsIgnoreModulePreludeBeforeParseBoundary(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	traceIDHex := "34343434343434343434343434343434"
	sessionID := "session-cli-module"
	clientID := "client-cli-module"

	connectHex := "1111111111111111"
	moduleSourceHex := "2222222222222222"
	moduleDigestHex := "3333333333333333"
	parseHex := "4444444444444444"
	wolfiHex := "5555555555555555"
	containerHex := "6666666666666666"

	commandArgs := []string{"/usr/local/bin/dagger", "call", "-m", "github.com/dagger/dagger/modules/wolfi", "container"}

	reqPB := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						kvString(t, "service.name", "dagger-cli"),
						kvStringList(t, "process.command_args", commandArgs),
					},
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/engine.client"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, connectHex),
								Name:              "connect",
								StartTimeUnixNano: 1,
								EndTimeUnixNano:   2,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.EngineClientKindAttr, "root"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/dagql"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, moduleSourceHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Query.moduleSource",
								StartTimeUnixNano: 10,
								EndTimeUnixNano:   20,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-module-source"),
									kvString(t, telemetry.DagOutputAttr, "state-module-source"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-module-source",
										Field:  "moduleSource",
										Type:   &callpbv1.Type{NamedType: "ModuleSource"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, moduleDigestHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "ModuleSource.digest",
								StartTimeUnixNano: 21,
								EndTimeUnixNano:   25,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-module-digest"),
									kvString(t, telemetry.DagOutputAttr, "state-module-digest"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-module-digest",
										ReceiverDigest: "state-module-source",
										Field:          "digest",
										Type:           &callpbv1.Type{NamedType: "String"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, wolfiHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Query.wolfi",
								StartTimeUnixNano: 40,
								EndTimeUnixNano:   45,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-wolfi"),
									kvString(t, telemetry.DagOutputAttr, "state-wolfi"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-wolfi",
										Field:  "wolfi",
										Type:   &callpbv1.Type{NamedType: "Wolfi"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, containerHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Wolfi.container",
								StartTimeUnixNano: 46,
								EndTimeUnixNano:   60,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-container"),
									kvString(t, telemetry.DagOutputAttr, "state-container"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-container",
										ReceiverDigest: "state-wolfi",
										Field:          "container",
										Type:           &callpbv1.Type{NamedType: "Container"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/cli"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, parseHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "parsing command line arguments",
								StartTimeUnixNano: 30,
								EndTimeUnixNano:   35,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
				},
			},
		},
	}

	ingestBody, err := proto.Marshal(reqPB)
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}
	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(ingestBody))
	ingestRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("ingest failed: status=%d body=%s", ingestRec.Code, ingestRec.Body.String())
	}

	runsReq := httptest.NewRequest(http.MethodGet, "/api/pipelines?traceID="+traceIDHex, nil)
	runsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(runsRec, runsReq)
	if runsRec.Code != http.StatusOK {
		t.Fatalf("cli-runs failed: status=%d body=%s", runsRec.Code, runsRec.Body.String())
	}

	var runsResp struct {
		Items []struct {
			CallCount          int    `json:"callCount"`
			ChainDepth         int    `json:"chainDepth"`
			TerminalCallName   string `json:"terminalCallName"`
			TerminalReturnType string `json:"terminalReturnType"`
			Name               string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(runsRec.Body.Bytes(), &runsResp); err != nil {
		t.Fatalf("decode cli-runs: %v", err)
	}
	if len(runsResp.Items) != 1 {
		t.Fatalf("expected 1 cli-run, got %#v", runsResp.Items)
	}
	run := runsResp.Items[0]
	if run.CallCount != 2 || run.ChainDepth != 2 {
		t.Fatalf("expected post-parse calls only, got %#v", run)
	}
	if run.TerminalCallName != "Wolfi.container" || run.TerminalReturnType != "Container" {
		t.Fatalf("unexpected terminal pipeline call: %#v", run)
	}
	if !strings.Contains(run.Name, "container") {
		t.Fatalf("unexpected pipeline name: %#v", run)
	}
}

func TestV2CLIRunsSkipFailedParseWithoutUserCalls(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	traceIDHex := "35353535353535353535353535353535"
	sessionID := "session-cli-parse-fail"
	clientID := "client-cli-parse-fail"

	connectHex := "1111111111111111"
	moduleSourceHex := "2222222222222222"
	moduleDigestHex := "3333333333333333"
	parseHex := "4444444444444444"

	commandArgs := []string{"/usr/local/bin/dagger", "call", "container", "from", "alpine"}

	reqPB := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						kvString(t, "service.name", "dagger-cli"),
						kvStringList(t, "process.command_args", commandArgs),
					},
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/engine.client"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, connectHex),
								Name:              "connect",
								StartTimeUnixNano: 1,
								EndTimeUnixNano:   2,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.EngineClientKindAttr, "root"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/dagql"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, moduleSourceHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Query.moduleSource",
								StartTimeUnixNano: 10,
								EndTimeUnixNano:   20,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-module-source"),
									kvString(t, telemetry.DagOutputAttr, "state-module-source"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-module-source",
										Field:  "moduleSource",
										Type:   &callpbv1.Type{NamedType: "ModuleSource"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, moduleDigestHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "ModuleSource.digest",
								StartTimeUnixNano: 21,
								EndTimeUnixNano:   25,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-module-digest"),
									kvString(t, telemetry.DagOutputAttr, "state-module-digest"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-module-digest",
										ReceiverDigest: "state-module-source",
										Field:          "digest",
										Type:           &callpbv1.Type{NamedType: "String"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/cli"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, parseHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "parsing command line arguments",
								StartTimeUnixNano: 30,
								EndTimeUnixNano:   35,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR},
							},
						},
					},
				},
			},
		},
	}

	ingestBody, err := proto.Marshal(reqPB)
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}
	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(ingestBody))
	ingestRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("ingest failed: status=%d body=%s", ingestRec.Code, ingestRec.Body.String())
	}

	runsReq := httptest.NewRequest(http.MethodGet, "/api/pipelines?traceID="+traceIDHex, nil)
	runsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(runsRec, runsReq)
	if runsRec.Code != http.StatusOK {
		t.Fatalf("cli-runs failed: status=%d body=%s", runsRec.Code, runsRec.Body.String())
	}

	var runsResp struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(runsRec.Body.Bytes(), &runsResp); err != nil {
		t.Fatalf("decode cli-runs: %v", err)
	}
	if len(runsResp.Items) != 0 {
		t.Fatalf("expected no pipelines for parse-only failure, got %s", runsRec.Body.String())
	}
}

func TestV2CLIRunsSkipModulePreludeOnlyRuns(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	traceIDHex := "36363636363636363636363636363636"
	sessionID := "session-cli-module-only"
	clientID := "client-cli-module-only"

	connectHex := "1111111111111111"
	moduleSourceHex := "2222222222222222"

	commandArgs := []string{"/usr/local/bin/dagger", "call", "-m", "github.com/dagger/dagger", "engine-dev", "playground", "terminal"}

	reqPB := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						kvString(t, "service.name", "dagger-cli"),
						kvStringList(t, "process.command_args", commandArgs),
					},
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/engine.client"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, connectHex),
								Name:              "connect",
								StartTimeUnixNano: 1,
								EndTimeUnixNano:   2,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.EngineClientKindAttr, "root"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/dagql"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, moduleSourceHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Query.moduleSource",
								StartTimeUnixNano: 10,
								EndTimeUnixNano:   20,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-module-source"),
									kvString(t, telemetry.DagOutputAttr, "state-module-source"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-module-source",
										Field:  "moduleSource",
										Type:   &callpbv1.Type{NamedType: "ModuleSource"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
				},
			},
		},
	}

	ingestBody, err := proto.Marshal(reqPB)
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}
	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(ingestBody))
	ingestRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("ingest failed: status=%d body=%s", ingestRec.Code, ingestRec.Body.String())
	}

	runsReq := httptest.NewRequest(http.MethodGet, "/api/pipelines?traceID="+traceIDHex, nil)
	runsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(runsRec, runsReq)
	if runsRec.Code != http.StatusOK {
		t.Fatalf("cli-runs failed: status=%d body=%s", runsRec.Code, runsRec.Body.String())
	}

	var runsResp struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(runsRec.Body.Bytes(), &runsResp); err != nil {
		t.Fatalf("decode cli-runs: %v", err)
	}
	if len(runsResp.Items) != 0 {
		t.Fatalf("expected no pipelines for module-prelude-only trace, got %s", runsRec.Body.String())
	}
}

func TestV2WorkspaceOps(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	traceIDHex := "36363636363636363636363636363636"
	sessionID := "session-workspace-ops"
	clientID := "client-workspace-ops"

	connectHex := "1111111111111111"
	hostDirHex := "2222222222222222"
	parseHex := "3333333333333333"
	dirCallHex := "4444444444444444"
	exportHex := "5555555555555555"

	commandArgs := []string{"/usr/local/bin/dagger", "call", "directory", "export", "--path", "./out"}

	reqPB := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						kvString(t, "service.name", "dagger-cli"),
						kvStringList(t, "process.command_args", commandArgs),
					},
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/engine.client"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, connectHex),
								Name:              "connect",
								StartTimeUnixNano: 1,
								EndTimeUnixNano:   2,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.EngineClientKindAttr, "root"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/dagql"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, hostDirHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Host.directory",
								StartTimeUnixNano: 10,
								EndTimeUnixNano:   18,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-host-directory"),
									kvString(t, telemetry.DagOutputAttr, "state-host-directory"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-host-directory",
										Field:  "directory",
										Type:   &callpbv1.Type{NamedType: "Directory"},
										Args: []*callpbv1.Argument{
											{
												Name:  "path",
												Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: "/tmp/ws"}},
											},
										},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, dirCallHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Query.directory",
								StartTimeUnixNano: 30,
								EndTimeUnixNano:   35,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-directory"),
									kvString(t, telemetry.DagOutputAttr, "state-directory"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-directory",
										Field:  "directory",
										Type:   &callpbv1.Type{NamedType: "Directory"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, exportHex),
								ParentSpanId:      mustDecodeHex(t, dirCallHex),
								Name:              "Directory.export",
								StartTimeUnixNano: 36,
								EndTimeUnixNano:   48,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-export"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-export",
										ReceiverDigest: "state-directory",
										Field:          "export",
										Type:           &callpbv1.Type{NamedType: "Void"},
										Args: []*callpbv1.Argument{
											{
												Name:  "path",
												Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: "./out"}},
											},
										},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/cli"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, parseHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "parsing command line arguments",
								StartTimeUnixNano: 20,
								EndTimeUnixNano:   25,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
				},
			},
		},
	}

	ingestBody, err := proto.Marshal(reqPB)
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}
	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(ingestBody))
	ingestRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("ingest failed: status=%d body=%s", ingestRec.Code, ingestRec.Body.String())
	}

	opsReq := httptest.NewRequest(http.MethodGet, "/api/workspace-ops?traceID="+traceIDHex, nil)
	opsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(opsRec, opsReq)
	if opsRec.Code != http.StatusOK {
		t.Fatalf("workspace ops failed: status=%d body=%s", opsRec.Code, opsRec.Body.String())
	}

	var opsResp struct {
		Items []struct {
			Kind             string `json:"kind"`
			Direction        string `json:"direction"`
			CallName         string `json:"callName"`
			Path             string `json:"path"`
			SessionID        string `json:"sessionID"`
			PipelineID       string `json:"pipelineID"`
			PipelineClientID string `json:"pipelineClientID"`
			PipelineCommand  string `json:"pipelineCommand"`
		} `json:"items"`
	}
	if err := json.Unmarshal(opsRec.Body.Bytes(), &opsResp); err != nil {
		t.Fatalf("decode workspace ops: %v", err)
	}
	if len(opsResp.Items) != 2 {
		t.Fatalf("expected 2 workspace ops, got %#v", opsResp.Items)
	}

	hostOp := opsResp.Items[0]
	if hostOp.Kind != "host-directory" || hostOp.Direction != "read" || hostOp.CallName != "Host.directory" || hostOp.Path != "/tmp/ws" {
		t.Fatalf("unexpected host workspace op: %#v", hostOp)
	}
	if hostOp.PipelineID != "" || hostOp.PipelineCommand != "" {
		t.Fatalf("expected pre-parse host op to stay detached from pipeline: %#v", hostOp)
	}

	exportOp := opsResp.Items[1]
	if exportOp.Kind != "directory-export" || exportOp.Direction != "write" || exportOp.CallName != "Directory.export" || exportOp.Path != "./out" {
		t.Fatalf("unexpected export workspace op: %#v", exportOp)
	}
	if exportOp.SessionID != sessionID || exportOp.PipelineClientID != clientID {
		t.Fatalf("unexpected export op scope: %#v", exportOp)
	}
	if exportOp.PipelineID == "" || !strings.Contains(exportOp.PipelineCommand, "directory export") {
		t.Fatalf("expected export op to attach to pipeline: %#v", exportOp)
	}
}

func TestV2Workspaces(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	traceIDHex := "38383838383838383838383838383838"
	sessionID := "session-workspaces"
	clientID := "client-workspaces"

	connectHex := "1111111111111111"
	hostRootAHex := "2222222222222222"
	hostRootBHex := "3333333333333333"
	hostChangesHex := "4444444444444444"
	parseHex := "5555555555555555"
	dirCallHex := "6666666666666666"
	exportHex := "7777777777777777"

	commandArgs := []string{"/usr/local/bin/dagger", "call", "directory", "export", "--path", "./out"}

	reqPB := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						kvString(t, "service.name", "dagger-cli"),
						kvStringList(t, "process.command_args", commandArgs),
					},
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/engine.client"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, connectHex),
								Name:              "connect",
								StartTimeUnixNano: 1,
								EndTimeUnixNano:   2,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.EngineClientKindAttr, "root"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/dagql"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, hostRootAHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Host.directory",
								StartTimeUnixNano: 10,
								EndTimeUnixNano:   18,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvBool(t, telemetry.UIInternalAttr, true),
									kvString(t, telemetry.DagDigestAttr, "call-host-directory-a"),
									kvString(t, telemetry.DagOutputAttr, "state-host-directory-a"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-host-directory-a",
										Field:  "directory",
										Type:   &callpbv1.Type{NamedType: "Directory"},
										Args: []*callpbv1.Argument{
											{
												Name:  "path",
												Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: "/tmp/ws"}},
											},
										},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, hostRootBHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Host.directory",
								StartTimeUnixNano: 19,
								EndTimeUnixNano:   27,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvBool(t, telemetry.UIInternalAttr, true),
									kvString(t, telemetry.DagDigestAttr, "call-host-directory-b"),
									kvString(t, telemetry.DagOutputAttr, "state-host-directory-b"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-host-directory-b",
										Field:  "directory",
										Type:   &callpbv1.Type{NamedType: "Directory"},
										Args: []*callpbv1.Argument{
											{
												Name:  "path",
												Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: "/tmp/ws"}},
											},
										},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, hostChangesHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Host.directory",
								StartTimeUnixNano: 28,
								EndTimeUnixNano:   35,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvBool(t, telemetry.UIInternalAttr, true),
									kvString(t, telemetry.DagDigestAttr, "call-host-directory-c"),
									kvString(t, telemetry.DagOutputAttr, "state-host-directory-c"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-host-directory-c",
										Field:  "directory",
										Type:   &callpbv1.Type{NamedType: "Directory"},
										Args: []*callpbv1.Argument{
											{
												Name:  "path",
												Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: "/tmp/ws/.changes"}},
											},
										},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, dirCallHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Query.directory",
								StartTimeUnixNano: 50,
								EndTimeUnixNano:   55,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-directory"),
									kvString(t, telemetry.DagOutputAttr, "state-directory"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-directory",
										Field:  "directory",
										Type:   &callpbv1.Type{NamedType: "Directory"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, exportHex),
								ParentSpanId:      mustDecodeHex(t, dirCallHex),
								Name:              "Directory.export",
								StartTimeUnixNano: 56,
								EndTimeUnixNano:   70,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-export"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-export",
										ReceiverDigest: "state-directory",
										Field:          "export",
										Type:           &callpbv1.Type{NamedType: "Void"},
										Args: []*callpbv1.Argument{
											{
												Name:  "path",
												Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: "./out"}},
											},
										},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/cli"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, parseHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "parsing command line arguments",
								StartTimeUnixNano: 40,
								EndTimeUnixNano:   45,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
				},
			},
		},
	}

	ingestBody, err := proto.Marshal(reqPB)
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}
	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(ingestBody))
	ingestRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("ingest failed: status=%d body=%s", ingestRec.Code, ingestRec.Body.String())
	}

	workspacesReq := httptest.NewRequest(http.MethodGet, "/api/workspaces?traceID="+traceIDHex, nil)
	workspacesRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(workspacesRec, workspacesReq)
	if workspacesRec.Code != http.StatusOK {
		t.Fatalf("workspaces failed: status=%d body=%s", workspacesRec.Code, workspacesRec.Body.String())
	}

	var workspacesResp struct {
		Items []struct {
			Root          string `json:"root"`
			Name          string `json:"name"`
			OpCount       int    `json:"opCount"`
			ReadCount     int    `json:"readCount"`
			WriteCount    int    `json:"writeCount"`
			SessionCount  int    `json:"sessionCount"`
			TraceCount    int    `json:"traceCount"`
			PipelineCount int    `json:"pipelineCount"`
			Ops           []struct {
				Path             string `json:"path"`
				CallName         string `json:"callName"`
				PipelineClientID string `json:"pipelineClientID"`
			} `json:"ops"`
		} `json:"items"`
	}
	if err := json.Unmarshal(workspacesRec.Body.Bytes(), &workspacesResp); err != nil {
		t.Fatalf("decode workspaces response: %v", err)
	}
	if len(workspacesResp.Items) != 1 {
		t.Fatalf("expected one workspace, got %#v", workspacesResp.Items)
	}

	item := workspacesResp.Items[0]
	if item.Root != "/tmp/ws" || item.Name != "ws" {
		t.Fatalf("unexpected workspace identity: %#v", item)
	}
	if item.OpCount != 4 || item.ReadCount != 3 || item.WriteCount != 1 {
		t.Fatalf("unexpected workspace op counts: %#v", item)
	}
	if item.SessionCount != 1 || item.TraceCount != 1 || item.PipelineCount != 1 {
		t.Fatalf("unexpected workspace scope counts: %#v", item)
	}
	if len(item.Ops) != 4 {
		t.Fatalf("expected attached workspace ops, got %#v", item.Ops)
	}
	if item.Ops[0].CallName != "Directory.export" || item.Ops[0].Path != "./out" || item.Ops[0].PipelineClientID != clientID {
		t.Fatalf("expected relative export to attach to observed root: %#v", item.Ops[0])
	}
}

func TestV2GitRemotes(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	traceIDHex := "37373737373737373737373737373737"
	sessionID := "session-git-remote"
	clientID := "client-git-remote"

	connectHex := "1111111111111111"
	loadModuleHex := "2222222222222222"
	parseHex := "3333333333333333"
	wolfiHex := "4444444444444444"
	containerHex := "5555555555555555"

	commandArgs := []string{"/usr/local/bin/dagger", "call", "-m", "github.com/dagger/dagger/modules/wolfi", "container"}

	reqPB := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						kvString(t, "service.name", "dagger-cli"),
						kvStringList(t, "process.command_args", commandArgs),
					},
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/engine.client"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, connectHex),
								Name:              "connect",
								StartTimeUnixNano: 1,
								EndTimeUnixNano:   2,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.EngineClientKindAttr, "root"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/cli"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, loadModuleHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "load module: github.com/dagger/dagger/modules/wolfi",
								StartTimeUnixNano: 5,
								EndTimeUnixNano:   9,
								Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, parseHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "parsing command line arguments",
								StartTimeUnixNano: 10,
								EndTimeUnixNano:   12,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/dagql"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, wolfiHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Query.wolfi",
								StartTimeUnixNano: 20,
								EndTimeUnixNano:   24,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.ModuleRefAttr, "github.com/dagger/dagger/modules/wolfi@deadbeef"),
									kvString(t, telemetry.DagDigestAttr, "call-wolfi"),
									kvString(t, telemetry.DagOutputAttr, "state-wolfi"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-wolfi",
										Field:  "wolfi",
										Type:   &callpbv1.Type{NamedType: "Wolfi"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, containerHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Wolfi.container",
								StartTimeUnixNano: 25,
								EndTimeUnixNano:   40,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.ModuleRefAttr, "github.com/dagger/dagger/modules/wolfi@deadbeef"),
									kvString(t, telemetry.DagDigestAttr, "call-container"),
									kvString(t, telemetry.DagOutputAttr, "state-container"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-container",
										ReceiverDigest: "state-wolfi",
										Field:          "container",
										Type:           &callpbv1.Type{NamedType: "Container"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
				},
			},
		},
	}

	ingestBody, err := proto.Marshal(reqPB)
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}
	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(ingestBody))
	ingestRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("ingest failed: status=%d body=%s", ingestRec.Code, ingestRec.Body.String())
	}

	remotesReq := httptest.NewRequest(http.MethodGet, "/api/git-remotes?traceID="+traceIDHex, nil)
	remotesRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(remotesRec, remotesReq)
	if remotesRec.Code != http.StatusOK {
		t.Fatalf("git remotes failed: status=%d body=%s", remotesRec.Code, remotesRec.Body.String())
	}

	var remotesResp struct {
		Items []struct {
			Ref               string   `json:"ref"`
			Host              string   `json:"host"`
			LatestResolvedRef string   `json:"latestResolvedRef"`
			TraceCount        int      `json:"traceCount"`
			SessionCount      int      `json:"sessionCount"`
			PipelineCount     int      `json:"pipelineCount"`
			SpanCount         int      `json:"spanCount"`
			SourceKinds       []string `json:"sourceKinds"`
			Pipelines         []struct {
				PipelineID string `json:"pipelineID"`
				Command    string `json:"command"`
				SessionID  string `json:"sessionID"`
				ClientID   string `json:"clientID"`
			} `json:"pipelines"`
		} `json:"items"`
	}
	if err := json.Unmarshal(remotesRec.Body.Bytes(), &remotesResp); err != nil {
		t.Fatalf("decode git remotes: %v", err)
	}
	if len(remotesResp.Items) != 1 {
		t.Fatalf("expected 1 git remote, got %#v", remotesResp.Items)
	}

	remote := remotesResp.Items[0]
	if remote.Ref != "github.com/dagger/dagger/modules/wolfi" || remote.Host != "github.com" {
		t.Fatalf("unexpected git remote identity: %#v", remote)
	}
	if remote.LatestResolvedRef != "github.com/dagger/dagger/modules/wolfi@deadbeef" {
		t.Fatalf("unexpected resolved ref: %#v", remote)
	}
	if remote.TraceCount != 1 || remote.SessionCount != 1 || remote.PipelineCount != 1 || remote.SpanCount != 3 {
		t.Fatalf("unexpected git remote counts: %#v", remote)
	}
	if !sameStrings(remote.SourceKinds, []string{"load-module", "module-ref"}) {
		t.Fatalf("unexpected git remote source kinds: %#v", remote.SourceKinds)
	}
	if len(remote.Pipelines) != 1 {
		t.Fatalf("expected attached pipeline, got %#v", remote.Pipelines)
	}
	pipeline := remote.Pipelines[0]
	if pipeline.PipelineID == "" || pipeline.SessionID != sessionID || pipeline.ClientID != clientID || !strings.Contains(pipeline.Command, "wolfi container") {
		t.Fatalf("unexpected attached pipeline: %#v", pipeline)
	}
}

func TestV2Registries(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	traceIDHex := "47474747474747474747474747474747"
	sessionID := "session-registry"
	clientID := "client-registry"

	connectHex := "1111111111111111"
	parseHex := "2222222222222222"
	queryContainerHex := "3333333333333333"
	fromHex := "4444444444444444"
	resolveHex := "5555555555555555"
	tokenHex := "6666666666666666"
	headHex := "7777777777777777"

	commandArgs := []string{"/usr/local/bin/dagger", "call", "container", "from", "--address", "docker.io/library/nginx:latest"}
	queryContainerState := map[string]any{
		"type":   "Container",
		"fields": map[string]any{},
	}
	fromState := map[string]any{
		"type":   "Container",
		"fields": map[string]any{},
	}

	reqPB := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						kvString(t, "service.name", "dagger-cli"),
						kvStringList(t, "process.command_args", commandArgs),
					},
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/engine.client"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, connectHex),
								Name:              "connect",
								StartTimeUnixNano: 1,
								EndTimeUnixNano:   2,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.EngineClientKindAttr, "root"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/cli"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, parseHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "parsing command line arguments",
								StartTimeUnixNano: 10,
								EndTimeUnixNano:   12,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/dagql"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, queryContainerHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Query.container",
								StartTimeUnixNano: 20,
								EndTimeUnixNano:   24,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-query-container"),
									kvString(t, telemetry.DagOutputAttr, "state-query-container"),
									kvString(t, telemetry.DagOutputStateAttr, encodeOutputStateB64(t, queryContainerState)),
									kvString(t, telemetry.DagOutputStateVersionAttr, telemetry.DagOutputStateVersionV2),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-query-container",
										Field:  "container",
										Type:   &callpbv1.Type{NamedType: "Container"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, fromHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Container.from",
								StartTimeUnixNano: 25,
								EndTimeUnixNano:   60,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-from"),
									kvString(t, telemetry.DagOutputAttr, "state-from"),
									kvString(t, telemetry.DagOutputStateAttr, encodeOutputStateB64(t, fromState)),
									kvString(t, telemetry.DagOutputStateVersionAttr, telemetry.DagOutputStateVersionV2),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-from",
										ReceiverDigest: "state-query-container",
										Field:          "from",
										Type:           &callpbv1.Type{NamedType: "Container"},
										Args: []*callpbv1.Argument{
											{
												Name: "address",
												Value: &callpbv1.Literal{
													Value: &callpbv1.Literal_String_{String_: "docker.io/library/nginx:latest"},
												},
											},
										},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "buildkit"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, resolveHex),
								ParentSpanId:      mustDecodeHex(t, fromHex),
								Name:              "resolving docker.io/library/nginx:latest",
								StartTimeUnixNano: 26,
								EndTimeUnixNano:   28,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_UNSET},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, tokenHex),
								ParentSpanId:      mustDecodeHex(t, fromHex),
								Name:              "remotes.docker.resolver.FetchToken",
								StartTimeUnixNano: 30,
								EndTimeUnixNano:   35,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, "url.full", "https://auth.docker.io/token?scope=repository%3Alibrary%2Fnginx%3Apull&service=registry.docker.io"),
									kvString(t, "server.address", "auth.docker.io"),
									kvString(t, "http.request.method", "GET"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_UNSET},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, headHex),
								ParentSpanId:      mustDecodeHex(t, fromHex),
								Name:              "HTTP HEAD",
								StartTimeUnixNano: 36,
								EndTimeUnixNano:   42,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, "url.full", "https://registry-1.docker.io/v2/library/nginx/manifests/latest"),
									kvString(t, "server.address", "registry-1.docker.io"),
									kvString(t, "http.request.method", "HEAD"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
				},
			},
		},
	}

	ingestBody, err := proto.Marshal(reqPB)
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}
	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(ingestBody))
	ingestRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("ingest failed: status=%d body=%s", ingestRec.Code, ingestRec.Body.String())
	}

	registriesReq := httptest.NewRequest(http.MethodGet, "/api/registries?traceID="+traceIDHex, nil)
	registriesRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(registriesRec, registriesReq)
	if registriesRec.Code != http.StatusOK {
		t.Fatalf("registries failed: status=%d body=%s", registriesRec.Code, registriesRec.Body.String())
	}

	var registriesResp struct {
		Items []struct {
			Ref           string   `json:"ref"`
			Host          string   `json:"host"`
			Repository    string   `json:"repository"`
			LatestRef     string   `json:"latestRef"`
			TraceCount    int      `json:"traceCount"`
			SessionCount  int      `json:"sessionCount"`
			PipelineCount int      `json:"pipelineCount"`
			ActivityCount int      `json:"activityCount"`
			LastOperation string   `json:"lastOperation"`
			SourceKinds   []string `json:"sourceKinds"`
			Activities    []struct {
				Operation        string `json:"operation"`
				SourceKind       string `json:"sourceKind"`
				PipelineID       string `json:"pipelineID"`
				PipelineCommand  string `json:"pipelineCommand"`
				PipelineClientID string `json:"pipelineClientID"`
			} `json:"activities"`
		} `json:"items"`
	}
	if err := json.Unmarshal(registriesRec.Body.Bytes(), &registriesResp); err != nil {
		t.Fatalf("decode registries response: %v", err)
	}
	if len(registriesResp.Items) != 1 {
		t.Fatalf("expected one registry item, got %#v", registriesResp.Items)
	}

	item := registriesResp.Items[0]
	if item.Ref != "docker.io/library/nginx" || item.Host != "docker.io" || item.Repository != "library/nginx" {
		t.Fatalf("unexpected registry identity: %#v", item)
	}
	if item.TraceCount != 1 || item.SessionCount != 1 || item.PipelineCount != 1 {
		t.Fatalf("unexpected registry aggregate counts: %#v", item)
	}
	if item.ActivityCount < 4 {
		t.Fatalf("expected multiple registry activities, got %#v", item)
	}
	if item.LastOperation == "" {
		t.Fatalf("expected last operation summary, got %#v", item)
	}

	sourceKinds := map[string]struct{}{}
	attached := 0
	for _, kind := range item.SourceKinds {
		sourceKinds[kind] = struct{}{}
	}
	for _, activity := range item.Activities {
		if activity.PipelineID != "" {
			attached++
			if activity.PipelineCommand == "" || activity.PipelineClientID != clientID {
				t.Fatalf("unexpected attached pipeline activity: %#v", activity)
			}
		}
	}
	for _, expected := range []string{"resolve", "auth", "http"} {
		if _, ok := sourceKinds[expected]; !ok {
			t.Fatalf("missing source kind %q in %#v", expected, item.SourceKinds)
		}
	}
	if attached == 0 {
		t.Fatalf("expected at least one registry activity to attach back to the pipeline: %#v", item.Activities)
	}
}

func TestV2Terminals(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	traceIDHex := "48484848484848484848484848484848"
	sessionID := "session-terminal"
	clientID := "client-terminal"

	connectHex := "1111111111111111"
	rootHex := "2222222222222222"
	callHex := "3333333333333333"
	queryHex := "4444444444444444"
	entrypointHex := "5555555555555555"
	shHex := "6666666666666666"

	terminalState := map[string]any{
		"type":   "Container",
		"fields": map[string]any{},
	}

	reqPB := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						kvString(t, "service.name", "dagger-cli"),
					},
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/engine.client"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, connectHex),
								Name:              "connect",
								StartTimeUnixNano: 1,
								EndTimeUnixNano:   2,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.EngineClientKindAttr, "root"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/cli"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, rootHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "engine-dev playground terminal",
								StartTimeUnixNano: 5,
								EndTimeUnixNano:   100,
								Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/dagql"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, callHex),
								ParentSpanId:      mustDecodeHex(t, queryHex),
								Name:              "Container.terminal",
								StartTimeUnixNano: 20,
								EndTimeUnixNano:   90,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-terminal"),
									kvString(t, telemetry.DagOutputAttr, "state-terminal"),
									kvString(t, telemetry.DagOutputStateAttr, encodeOutputStateB64(t, terminalState)),
									kvString(t, telemetry.DagOutputStateVersionAttr, telemetry.DagOutputStateVersionV2),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-terminal",
										ReceiverDigest: "state-terminal",
										Field:          "terminal",
										Type:           &callpbv1.Type{NamedType: "Container"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "buildkit"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, queryHex),
								ParentSpanId:      mustDecodeHex(t, rootHex),
								Name:              "POST /query",
								StartTimeUnixNano: 10,
								EndTimeUnixNano:   91,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, entrypointHex),
								ParentSpanId:      mustDecodeHex(t, callHex),
								Name:              "exec dagger-entrypoint.sh --addr tcp://0.0.0.0:1234",
								StartTimeUnixNano: 30,
								EndTimeUnixNano:   95,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, shHex),
								ParentSpanId:      mustDecodeHex(t, callHex),
								Name:              "exec sh",
								StartTimeUnixNano: 35,
								EndTimeUnixNano:   89,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR},
							},
						},
					},
				},
			},
		},
	}

	ingestBody, err := proto.Marshal(reqPB)
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}
	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(ingestBody))
	ingestRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("ingest failed: status=%d body=%s", ingestRec.Code, ingestRec.Body.String())
	}

	terminalsReq := httptest.NewRequest(http.MethodGet, "/api/terminals?traceID="+traceIDHex, nil)
	terminalsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(terminalsRec, terminalsReq)
	if terminalsRec.Code != http.StatusOK {
		t.Fatalf("terminals failed: status=%d body=%s", terminalsRec.Code, terminalsRec.Body.String())
	}

	var terminalsResp struct {
		Items []struct {
			Name            string   `json:"name"`
			CallName        string   `json:"callName"`
			Status          string   `json:"status"`
			SessionID       string   `json:"sessionID"`
			ClientID        string   `json:"clientID"`
			ReceiverDagqlID string   `json:"receiverDagqlID"`
			OutputDagqlID   string   `json:"outputDagqlID"`
			ActivityCount   int      `json:"activityCount"`
			ExecCount       int      `json:"execCount"`
			ActivityNames   []string `json:"activityNames"`
			Activities      []struct {
				Name   string `json:"name"`
				Kind   string `json:"kind"`
				Status string `json:"status"`
			} `json:"activities"`
		} `json:"items"`
	}
	if err := json.Unmarshal(terminalsRec.Body.Bytes(), &terminalsResp); err != nil {
		t.Fatalf("decode terminals response: %v", err)
	}
	if len(terminalsResp.Items) != 1 {
		t.Fatalf("expected one terminal item, got %#v", terminalsResp.Items)
	}

	item := terminalsResp.Items[0]
	if item.Name != "engine-dev playground terminal" || item.CallName != "Container.terminal" {
		t.Fatalf("unexpected terminal identity: %#v", item)
	}
	if item.Status != "failed" || item.SessionID != sessionID || item.ClientID != clientID {
		t.Fatalf("unexpected terminal scope/status: %#v", item)
	}
	if item.ReceiverDagqlID != "state-terminal" || item.OutputDagqlID != "state-terminal" {
		t.Fatalf("unexpected terminal dagql ids: %#v", item)
	}
	if item.ActivityCount != 2 || item.ExecCount != 2 {
		t.Fatalf("expected only exec activity rows, got %#v", item)
	}
	if !sameStrings(item.ActivityNames, []string{
		"exec dagger-entrypoint.sh --addr tcp://0.0.0.0:1234",
		"exec sh",
	}) {
		t.Fatalf("unexpected terminal activity names: %#v", item.ActivityNames)
	}
	for _, activity := range item.Activities {
		if activity.Kind != "exec" {
			t.Fatalf("unexpected activity kind: %#v", item.Activities)
		}
	}
}

func TestV2Repls(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "odag.db")
	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     dbPath,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	traceIDHex := "c9c10b4a0fb67ed7f6166b5f5eae9b01"
	connectHex := "c7d60e2bb5226f41"
	rootHex := "2197e7b3c7412172"
	containerHex := "1e724ac0cafe7196"
	fromHex := "516e4c32d230b10b"
	asServiceHex := "053dab7fdb31673c"
	upHex := "98b246f2436c4ea9"
	clientID := "repl-client"
	sessionID := "repl-session"
	commandArgs := []string{"dagger", "-M", "-c", "container | from nginx | as-service | up"}
	rootCommand := strings.Join(commandArgs, " ")

	reqPB := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						kvStringList(t, "process.command_args", commandArgs),
						kvString(t, "service.name", "dagger-cli"),
					},
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/engine.client"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, connectHex),
								Name:              "connect",
								StartTimeUnixNano: 1,
								EndTimeUnixNano:   2,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.EngineClientKindAttr, "root"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/cli"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, rootHex),
								Name:              rootCommand,
								StartTimeUnixNano: 1,
								EndTimeUnixNano:   0,
								Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/shell"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, containerHex),
								ParentSpanId:      mustDecodeHex(t, rootHex),
								Name:              "container",
								StartTimeUnixNano: 20,
								EndTimeUnixNano:   30,
								Attributes: []*commonpb.KeyValue{
									kvStringList(t, "dagger.io/shell.handler.args", []string{"container"}),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, fromHex),
								ParentSpanId:      mustDecodeHex(t, rootHex),
								Name:              "from",
								StartTimeUnixNano: 31,
								EndTimeUnixNano:   40,
								Attributes: []*commonpb.KeyValue{
									kvStringList(t, "dagger.io/shell.handler.args", []string{"from", "nginx"}),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, asServiceHex),
								ParentSpanId:      mustDecodeHex(t, rootHex),
								Name:              "as-service",
								StartTimeUnixNano: 41,
								EndTimeUnixNano:   50,
								Attributes: []*commonpb.KeyValue{
									kvStringList(t, "dagger.io/shell.handler.args", []string{"as-service"}),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, upHex),
								ParentSpanId:      mustDecodeHex(t, rootHex),
								Name:              "up",
								StartTimeUnixNano: 51,
								EndTimeUnixNano:   0,
								Attributes: []*commonpb.KeyValue{
									kvStringList(t, "dagger.io/shell.handler.args", []string{"up"}),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR},
							},
						},
					},
				},
			},
		},
	}

	ingestBody, err := proto.Marshal(reqPB)
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}
	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(ingestBody))
	ingestRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("ingest failed: status=%d body=%s", ingestRec.Code, ingestRec.Body.String())
	}

	replsReq := httptest.NewRequest(http.MethodGet, "/api/repls?traceID="+traceIDHex, nil)
	replsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(replsRec, replsReq)
	if replsRec.Code != http.StatusOK {
		t.Fatalf("repls failed: status=%d body=%s", replsRec.Code, replsRec.Body.String())
	}

	var replsResp struct {
		Items []struct {
			Name         string `json:"name"`
			Command      string `json:"command"`
			Mode         string `json:"mode"`
			Status       string `json:"status"`
			SessionID    string `json:"sessionID"`
			ClientID     string `json:"clientID"`
			RootClientID string `json:"rootClientID"`
			CommandCount int    `json:"commandCount"`
			FirstCommand string `json:"firstCommand"`
			LastCommand  string `json:"lastCommand"`
			Commands     []struct {
				Command string `json:"command"`
				Status  string `json:"status"`
			} `json:"commands"`
		} `json:"items"`
	}
	if err := json.Unmarshal(replsRec.Body.Bytes(), &replsResp); err != nil {
		t.Fatalf("decode repls response: %v", err)
	}
	if len(replsResp.Items) != 1 {
		t.Fatalf("expected one repl item, got %#v", replsResp.Items)
	}

	item := replsResp.Items[0]
	if item.Name != rootCommand || item.Command != rootCommand {
		t.Fatalf("unexpected repl identity: %#v", item)
	}
	if item.Mode != "inline" {
		t.Fatalf("unexpected repl mode: %#v", item)
	}
	if item.Status != "failed" || item.SessionID != sessionID || item.ClientID != clientID || item.RootClientID != clientID {
		t.Fatalf("unexpected repl scope/status: %#v", item)
	}
	if item.CommandCount != 4 || item.FirstCommand != "container" || item.LastCommand != "up" {
		t.Fatalf("unexpected repl command summary: %#v", item)
	}
	if got := []string{
		item.Commands[0].Command,
		item.Commands[1].Command,
		item.Commands[2].Command,
		item.Commands[3].Command,
	}; !sameStrings(got, []string{"container", "from nginx", "as-service", "up"}) {
		t.Fatalf("unexpected repl commands: %#v", item.Commands)
	}
}

func TestV2ShellCommandsMaterializeAsPipelines(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	traceIDHex := "d9d10b4a0fb67ed7f6166b5f5eae9b02"
	connectHex := "c7d60e2bb5226f42"
	rootHex := "2197e7b3c7412173"
	containerHex := "1e724ac0cafe7197"
	fromHex := "516e4c32d230b10c"
	asServiceHex := "053dab7fdb31673d"
	upHex := "98b246f2436c4eaa"
	queryContainerHex := "8633244a58197006"
	fromCallHex := "be3f90324db607cc"
	asServiceCallHex := "523e3d2576d8e229"
	upCallHex := "45062880bee298f4"

	clientID := "repl-pipeline-client"
	sessionID := "repl-pipeline-session"
	commandArgs := []string{"dagger", "-M", "-c", "container | from nginx | as-service | up"}
	rootCommand := strings.Join(commandArgs, " ")

	reqPB := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						kvStringList(t, "process.command_args", commandArgs),
						kvString(t, "service.name", "dagger-cli"),
					},
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/engine.client"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, connectHex),
								ParentSpanId:      mustDecodeHex(t, rootHex),
								Name:              "connect",
								StartTimeUnixNano: 2,
								EndTimeUnixNano:   10,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.EngineClientKindAttr, "root"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/cli"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, rootHex),
								Name:              rootCommand,
								StartTimeUnixNano: 1,
								EndTimeUnixNano:   100,
								Status:            &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/shell"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, containerHex),
								ParentSpanId:      mustDecodeHex(t, rootHex),
								Name:              "container",
								StartTimeUnixNano: 20,
								EndTimeUnixNano:   30,
								Attributes: []*commonpb.KeyValue{
									kvStringList(t, "dagger.io/shell.handler.args", []string{"container"}),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, fromHex),
								ParentSpanId:      mustDecodeHex(t, rootHex),
								Name:              "from",
								StartTimeUnixNano: 31,
								EndTimeUnixNano:   40,
								Attributes: []*commonpb.KeyValue{
									kvStringList(t, "dagger.io/shell.handler.args", []string{"from", "nginx"}),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, asServiceHex),
								ParentSpanId:      mustDecodeHex(t, rootHex),
								Name:              "as-service",
								StartTimeUnixNano: 41,
								EndTimeUnixNano:   50,
								Attributes: []*commonpb.KeyValue{
									kvStringList(t, "dagger.io/shell.handler.args", []string{"as-service"}),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, upHex),
								ParentSpanId:      mustDecodeHex(t, rootHex),
								Name:              "up",
								StartTimeUnixNano: 51,
								EndTimeUnixNano:   60,
								Attributes: []*commonpb.KeyValue{
									kvStringList(t, "dagger.io/shell.handler.args", []string{"up"}),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/dagql"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, queryContainerHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Query.container",
								StartTimeUnixNano: 70,
								EndTimeUnixNano:   75,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-container"),
									kvString(t, telemetry.DagOutputAttr, "state-container"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-container",
										Field:  "container",
										Type:   &callpbv1.Type{NamedType: "Container"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, fromCallHex),
								ParentSpanId:      mustDecodeHex(t, queryContainerHex),
								Name:              "Container.from",
								StartTimeUnixNano: 76,
								EndTimeUnixNano:   82,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-from"),
									kvString(t, telemetry.DagOutputAttr, "state-from"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-from",
										ReceiverDigest: "state-container",
										Field:          "from",
										Type:           &callpbv1.Type{NamedType: "Container"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, asServiceCallHex),
								ParentSpanId:      mustDecodeHex(t, fromCallHex),
								Name:              "Container.asService",
								StartTimeUnixNano: 83,
								EndTimeUnixNano:   88,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-as-service"),
									kvString(t, telemetry.DagOutputAttr, "state-service"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-as-service",
										ReceiverDigest: "state-from",
										Field:          "asService",
										Type:           &callpbv1.Type{NamedType: "Service"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, upCallHex),
								ParentSpanId:      mustDecodeHex(t, asServiceCallHex),
								Name:              "Service.up",
								StartTimeUnixNano: 89,
								EndTimeUnixNano:   0,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-up"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-up",
										ReceiverDigest: "state-service",
										Field:          "up",
										Type:           &callpbv1.Type{NamedType: "Void"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
				},
			},
		},
	}

	ingestBody, err := proto.Marshal(reqPB)
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}
	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(ingestBody))
	ingestRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("ingest failed: status=%d body=%s", ingestRec.Code, ingestRec.Body.String())
	}

	pipelinesReq := httptest.NewRequest(http.MethodGet, "/api/pipelines?traceID="+traceIDHex, nil)
	pipelinesRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(pipelinesRec, pipelinesReq)
	if pipelinesRec.Code != http.StatusOK {
		t.Fatalf("pipelines failed: status=%d body=%s", pipelinesRec.Code, pipelinesRec.Body.String())
	}

	var pipelinesResp struct {
		Items []struct {
			ID               string `json:"id"`
			Command          string `json:"command"`
			SubmittedSpanID  string `json:"submittedSpanID"`
			TerminalCallName string `json:"terminalCallName"`
			CallCount        int    `json:"callCount"`
			ChainDepth       int    `json:"chainDepth"`
		} `json:"items"`
	}
	if err := json.Unmarshal(pipelinesRec.Body.Bytes(), &pipelinesResp); err != nil {
		t.Fatalf("decode pipelines: %v", err)
	}
	if len(pipelinesResp.Items) != 1 {
		t.Fatalf("expected one shell-derived pipeline, got %#v", pipelinesResp.Items)
	}
	pipeline := pipelinesResp.Items[0]
	if pipeline.Command != rootCommand || pipeline.TerminalCallName != "Service.up" {
		t.Fatalf("unexpected shell pipeline identity: %#v", pipeline)
	}
	if pipeline.CallCount != 4 || pipeline.ChainDepth != 4 {
		t.Fatalf("unexpected shell pipeline counts: %#v", pipeline)
	}
	if pipeline.SubmittedSpanID != traceIDHex+"/"+rootHex {
		t.Fatalf("unexpected submitted span id: %#v", pipeline)
	}

	replsReq := httptest.NewRequest(http.MethodGet, "/api/repls?traceID="+traceIDHex, nil)
	replsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(replsRec, replsReq)
	if replsRec.Code != http.StatusOK {
		t.Fatalf("repls failed: status=%d body=%s", replsRec.Code, replsRec.Body.String())
	}

	var replsResp struct {
		Items []struct {
			Commands []struct {
				Command         string `json:"command"`
				PipelineID      string `json:"pipelineID"`
				PipelineCommand string `json:"pipelineCommand"`
			} `json:"commands"`
		} `json:"items"`
	}
	if err := json.Unmarshal(replsRec.Body.Bytes(), &replsResp); err != nil {
		t.Fatalf("decode repls response: %v", err)
	}
	if len(replsResp.Items) != 1 || len(replsResp.Items[0].Commands) != 4 {
		t.Fatalf("unexpected repl command history: %#v", replsResp.Items)
	}
	for _, command := range replsResp.Items[0].Commands {
		if command.PipelineID != pipeline.ID || command.PipelineCommand != rootCommand {
			t.Fatalf("expected repl command to link back to shell pipeline, got %#v", command)
		}
	}
}

func TestV2Checks(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	traceIDHex := "dededededededededededededededede"
	connectHex := "1111111111111111"
	checkHex := "2222222222222222"
	sessionID := "session-checks"
	clientID := "client-checks"

	reqPB := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						kvString(t, "service.name", "dagger-cli"),
					},
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/engine.client"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, connectHex),
								Name:              "connect",
								StartTimeUnixNano: 1,
								EndTimeUnixNano:   2,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.EngineClientKindAttr, "root"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/checks"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, checkHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "test:lint",
								StartTimeUnixNano: 10,
								EndTimeUnixNano:   20,
								Attributes: []*commonpb.KeyValue{
									kvString(t, "dagger.io/check.name", "test:lint"),
									kvBool(t, "dagger.io/check.passed", false),
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR},
							},
						},
					},
				},
			},
		},
	}

	ingestBody, err := proto.Marshal(reqPB)
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}
	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(ingestBody))
	ingestRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("ingest failed: status=%d body=%s", ingestRec.Code, ingestRec.Body.String())
	}

	checksReq := httptest.NewRequest(http.MethodGet, "/api/checks?traceID="+traceIDHex, nil)
	checksRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(checksRec, checksReq)
	if checksRec.Code != http.StatusOK {
		t.Fatalf("checks failed: status=%d body=%s", checksRec.Code, checksRec.Body.String())
	}

	var checksResp struct {
		Items []struct {
			Name      string `json:"name"`
			Status    string `json:"status"`
			SessionID string `json:"sessionID"`
			ClientID  string `json:"clientID"`
			SpanName  string `json:"spanName"`
		} `json:"items"`
	}
	if err := json.Unmarshal(checksRec.Body.Bytes(), &checksResp); err != nil {
		t.Fatalf("decode checks response: %v", err)
	}
	if len(checksResp.Items) != 1 {
		t.Fatalf("expected one check item, got %#v", checksResp.Items)
	}

	item := checksResp.Items[0]
	if item.Name != "test:lint" || item.SpanName != "test:lint" {
		t.Fatalf("unexpected check identity: %#v", item)
	}
	if item.Status != "failed" || item.SessionID != sessionID || item.ClientID != clientID {
		t.Fatalf("unexpected check scope/status: %#v", item)
	}
}

func TestV2Services(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	traceIDHex := "38383838383838383838383838383838"
	sessionID := "session-service"
	clientID := "client-service"

	connectHex := "1111111111111111"
	asServiceHex := "2222222222222222"
	upHex := "3333333333333333"
	bindHex := "4444444444444444"

	commandArgs := []string{"/usr/local/bin/dagger", "shell", "-c", "container | from nginx | as-service | up"}
	serviceState := map[string]any{
		"type": "Service",
		"fields": map[string]any{
			"CustomHostname": map[string]any{"name": "CustomHostname", "type": "string", "value": ""},
			"Container": map[string]any{
				"name":  "Container",
				"type":  "Container!",
				"value": map[string]any{"ImageRef": "docker.io/library/nginx:latest"},
				"refs":  []any{"state-container"},
			},
			"HostSockets":    map[string]any{"name": "HostSockets", "type": "[]*core.Socket", "value": []any{}},
			"TunnelPorts":    map[string]any{"name": "TunnelPorts", "type": "[]core.PortForward", "value": []any{}},
			"TunnelUpstream": map[string]any{"name": "TunnelUpstream", "type": "Service!", "value": nil},
		},
	}

	reqPB := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						kvString(t, "service.name", "dagger-cli"),
						kvStringList(t, "process.command_args", commandArgs),
					},
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/engine.client"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, connectHex),
								Name:              "connect",
								StartTimeUnixNano: 1,
								EndTimeUnixNano:   2,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.EngineClientKindAttr, "root"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/dagql"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, asServiceHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Container.asService",
								StartTimeUnixNano: 10,
								EndTimeUnixNano:   15,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-as-service"),
									kvString(t, telemetry.DagOutputAttr, "state-service"),
									kvString(t, telemetry.DagOutputStateAttr, encodeOutputStateB64(t, serviceState)),
									kvString(t, telemetry.DagOutputStateVersionAttr, telemetry.DagOutputStateVersionV2),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-as-service",
										ReceiverDigest: "state-container",
										Field:          "asService",
										Type:           &callpbv1.Type{NamedType: "Service"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, upHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Service.up",
								StartTimeUnixNano: 16,
								EndTimeUnixNano:   24,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-up"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-up",
										ReceiverDigest: "state-service",
										Field:          "up",
										Type:           &callpbv1.Type{NamedType: "Void"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, bindHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Container.withServiceBinding",
								StartTimeUnixNano: 25,
								EndTimeUnixNano:   30,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-bind"),
									kvString(t, telemetry.DagOutputAttr, "state-bound-container"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-bind",
										ReceiverDigest: "state-container",
										Field:          "withServiceBinding",
										Type:           &callpbv1.Type{NamedType: "Container"},
										Args: []*callpbv1.Argument{
											{
												Name:  "service",
												Value: &callpbv1.Literal{Value: &callpbv1.Literal_CallDigest{CallDigest: "state-service"}},
											},
										},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
				},
			},
		},
	}

	ingestBody, err := proto.Marshal(reqPB)
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}
	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(ingestBody))
	ingestRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("ingest failed: status=%d body=%s", ingestRec.Code, ingestRec.Body.String())
	}

	servicesReq := httptest.NewRequest(http.MethodGet, "/api/services?traceID="+traceIDHex, nil)
	servicesRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(servicesRec, servicesReq)
	if servicesRec.Code != http.StatusOK {
		t.Fatalf("services failed: status=%d body=%s", servicesRec.Code, servicesRec.Body.String())
	}

	var servicesResp struct {
		Items []struct {
			Name                  string `json:"name"`
			Kind                  string `json:"kind"`
			Status                string `json:"status"`
			DagqlID               string `json:"dagqlID"`
			SessionID             string `json:"sessionID"`
			ClientID              string `json:"clientID"`
			CreatedByCallName     string `json:"createdByCallName"`
			ImageRef              string `json:"imageRef"`
			ContainerDagqlID      string `json:"containerDagqlID"`
			TunnelUpstreamDagqlID string `json:"tunnelUpstreamDagqlID"`
			Activity              []struct {
				Name string `json:"name"`
				Role string `json:"role"`
			} `json:"activity"`
		} `json:"items"`
	}
	if err := json.Unmarshal(servicesRec.Body.Bytes(), &servicesResp); err != nil {
		t.Fatalf("decode services: %v", err)
	}
	if len(servicesResp.Items) != 1 {
		t.Fatalf("expected 1 service, got %#v", servicesResp.Items)
	}

	service := servicesResp.Items[0]
	if service.Name != "nginx:latest" || service.Kind != "container" || service.Status != "failed" {
		t.Fatalf("unexpected service identity: %#v", service)
	}
	if service.DagqlID != "state-service" || service.SessionID != sessionID || service.ClientID != clientID {
		t.Fatalf("unexpected service scope: %#v", service)
	}
	if service.CreatedByCallName != "Container.asService" || service.ImageRef != "docker.io/library/nginx:latest" || service.ContainerDagqlID != "state-container" || service.TunnelUpstreamDagqlID != "" {
		t.Fatalf("unexpected service fields: %#v", service)
	}
	if len(service.Activity) != 3 {
		t.Fatalf("expected 3 service activity rows, got %#v", service.Activity)
	}
	if service.Activity[0].Name != "Container.withServiceBinding" || service.Activity[0].Role != "consumer" {
		t.Fatalf("unexpected latest service activity: %#v", service.Activity[0])
	}
	if service.Activity[1].Name != "Service.up" || service.Activity[1].Role != "lifecycle" {
		t.Fatalf("unexpected lifecycle activity: %#v", service.Activity[1])
	}
	if service.Activity[2].Name != "Container.asService" || service.Activity[2].Role != "producer" {
		t.Fatalf("unexpected producer activity: %#v", service.Activity[2])
	}
}

func TestV2Shells(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	traceIDHex := "44444444444444444444444444444444"
	rootConnectHex := "1111111111111111"
	rootCallHex := "2222222222222222"
	childConnectHex := "3333333333333333"
	childCallHex := "4444444444444444"
	auxSpanHex := "5555555555555555"

	sessionID := "session-shell"
	rootClientID := "client-shell-root"
	childClientID := "client-shell-child"
	commandArgs := []string{"/usr/local/bin/dagger", "shell", "-c", "container | with-exec uname -a"}

	reqPB := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						kvString(t, "service.name", "dagger-cli"),
						kvStringList(t, "process.command_args", commandArgs),
					},
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/engine.client"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, rootConnectHex),
								Name:              "connect",
								StartTimeUnixNano: 1,
								EndTimeUnixNano:   2,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, rootClientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.EngineClientKindAttr, "root"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, childConnectHex),
								ParentSpanId:      mustDecodeHex(t, rootCallHex),
								Name:              "connect",
								StartTimeUnixNano: 20,
								EndTimeUnixNano:   21,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, childClientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.EngineParentClientIDAttr, rootClientID),
									kvString(t, telemetry.EngineClientKindAttr, "nested"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/dagql"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, rootCallHex),
								ParentSpanId:      mustDecodeHex(t, rootConnectHex),
								Name:              "Query.container",
								StartTimeUnixNano: 10,
								EndTimeUnixNano:   18,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, rootClientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-container"),
									kvString(t, telemetry.DagOutputAttr, "state-container"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-container",
										Field:  "container",
										Type:   &callpbv1.Type{NamedType: "Container"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, childCallHex),
								ParentSpanId:      mustDecodeHex(t, childConnectHex),
								Name:              "Container.withExec",
								StartTimeUnixNano: 24,
								EndTimeUnixNano:   40,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, childClientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-with-exec"),
									kvString(t, telemetry.DagOutputAttr, "state-exec"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-with-exec",
										ReceiverDigest: "state-container",
										Field:          "withExec",
										Type:           &callpbv1.Type{NamedType: "Container"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/shell"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, auxSpanHex),
								ParentSpanId:      mustDecodeHex(t, childCallHex),
								Name:              "render prompt",
								StartTimeUnixNano: 41,
								EndTimeUnixNano:   48,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, childClientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
				},
			},
		},
	}

	ingestBody, err := proto.Marshal(reqPB)
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}
	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(ingestBody))
	ingestRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("ingest failed: status=%d body=%s", ingestRec.Code, ingestRec.Body.String())
	}

	shellsReq := httptest.NewRequest(http.MethodGet, "/api/shells?traceID="+traceIDHex, nil)
	shellsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(shellsRec, shellsReq)
	if shellsRec.Code != http.StatusOK {
		t.Fatalf("shells failed: status=%d body=%s", shellsRec.Code, shellsRec.Body.String())
	}

	var shellsResp struct {
		DerivationVersion string `json:"derivationVersion"`
		Items             []struct {
			ID               string   `json:"id"`
			TraceID          string   `json:"traceID"`
			SessionID        string   `json:"sessionID"`
			ClientID         string   `json:"clientID"`
			Name             string   `json:"name"`
			Mode             string   `json:"mode"`
			EntryLabel       string   `json:"entryLabel"`
			Status           string   `json:"status"`
			ChildClientIDs   []string `json:"childClientIDs"`
			ChildClientCount int      `json:"childClientCount"`
			CallCount        int      `json:"callCount"`
			SpanCount        int      `json:"spanCount"`
			ActivityNames    []string `json:"activityNames"`
			Evidence         []struct {
				Kind string `json:"kind"`
				Note string `json:"note"`
			} `json:"evidence"`
			Relations []struct {
				Relation string `json:"relation"`
				Target   string `json:"target"`
			} `json:"relations"`
		} `json:"items"`
	}
	if err := json.Unmarshal(shellsRec.Body.Bytes(), &shellsResp); err != nil {
		t.Fatalf("decode shells: %v", err)
	}
	if shellsResp.DerivationVersion == "" || len(shellsResp.Items) != 1 {
		t.Fatalf("unexpected shells response: %#v", shellsResp)
	}

	shell := shellsResp.Items[0]
	if shell.TraceID != traceIDHex || shell.SessionID != sessionID || shell.ClientID != rootClientID {
		t.Fatalf("unexpected shell scope: %#v", shell)
	}
	if shell.Name != "container | with-exec uname -a" || shell.Mode != "inline" || shell.EntryLabel == "" {
		t.Fatalf("unexpected shell identity: %#v", shell)
	}
	if shell.Status != "ready" || shell.ChildClientCount != 1 || shell.CallCount != 2 || shell.SpanCount != 5 {
		t.Fatalf("unexpected shell counts: %#v", shell)
	}
	if !sameStrings(shell.ChildClientIDs, []string{childClientID}) {
		t.Fatalf("unexpected child clients: %#v", shell.ChildClientIDs)
	}
	if !sameStrings(shell.ActivityNames, []string{"Container.withExec", "Query.container"}) {
		t.Fatalf("unexpected activity names: %#v", shell.ActivityNames)
	}
	if len(shell.Evidence) < 4 {
		t.Fatalf("expected shell evidence rows, got %#v", shell.Evidence)
	}
	if len(shell.Relations) < 3 {
		t.Fatalf("expected shell relations, got %#v", shell.Relations)
	}
}

func TestPipelineObjectDAG(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	traceIDHex := "55555555555555555555555555555555"
	connectHex := "1111111111111111"
	moduleLoadHex := "1212121212121212"
	moduleSourceHex := "1313131313131313"
	asModuleHex := "1414141414141414"
	terminalHex := "1515151515151515"
	childHex := "1616161616161616"

	sessionID := "session-pipeline"
	clientID := "client-pipeline"
	moduleRef := "github.com/dagger/dagger/modules/wolfi"
	commandArgs := []string{"/usr/local/bin/dagger", "call", "-m", moduleRef, "container"}

	containerState := map[string]any{
		"type": "Container",
		"fields": map[string]any{
			"rootfs": map[string]any{
				"name":  "rootfs",
				"type":  "Directory",
				"value": nil,
				"refs":  []any{"state-dir"},
			},
		},
	}
	dirState := map[string]any{
		"type":   "Directory",
		"fields": map[string]any{},
	}
	moduleSourceState := map[string]any{
		"type":   "ModuleSource",
		"fields": map[string]any{},
	}
	moduleState := map[string]any{
		"type":   "Module",
		"fields": map[string]any{},
	}

	reqPB := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						kvString(t, "service.name", "dagger-cli"),
						kvStringList(t, "process.command_args", commandArgs),
					},
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/engine.client"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, connectHex),
								Name:              "connect",
								StartTimeUnixNano: 1,
								EndTimeUnixNano:   2,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.EngineClientKindAttr, "root"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/cli"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, moduleLoadHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "load module: " + moduleRef,
								StartTimeUnixNano: 5,
								EndTimeUnixNano:   8,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/dagql"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, moduleSourceHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Query.moduleSource",
								StartTimeUnixNano: 9,
								EndTimeUnixNano:   15,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-module-source"),
									kvString(t, telemetry.DagOutputAttr, "state-module-source"),
									kvString(t, telemetry.DagOutputStateAttr, encodeOutputStateB64(t, moduleSourceState)),
									kvString(t, telemetry.DagOutputStateVersionAttr, telemetry.DagOutputStateVersionV2),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-module-source",
										Field:  "moduleSource",
										Type:   &callpbv1.Type{NamedType: "ModuleSource"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, asModuleHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "ModuleSource.asModule",
								StartTimeUnixNano: 16,
								EndTimeUnixNano:   22,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-as-module"),
									kvString(t, telemetry.DagOutputAttr, "state-module"),
									kvString(t, telemetry.DagOutputStateAttr, encodeOutputStateB64(t, moduleState)),
									kvString(t, telemetry.DagOutputStateVersionAttr, telemetry.DagOutputStateVersionV2),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-as-module",
										ReceiverDigest: "state-module-source",
										Field:          "asModule",
										Type:           &callpbv1.Type{NamedType: "Module"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, terminalHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Wolfi.container",
								StartTimeUnixNano: 30,
								EndTimeUnixNano:   40,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-container"),
									kvString(t, telemetry.DagOutputAttr, "state-container"),
									kvString(t, telemetry.DagOutputStateAttr, encodeOutputStateB64(t, containerState)),
									kvString(t, telemetry.DagOutputStateVersionAttr, telemetry.DagOutputStateVersionV2),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-container",
										Field:  "container",
										Type:   &callpbv1.Type{NamedType: "Container"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, childHex),
								ParentSpanId:      mustDecodeHex(t, terminalHex),
								Name:              "Container.directory",
								StartTimeUnixNano: 33,
								EndTimeUnixNano:   36,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-directory"),
									kvString(t, telemetry.DagOutputAttr, "state-dir"),
									kvString(t, telemetry.DagOutputStateAttr, encodeOutputStateB64(t, dirState)),
									kvString(t, telemetry.DagOutputStateVersionAttr, telemetry.DagOutputStateVersionV2),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-directory",
										ReceiverDigest: "state-container",
										Field:          "directory",
										Type:           &callpbv1.Type{NamedType: "Directory"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
				},
			},
		},
	}

	ingestBody, err := proto.Marshal(reqPB)
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}
	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(ingestBody))
	ingestRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("ingest failed: status=%d body=%s", ingestRec.Code, ingestRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/pipelines/object-dag?traceID="+traceIDHex+"&callID="+terminalHex, nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pipeline object dag failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		DerivationVersion string `json:"derivationVersion"`
		Context           struct {
			TraceID       string `json:"traceID"`
			CallID        string `json:"callID"`
			ClientID      string `json:"clientID"`
			SessionID     string `json:"sessionID"`
			OutputDagqlID string `json:"outputDagqlID"`
			OutputType    string `json:"outputType"`
		} `json:"context"`
		Module struct {
			Ref     string   `json:"ref"`
			CallIDs []string `json:"callIDs"`
		} `json:"module"`
		Objects []struct {
			DagqlID string `json:"dagqlID"`
		} `json:"objects"`
		Edges []struct {
			FromDagqlID string `json:"fromDagqlID"`
			ToDagqlID   string `json:"toDagqlID"`
			Label       string `json:"label"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode pipeline object dag: %v", err)
	}

	if resp.DerivationVersion == "" {
		t.Fatalf("missing derivation version: %#v", resp)
	}
	if resp.Context.TraceID != traceIDHex || resp.Context.CallID != terminalHex || resp.Context.ClientID != clientID || resp.Context.SessionID != sessionID {
		t.Fatalf("unexpected pipeline context: %#v", resp.Context)
	}
	if resp.Context.OutputDagqlID != "state-container" || resp.Context.OutputType != "Container" {
		t.Fatalf("unexpected pipeline output context: %#v", resp.Context)
	}
	if resp.Module.Ref != moduleRef {
		t.Fatalf("unexpected module metadata: %#v", resp.Module)
	}
	if !sameStrings(resp.Module.CallIDs, []string{
		traceIDHex + "/" + moduleSourceHex,
		traceIDHex + "/" + asModuleHex,
	}) {
		t.Fatalf("unexpected module call ids: %#v", resp.Module.CallIDs)
	}

	dagqlIDs := make([]string, 0, len(resp.Objects))
	for _, obj := range resp.Objects {
		dagqlIDs = append(dagqlIDs, obj.DagqlID)
	}
	if !sameStrings(dagqlIDs, []string{"state-container"}) {
		t.Fatalf("unexpected pipeline object set: %#v", dagqlIDs)
	}
	if len(resp.Edges) != 0 {
		t.Fatalf("unexpected pipeline edges: %#v", resp.Edges)
	}
}

func TestPipelineObjectDAGNarrowsToPipelineChainObjects(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	traceIDHex := "babababababababababababababababa"
	sessionID := "sess-pipeline-chain"
	rootClientID := "client-root"
	childClientID := "client-child"
	moduleRef := "github.com/dagger/dagger/modules/wolfi"

	rootConnectHex := "1111111111111111"
	childConnectHex := "2222222222222222"
	moduleLoadHex := "3333333333333333"
	moduleSourceHex := "4444444444444444"
	asModuleHex := "5555555555555555"
	wolfiHex := "6666666666666666"
	terminalHex := "7777777777777777"
	childContainerHex := "8888888888888888"
	childNoiseHex := "9999999999999999"

	moduleSourceState := map[string]any{
		"type":   "ModuleSource",
		"fields": map[string]any{},
	}
	moduleState := map[string]any{
		"type":   "Module",
		"fields": map[string]any{},
	}
	wolfiState := map[string]any{
		"type":   "Wolfi",
		"fields": map[string]any{},
	}
	containerState := map[string]any{
		"type": "Container",
		"fields": map[string]any{
			"FS": map[string]any{
				"name":  "FS",
				"type":  "Directory!",
				"value": "state-rootfs",
				"refs":  []any{"state-rootfs"},
			},
		},
	}
	noiseState := map[string]any{
		"type":   "File",
		"fields": map[string]any{},
	}

	reqPB := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/engine.client"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, rootConnectHex),
								Name:              "connect",
								StartTimeUnixNano: 1,
								EndTimeUnixNano:   2,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, rootClientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.EngineClientKindAttr, "root"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, childConnectHex),
								ParentSpanId:      mustDecodeHex(t, terminalHex),
								Name:              "connect",
								StartTimeUnixNano: 35,
								EndTimeUnixNano:   36,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, childClientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.EngineParentClientIDAttr, rootClientID),
									kvString(t, telemetry.EngineClientKindAttr, "nested"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/cli"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, moduleLoadHex),
								ParentSpanId:      mustDecodeHex(t, rootConnectHex),
								Name:              "load module: " + moduleRef,
								StartTimeUnixNano: 3,
								EndTimeUnixNano:   4,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, rootClientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/dagql"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, moduleSourceHex),
								ParentSpanId:      mustDecodeHex(t, rootConnectHex),
								Name:              "Query.moduleSource",
								StartTimeUnixNano: 5,
								EndTimeUnixNano:   10,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, rootClientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-module-source"),
									kvString(t, telemetry.DagOutputAttr, "state-module-source"),
									kvString(t, telemetry.DagOutputStateAttr, encodeOutputStateB64(t, moduleSourceState)),
									kvString(t, telemetry.DagOutputStateVersionAttr, telemetry.DagOutputStateVersionV2),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-module-source",
										Field:  "moduleSource",
										Type:   &callpbv1.Type{NamedType: "ModuleSource"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, asModuleHex),
								ParentSpanId:      mustDecodeHex(t, rootConnectHex),
								Name:              "ModuleSource.asModule",
								StartTimeUnixNano: 11,
								EndTimeUnixNano:   14,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, rootClientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-as-module"),
									kvString(t, telemetry.DagOutputAttr, "state-module"),
									kvString(t, telemetry.DagOutputStateAttr, encodeOutputStateB64(t, moduleState)),
									kvString(t, telemetry.DagOutputStateVersionAttr, telemetry.DagOutputStateVersionV2),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-as-module",
										ReceiverDigest: "state-module-source",
										Field:          "asModule",
										Type:           &callpbv1.Type{NamedType: "Module"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, wolfiHex),
								ParentSpanId:      mustDecodeHex(t, rootConnectHex),
								Name:              "Query.wolfi",
								StartTimeUnixNano: 20,
								EndTimeUnixNano:   24,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, rootClientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-wolfi"),
									kvString(t, telemetry.DagOutputAttr, "state-wolfi"),
									kvString(t, telemetry.DagOutputStateAttr, encodeOutputStateB64(t, wolfiState)),
									kvString(t, telemetry.DagOutputStateVersionAttr, telemetry.DagOutputStateVersionV2),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-wolfi",
										Field:  "wolfi",
										Type:   &callpbv1.Type{NamedType: "Wolfi"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, terminalHex),
								ParentSpanId:      mustDecodeHex(t, rootConnectHex),
								Name:              "Wolfi.container",
								StartTimeUnixNano: 25,
								EndTimeUnixNano:   34,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, rootClientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-container"),
									kvString(t, telemetry.DagOutputAttr, "state-container"),
									kvString(t, telemetry.DagOutputStateAttr, encodeOutputStateB64(t, containerState)),
									kvString(t, telemetry.DagOutputStateVersionAttr, telemetry.DagOutputStateVersionV2),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-container",
										ReceiverDigest: "state-wolfi",
										Field:          "container",
										Type:           &callpbv1.Type{NamedType: "Container"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, childContainerHex),
								ParentSpanId:      mustDecodeHex(t, terminalHex),
								Name:              "Alpine.container",
								StartTimeUnixNano: 37,
								EndTimeUnixNano:   44,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, childClientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-child-container"),
									kvString(t, telemetry.DagOutputAttr, "state-container"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-child-container",
										ReceiverDigest: "state-alpine",
										Field:          "container",
										Type:           &callpbv1.Type{NamedType: "Container"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, childNoiseHex),
								ParentSpanId:      mustDecodeHex(t, childContainerHex),
								Name:              "Query.http",
								StartTimeUnixNano: 45,
								EndTimeUnixNano:   52,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, childClientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-http"),
									kvString(t, telemetry.DagOutputAttr, "state-noise"),
									kvString(t, telemetry.DagOutputStateAttr, encodeOutputStateB64(t, noiseState)),
									kvString(t, telemetry.DagOutputStateVersionAttr, telemetry.DagOutputStateVersionV2),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-http",
										Field:  "http",
										Type:   &callpbv1.Type{NamedType: "File"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
				},
			},
		},
	}

	ingestBody, err := proto.Marshal(reqPB)
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}
	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(ingestBody))
	ingestRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("ingest failed: status=%d body=%s", ingestRec.Code, ingestRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/pipelines/object-dag?traceID="+traceIDHex+"&callID="+terminalHex, nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pipeline object dag failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Context struct {
			OutputDagqlID string `json:"outputDagqlID"`
			OutputType    string `json:"outputType"`
		} `json:"context"`
		Module struct {
			Ref     string   `json:"ref"`
			CallIDs []string `json:"callIDs"`
		} `json:"module"`
		Objects []struct {
			DagqlID           string   `json:"dagqlID"`
			TypeName          string   `json:"typeName"`
			Role              string   `json:"role"`
			Placeholder       bool     `json:"placeholder"`
			ProducedByCallIDs []string `json:"producedByCallIDs"`
		} `json:"objects"`
		Edges []struct {
			Kind        string `json:"kind"`
			FromDagqlID string `json:"fromDagqlID"`
			ToDagqlID   string `json:"toDagqlID"`
			Label       string `json:"label"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode pipeline object dag: %v", err)
	}

	if resp.Context.OutputDagqlID != "state-container" || resp.Context.OutputType != "Container" {
		t.Fatalf("unexpected pipeline context: %#v", resp.Context)
	}
	if resp.Module.Ref != moduleRef {
		t.Fatalf("unexpected module ref: %#v", resp.Module)
	}
	if !sameStrings(resp.Module.CallIDs, []string{
		traceIDHex + "/" + moduleSourceHex,
		traceIDHex + "/" + asModuleHex,
	}) {
		t.Fatalf("unexpected module call ids: %#v", resp.Module.CallIDs)
	}

	objectsByID := map[string]struct {
		TypeName          string
		Role              string
		Placeholder       bool
		ProducedByCallIDs []string
	}{}
	for _, obj := range resp.Objects {
		objectsByID[obj.DagqlID] = struct {
			TypeName          string
			Role              string
			Placeholder       bool
			ProducedByCallIDs []string
		}{
			TypeName:          obj.TypeName,
			Role:              obj.Role,
			Placeholder:       obj.Placeholder,
			ProducedByCallIDs: obj.ProducedByCallIDs,
		}
	}

	if _, ok := objectsByID["state-module-source"]; ok {
		t.Fatalf("unexpected module prelude object in dag: %#v", objectsByID)
	}
	if _, ok := objectsByID["state-module"]; ok {
		t.Fatalf("unexpected module prelude object in dag: %#v", objectsByID)
	}
	if _, ok := objectsByID["state-noise"]; ok {
		t.Fatalf("unexpected descendant noise object in dag: %#v", objectsByID)
	}

	wolfiObj, ok := objectsByID["state-wolfi"]
	if !ok || wolfiObj.TypeName != "Wolfi" {
		t.Fatalf("missing wolfi chain object: %#v", objectsByID)
	}
	containerObj, ok := objectsByID["state-container"]
	if !ok || containerObj.TypeName != "Container" || containerObj.Role != "output" {
		t.Fatalf("missing container output object: %#v", objectsByID)
	}
	if !sameStrings(containerObj.ProducedByCallIDs, []string{
		traceIDHex + "/" + terminalHex,
		traceIDHex + "/" + childContainerHex,
	}) {
		t.Fatalf("unexpected output produced-by calls: %#v", containerObj.ProducedByCallIDs)
	}
	if _, ok := objectsByID["state-rootfs"]; ok {
		t.Fatalf("unexpected out-of-scope field-ref dependency in dag: %#v", objectsByID)
	}

	edgeSet := map[string]struct{}{}
	for _, edge := range resp.Edges {
		edgeSet[edge.Kind+"|"+edge.FromDagqlID+"|"+edge.ToDagqlID+"|"+edge.Label] = struct{}{}
	}
	if _, ok := edgeSet["call_chain|state-wolfi|state-container|container"]; !ok {
		t.Fatalf("missing receiver-chain edge: %#v", resp.Edges)
	}
	if len(edgeSet) != 1 {
		t.Fatalf("unexpected extra pipeline edges: %#v", resp.Edges)
	}
}

func TestPipelineObjectDAGKeepsChainObjectsWithoutEmittedState(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	traceIDHex := "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
	sessionID := "sess-pipeline-no-state"
	clientID := "client-pipeline-no-state"

	connectHex := "1111111111111111"
	wolfiHex := "2222222222222222"
	terminalHex := "3333333333333333"

	reqPB := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/engine.client"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, connectHex),
								Name:              "connect",
								StartTimeUnixNano: 1,
								EndTimeUnixNano:   2,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.EngineClientKindAttr, "root"),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
					{
						Scope: &commonpb.InstrumentationScope{Name: "dagger.io/dagql"},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, wolfiHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Query.wolfi",
								StartTimeUnixNano: 10,
								EndTimeUnixNano:   12,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-wolfi"),
									kvString(t, telemetry.DagOutputAttr, "state-wolfi"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-wolfi",
										Field:  "wolfi",
										Type:   &callpbv1.Type{NamedType: "Wolfi"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, terminalHex),
								ParentSpanId:      mustDecodeHex(t, connectHex),
								Name:              "Wolfi.container",
								StartTimeUnixNano: 13,
								EndTimeUnixNano:   20,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.EngineClientIDAttr, clientID),
									kvString(t, telemetry.EngineSessionIDAttr, sessionID),
									kvString(t, telemetry.DagDigestAttr, "call-container"),
									kvString(t, telemetry.DagOutputAttr, "state-container"),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-container",
										ReceiverDigest: "state-wolfi",
										Field:          "container",
										Type:           &callpbv1.Type{NamedType: "Container"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
						},
					},
				},
			},
		},
	}

	ingestBody, err := proto.Marshal(reqPB)
	if err != nil {
		t.Fatalf("marshal ingest payload: %v", err)
	}
	ingestReq := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(ingestBody))
	ingestRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(ingestRec, ingestReq)
	if ingestRec.Code != http.StatusCreated {
		t.Fatalf("ingest failed: status=%d body=%s", ingestRec.Code, ingestRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/pipelines/object-dag?traceID="+traceIDHex+"&callID="+terminalHex, nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pipeline object dag failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Context struct {
			OutputDagqlID string `json:"outputDagqlID"`
			OutputType    string `json:"outputType"`
		} `json:"context"`
		Objects []struct {
			DagqlID     string         `json:"dagqlID"`
			TypeName    string         `json:"typeName"`
			OutputState map[string]any `json:"outputState"`
			Role        string         `json:"role"`
		} `json:"objects"`
		Edges []struct {
			Kind        string `json:"kind"`
			FromDagqlID string `json:"fromDagqlID"`
			ToDagqlID   string `json:"toDagqlID"`
			Label       string `json:"label"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode pipeline object dag: %v", err)
	}

	if resp.Context.OutputDagqlID != "state-container" || resp.Context.OutputType != "Container" {
		t.Fatalf("unexpected pipeline context: %#v", resp.Context)
	}

	objectsByID := map[string]struct {
		TypeName    string
		OutputState map[string]any
		Role        string
	}{}
	for _, obj := range resp.Objects {
		objectsByID[obj.DagqlID] = struct {
			TypeName    string
			OutputState map[string]any
			Role        string
		}{
			TypeName:    obj.TypeName,
			OutputState: obj.OutputState,
			Role:        obj.Role,
		}
	}

	wolfiObj, ok := objectsByID["state-wolfi"]
	if !ok || wolfiObj.TypeName != "Wolfi" || wolfiObj.OutputState != nil {
		t.Fatalf("missing wolfi chain object without state payload: %#v", objectsByID)
	}
	containerObj, ok := objectsByID["state-container"]
	if !ok || containerObj.TypeName != "Container" || containerObj.Role != "output" || containerObj.OutputState != nil {
		t.Fatalf("missing container output object without state payload: %#v", objectsByID)
	}

	if len(resp.Edges) != 1 || resp.Edges[0].Kind != "call_chain" || resp.Edges[0].FromDagqlID != "state-wolfi" || resp.Edges[0].ToDagqlID != "state-container" || resp.Edges[0].Label != "container" {
		t.Fatalf("unexpected pipeline chain edges: %#v", resp.Edges)
	}
}

func TestOpenTraceValidation(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	req := httptest.NewRequest(http.MethodPost, "/api/traces/open", bytes.NewBufferString(`{"mode":"collector"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestWebRouteFallbacks(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	listReq := httptest.NewRequest(http.MethodGet, "/", nil)
	listRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list page failed: %d %s", listRec.Code, listRec.Body.String())
	}
	if !strings.Contains(listRec.Body.String(), "v3.js") {
		t.Fatalf("expected v3 app shell html, got %q", listRec.Body.String())
	}

	traceReq := httptest.NewRequest(http.MethodGet, "/traces/abc123", nil)
	traceRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(traceRec, traceReq)
	if traceRec.Code != http.StatusOK {
		t.Fatalf("trace page failed: %d %s", traceRec.Code, traceRec.Body.String())
	}
	if !strings.Contains(traceRec.Body.String(), "Entity Explorer") {
		t.Fatalf("expected trace route to serve v3 shell, got %q", traceRec.Body.String())
	}

	dagReq := httptest.NewRequest(http.MethodGet, "/dag?traceID=abc123", nil)
	dagRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(dagRec, dagReq)
	if dagRec.Code != http.StatusOK {
		t.Fatalf("dag page failed: %d %s", dagRec.Code, dagRec.Body.String())
	}
	if !strings.Contains(dagRec.Body.String(), "Entity Explorer") {
		t.Fatalf("expected dag route to serve v3 shell, got %q", dagRec.Body.String())
	}

	pipelinesReq := httptest.NewRequest(http.MethodGet, "/pipelines", nil)
	pipelinesRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(pipelinesRec, pipelinesReq)
	if pipelinesRec.Code != http.StatusOK {
		t.Fatalf("pipelines page failed: %d %s", pipelinesRec.Code, pipelinesRec.Body.String())
	}
	if !strings.Contains(pipelinesRec.Body.String(), "Entity Explorer") {
		t.Fatalf("expected pipelines route to serve v3 shell, got %q", pipelinesRec.Body.String())
	}

	pipelineReq := httptest.NewRequest(http.MethodGet, "/pipelines/abc123", nil)
	pipelineRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(pipelineRec, pipelineReq)
	if pipelineRec.Code != http.StatusOK {
		t.Fatalf("pipeline detail page failed: %d %s", pipelineRec.Code, pipelineRec.Body.String())
	}
	if !strings.Contains(pipelineRec.Body.String(), "Entity Explorer") {
		t.Fatalf("expected pipeline detail route to serve v3 shell, got %q", pipelineRec.Body.String())
	}

	sessionsReq := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	sessionsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(sessionsRec, sessionsReq)
	if sessionsRec.Code != http.StatusOK {
		t.Fatalf("sessions page failed: %d %s", sessionsRec.Code, sessionsRec.Body.String())
	}
	if !strings.Contains(sessionsRec.Body.String(), "Entity Explorer") {
		t.Fatalf("expected sessions route to serve v3 shell, got %q", sessionsRec.Body.String())
	}

	sessionReq := httptest.NewRequest(http.MethodGet, "/sessions/abc123", nil)
	sessionRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(sessionRec, sessionReq)
	if sessionRec.Code != http.StatusOK {
		t.Fatalf("session detail page failed: %d %s", sessionRec.Code, sessionRec.Body.String())
	}
	if !strings.Contains(sessionRec.Body.String(), "Entity Explorer") {
		t.Fatalf("expected session detail route to serve v3 shell, got %q", sessionRec.Body.String())
	}

	terminalsReq := httptest.NewRequest(http.MethodGet, "/terminals", nil)
	terminalsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(terminalsRec, terminalsReq)
	if terminalsRec.Code != http.StatusOK {
		t.Fatalf("terminals page failed: %d %s", terminalsRec.Code, terminalsRec.Body.String())
	}
	if !strings.Contains(terminalsRec.Body.String(), "Entity Explorer") {
		t.Fatalf("expected terminals route to serve v3 shell, got %q", terminalsRec.Body.String())
	}

	terminalReq := httptest.NewRequest(http.MethodGet, "/terminals/abc123", nil)
	terminalRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(terminalRec, terminalReq)
	if terminalRec.Code != http.StatusOK {
		t.Fatalf("terminal detail page failed: %d %s", terminalRec.Code, terminalRec.Body.String())
	}
	if !strings.Contains(terminalRec.Body.String(), "Entity Explorer") {
		t.Fatalf("expected terminal detail route to serve v3 shell, got %q", terminalRec.Body.String())
	}

	replsReq := httptest.NewRequest(http.MethodGet, "/repls", nil)
	replsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(replsRec, replsReq)
	if replsRec.Code != http.StatusOK {
		t.Fatalf("repls page failed: %d %s", replsRec.Code, replsRec.Body.String())
	}
	if !strings.Contains(replsRec.Body.String(), "Entity Explorer") {
		t.Fatalf("expected repls route to serve v3 shell, got %q", replsRec.Body.String())
	}

	replReq := httptest.NewRequest(http.MethodGet, "/repls/abc123", nil)
	replRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(replRec, replReq)
	if replRec.Code != http.StatusOK {
		t.Fatalf("repl detail page failed: %d %s", replRec.Code, replRec.Body.String())
	}
	if !strings.Contains(replRec.Body.String(), "Entity Explorer") {
		t.Fatalf("expected repl detail route to serve v3 shell, got %q", replRec.Body.String())
	}

	checksReq := httptest.NewRequest(http.MethodGet, "/checks", nil)
	checksRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(checksRec, checksReq)
	if checksRec.Code != http.StatusOK {
		t.Fatalf("checks page failed: %d %s", checksRec.Code, checksRec.Body.String())
	}
	if !strings.Contains(checksRec.Body.String(), "Entity Explorer") {
		t.Fatalf("expected checks route to serve v3 shell, got %q", checksRec.Body.String())
	}

	checkReq := httptest.NewRequest(http.MethodGet, "/checks/abc123", nil)
	checkRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(checkRec, checkReq)
	if checkRec.Code != http.StatusOK {
		t.Fatalf("check detail page failed: %d %s", checkRec.Code, checkRec.Body.String())
	}
	if !strings.Contains(checkRec.Body.String(), "Entity Explorer") {
		t.Fatalf("expected check detail route to serve v3 shell, got %q", checkRec.Body.String())
	}

	workspacesReq := httptest.NewRequest(http.MethodGet, "/workspaces", nil)
	workspacesRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(workspacesRec, workspacesReq)
	if workspacesRec.Code != http.StatusOK {
		t.Fatalf("workspaces page failed: %d %s", workspacesRec.Code, workspacesRec.Body.String())
	}
	if !strings.Contains(workspacesRec.Body.String(), "Entity Explorer") {
		t.Fatalf("expected workspaces route to serve v3 shell, got %q", workspacesRec.Body.String())
	}

	workspaceReq := httptest.NewRequest(http.MethodGet, "/workspaces/abc123", nil)
	workspaceRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(workspaceRec, workspaceReq)
	if workspaceRec.Code != http.StatusOK {
		t.Fatalf("workspace detail page failed: %d %s", workspaceRec.Code, workspaceRec.Body.String())
	}
	if !strings.Contains(workspaceRec.Body.String(), "Entity Explorer") {
		t.Fatalf("expected workspace detail route to serve v3 shell, got %q", workspaceRec.Body.String())
	}

	gitRemotesReq := httptest.NewRequest(http.MethodGet, "/git-remotes", nil)
	gitRemotesRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(gitRemotesRec, gitRemotesReq)
	if gitRemotesRec.Code != http.StatusOK {
		t.Fatalf("git remotes page failed: %d %s", gitRemotesRec.Code, gitRemotesRec.Body.String())
	}
	if !strings.Contains(gitRemotesRec.Body.String(), "Entity Explorer") {
		t.Fatalf("expected git remotes route to serve v3 shell, got %q", gitRemotesRec.Body.String())
	}

	gitRemoteReq := httptest.NewRequest(http.MethodGet, "/git-remotes/abc123", nil)
	gitRemoteRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(gitRemoteRec, gitRemoteReq)
	if gitRemoteRec.Code != http.StatusOK {
		t.Fatalf("git remote detail page failed: %d %s", gitRemoteRec.Code, gitRemoteRec.Body.String())
	}
	if !strings.Contains(gitRemoteRec.Body.String(), "Entity Explorer") {
		t.Fatalf("expected git remote detail route to serve v3 shell, got %q", gitRemoteRec.Body.String())
	}

	servicesReq := httptest.NewRequest(http.MethodGet, "/services", nil)
	servicesRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(servicesRec, servicesReq)
	if servicesRec.Code != http.StatusOK {
		t.Fatalf("services page failed: %d %s", servicesRec.Code, servicesRec.Body.String())
	}
	if !strings.Contains(servicesRec.Body.String(), "Entity Explorer") {
		t.Fatalf("expected services route to serve v3 shell, got %q", servicesRec.Body.String())
	}

	serviceReq := httptest.NewRequest(http.MethodGet, "/services/abc123", nil)
	serviceRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(serviceRec, serviceReq)
	if serviceRec.Code != http.StatusOK {
		t.Fatalf("service detail page failed: %d %s", serviceRec.Code, serviceRec.Body.String())
	}
	if !strings.Contains(serviceRec.Body.String(), "Entity Explorer") {
		t.Fatalf("expected service detail route to serve v3 shell, got %q", serviceRec.Body.String())
	}

	registriesReq := httptest.NewRequest(http.MethodGet, "/registries", nil)
	registriesRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(registriesRec, registriesReq)
	if registriesRec.Code != http.StatusOK {
		t.Fatalf("registries page failed: %d %s", registriesRec.Code, registriesRec.Body.String())
	}
	if !strings.Contains(registriesRec.Body.String(), "Entity Explorer") {
		t.Fatalf("expected registries route to serve v3 shell, got %q", registriesRec.Body.String())
	}

	registryReq := httptest.NewRequest(http.MethodGet, "/registries/abc123", nil)
	registryRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(registryRec, registryReq)
	if registryRec.Code != http.StatusOK {
		t.Fatalf("registry detail page failed: %d %s", registryRec.Code, registryRec.Body.String())
	}
	if !strings.Contains(registryRec.Body.String(), "Entity Explorer") {
		t.Fatalf("expected registry detail route to serve v3 shell, got %q", registryRec.Body.String())
	}
}

func TestDevModeWebHashAndInjection(t *testing.T) {
	t.Parallel()

	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		DBPath:     filepath.Join(t.TempDir(), "odag.db"),
		DevMode:    true,
		WebDir:     testWebDir(t),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.store.Close()
	})

	hashReq := httptest.NewRequest(http.MethodGet, "/__odag_dev_hash", nil)
	hashRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(hashRec, hashReq)
	if hashRec.Code != http.StatusOK {
		t.Fatalf("dev hash failed: %d %s", hashRec.Code, hashRec.Body.String())
	}
	if strings.TrimSpace(hashRec.Body.String()) == "" {
		t.Fatalf("expected non-empty dev hash")
	}

	listReq := httptest.NewRequest(http.MethodGet, "/", nil)
	listRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list page failed: %d %s", listRec.Code, listRec.Body.String())
	}
	if !strings.Contains(listRec.Body.String(), "/__odag_dev_hash") {
		t.Fatalf("expected dev reload script injection, got %q", listRec.Body.String())
	}
}

func testWebDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "web")
}

func kvString(t *testing.T, key, val string) *commonpb.KeyValue {
	t.Helper()
	return &commonpb.KeyValue{
		Key: key,
		Value: &commonpb.AnyValue{
			Value: &commonpb.AnyValue_StringValue{
				StringValue: val,
			},
		},
	}
}

func kvBool(t *testing.T, key string, val bool) *commonpb.KeyValue {
	t.Helper()
	return &commonpb.KeyValue{
		Key: key,
		Value: &commonpb.AnyValue{
			Value: &commonpb.AnyValue_BoolValue{
				BoolValue: val,
			},
		},
	}
}

func kvStringList(t *testing.T, key string, vals []string) *commonpb.KeyValue {
	t.Helper()
	items := make([]*commonpb.AnyValue, 0, len(vals))
	for _, val := range vals {
		items = append(items, &commonpb.AnyValue{
			Value: &commonpb.AnyValue_StringValue{
				StringValue: val,
			},
		})
	}
	return &commonpb.KeyValue{
		Key: key,
		Value: &commonpb.AnyValue{
			Value: &commonpb.AnyValue_ArrayValue{
				ArrayValue: &commonpb.ArrayValue{Values: items},
			},
		},
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	gotCopy := append([]string(nil), got...)
	wantCopy := append([]string(nil), want...)
	sort.Strings(gotCopy)
	sort.Strings(wantCopy)
	for i := range gotCopy {
		if gotCopy[i] != wantCopy[i] {
			return false
		}
	}
	return true
}

func encodeCallB64(t *testing.T, call *callpbv1.Call) string {
	t.Helper()
	b, err := proto.Marshal(call)
	if err != nil {
		t.Fatalf("marshal call: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func encodeOutputStateB64(t *testing.T, payload map[string]any) string {
	t.Helper()
	state := outputStateProtoFromMap(t, payload)
	b, err := proto.Marshal(state)
	if err != nil {
		t.Fatalf("marshal output state: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func outputStateProtoFromMap(t *testing.T, payload map[string]any) *callpbv1.OutputState {
	t.Helper()
	fieldsRaw, _ := payload["fields"].(map[string]any)
	fieldNames := make([]string, 0, len(fieldsRaw))
	for name := range fieldsRaw {
		fieldNames = append(fieldNames, name)
	}
	sort.Strings(fieldNames)
	fields := make([]*callpbv1.OutputStateField, 0, len(fieldNames))
	for _, fallbackName := range fieldNames {
		rawField, ok := fieldsRaw[fallbackName].(map[string]any)
		if !ok {
			continue
		}
		name := fallbackName
		if v, ok := rawField["name"].(string); ok && v != "" {
			name = v
		}
		field := &callpbv1.OutputStateField{
			Name:  name,
			Type:  stringValue(rawField["type"]),
			Value: literalFromAnyForTest(t, rawField["value"]),
			Refs:  stringSliceValue(rawField["refs"]),
		}
		fields = append(fields, field)
	}
	return &callpbv1.OutputState{
		Type:   stringValue(payload["type"]),
		Fields: fields,
	}
}

func literalFromAnyForTest(t *testing.T, value any) *callpbv1.Literal {
	t.Helper()
	switch v := value.(type) {
	case nil:
		return &callpbv1.Literal{Value: &callpbv1.Literal_Null{Null: true}}
	case bool:
		return &callpbv1.Literal{Value: &callpbv1.Literal_Bool{Bool: v}}
	case string:
		return &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: v}}
	case int:
		return &callpbv1.Literal{Value: &callpbv1.Literal_Int{Int: int64(v)}}
	case int64:
		return &callpbv1.Literal{Value: &callpbv1.Literal_Int{Int: v}}
	case float64:
		return &callpbv1.Literal{Value: &callpbv1.Literal_Float{Float: v}}
	case []any:
		items := make([]*callpbv1.Literal, 0, len(v))
		for _, item := range v {
			items = append(items, literalFromAnyForTest(t, item))
		}
		return &callpbv1.Literal{Value: &callpbv1.Literal_List{List: &callpbv1.List{Values: items}}}
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		args := make([]*callpbv1.Argument, 0, len(keys))
		for _, key := range keys {
			args = append(args, &callpbv1.Argument{
				Name:  key,
				Value: literalFromAnyForTest(t, v[key]),
			})
		}
		return &callpbv1.Literal{Value: &callpbv1.Literal_Object{Object: &callpbv1.Object{Values: args}}}
	default:
		t.Fatalf("unsupported output state test literal type %T", value)
		return nil
	}
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func stringSliceValue(v any) []string {
	switch vals := v.(type) {
	case []string:
		return vals
	case []any:
		out := make([]string, 0, len(vals))
		for _, item := range vals {
			s, ok := item.(string)
			if ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func mustDecodeHex(t *testing.T, v string) []byte {
	t.Helper()
	b, err := hex.DecodeString(v)
	if err != nil {
		t.Fatalf("decode hex %q: %v", v, err)
	}
	return b
}
