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
	if !sameStrings(dagqlIDs, []string{"state-container", "state-dir"}) {
		t.Fatalf("unexpected pipeline object set: %#v", dagqlIDs)
	}
	if len(resp.Edges) != 1 || resp.Edges[0].FromDagqlID != "state-container" || resp.Edges[0].ToDagqlID != "state-dir" || resp.Edges[0].Label != "rootfs" {
		t.Fatalf("unexpected pipeline edges: %#v", resp.Edges)
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
