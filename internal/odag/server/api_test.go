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
	"strings"
	"testing"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/call/callpbv1"
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
			SpanID           string `json:"spanID"`
			DerivedOperation string `json:"derivedOperation"`
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

	v2BindingsReq := httptest.NewRequest(http.MethodGet, "/api/v2/object-bindings?traceID="+traceIDHex, nil)
	v2BindingsRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(v2BindingsRec, v2BindingsReq)
	if v2BindingsRec.Code != http.StatusOK {
		t.Fatalf("v2 object-bindings failed: status=%d body=%s", v2BindingsRec.Code, v2BindingsRec.Body.String())
	}
	var v2BindingsResp struct {
		Items []struct {
			BindingID string   `json:"bindingID"`
			TraceID   string   `json:"traceID"`
			Archived  bool     `json:"archived"`
			History   []string `json:"snapshotHistory"`
		} `json:"items"`
	}
	if err := json.Unmarshal(v2BindingsRec.Body.Bytes(), &v2BindingsResp); err != nil {
		t.Fatalf("decode v2 object-bindings: %v", err)
	}
	if len(v2BindingsResp.Items) != 1 {
		t.Fatalf("expected 1 v2 binding, got %#v", v2BindingsResp.Items)
	}
	if v2BindingsResp.Items[0].TraceID != traceIDHex || len(v2BindingsResp.Items[0].History) != 2 {
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
			SnapshotID string `json:"snapshotID"`
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

func TestV2RenderEndpoints(t *testing.T) {
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

	traceIDHex := "dddddddddddddddddddddddddddddddd"
	call1SpanHex := "1111111111111111"
	call2SpanHex := "2222222222222222"
	call3SpanHex := "3333333333333333"

	call1State := map[string]any{
		"type": "Directory",
		"fields": map[string]any{
			"path": map[string]any{
				"name":  "path",
				"type":  "String",
				"value": "/work",
			},
		},
	}
	containerState := map[string]any{
		"type": "Container",
		"fields": map[string]any{
			"mounted": map[string]any{
				"name":  "mounted",
				"type":  "Directory",
				"value": "state-a",
			},
		},
	}

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
								SpanId:            mustDecodeHex(t, call1SpanHex),
								Name:              "Query.directory",
								StartTimeUnixNano: 10,
								EndTimeUnixNano:   20,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.DagDigestAttr, "call-a"),
									kvString(t, telemetry.DagOutputAttr, "state-a"),
									kvString(t, telemetry.DagOutputStateAttr, encodeOutputStateB64(t, call1State)),
									kvString(t, telemetry.DagOutputStateVersionAttr, telemetry.DagOutputStateVersionV1),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-a",
										Field:  "directory",
										Type:   &callpbv1.Type{NamedType: "Directory"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, call2SpanHex),
								ParentSpanId:      mustDecodeHex(t, call1SpanHex),
								Name:              "Query.container",
								StartTimeUnixNano: 25,
								EndTimeUnixNano:   35,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.DagDigestAttr, "call-b"),
									kvString(t, telemetry.DagOutputAttr, "state-b"),
									kvString(t, telemetry.DagOutputStateAttr, encodeOutputStateB64(t, containerState)),
									kvString(t, telemetry.DagOutputStateVersionAttr, telemetry.DagOutputStateVersionV1),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest: "call-b",
										Field:  "container",
										Type:   &callpbv1.Type{NamedType: "Container"},
									})),
								},
								Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK},
							},
							{
								TraceId:           mustDecodeHex(t, traceIDHex),
								SpanId:            mustDecodeHex(t, call3SpanHex),
								ParentSpanId:      mustDecodeHex(t, call2SpanHex),
								Name:              "Container.withMountedDirectory",
								StartTimeUnixNano: 40,
								EndTimeUnixNano:   50,
								Attributes: []*commonpb.KeyValue{
									kvString(t, telemetry.DagDigestAttr, "call-c"),
									kvString(t, telemetry.DagOutputAttr, "state-c"),
									kvString(t, telemetry.DagOutputStateAttr, encodeOutputStateB64(t, containerState)),
									kvString(t, telemetry.DagOutputStateVersionAttr, telemetry.DagOutputStateVersionV1),
									kvString(t, telemetry.DagCallAttr, encodeCallB64(t, &callpbv1.Call{
										Digest:         "call-c",
										ReceiverDigest: "state-b",
										Field:          "withMountedDirectory",
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

	renderReq := httptest.NewRequest(http.MethodGet, "/api/v2/render?traceID="+traceIDHex, nil)
	renderRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(renderRec, renderReq)
	if renderRec.Code != http.StatusOK {
		t.Fatalf("v2 render failed: status=%d body=%s", renderRec.Code, renderRec.Body.String())
	}

	var renderResp struct {
		DerivationVersion string `json:"derivationVersion"`
		Context           struct {
			Mode       string `json:"mode"`
			TraceTitle string `json:"traceTitle"`
		} `json:"context"`
		Objects []struct {
			ObjectID        string         `json:"objectID"`
			Alias           string         `json:"alias"`
			CurrentState    map[string]any `json:"currentState"`
			SnapshotHistory []string       `json:"snapshotHistory"`
		} `json:"objects"`
		Calls []struct {
			CallID string `json:"callID"`
		} `json:"calls"`
		Edges []struct {
			Kind          string `json:"kind"`
			FromID        string `json:"fromID"`
			ToID          string `json:"toID"`
			EvidenceCount int    `json:"evidenceCount"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(renderRec.Body.Bytes(), &renderResp); err != nil {
		t.Fatalf("decode render response: %v", err)
	}
	if renderResp.DerivationVersion == "" {
		t.Fatalf("expected derivationVersion")
	}
	if renderResp.Context.Mode != "global" {
		t.Fatalf("unexpected render mode: %#v", renderResp.Context.Mode)
	}
	if renderResp.Context.TraceTitle == "" {
		t.Fatalf("expected trace title in render context")
	}
	if len(renderResp.Objects) != 2 {
		t.Fatalf("expected 2 render objects, got %#v", renderResp.Objects)
	}
	if len(renderResp.Calls) != 3 {
		t.Fatalf("expected 3 render calls, got %#v", renderResp.Calls)
	}

	byAlias := map[string]string{}
	for _, obj := range renderResp.Objects {
		byAlias[obj.Alias] = obj.ObjectID
	}
	containerID := byAlias["Container#1"]
	directoryID := byAlias["Directory#1"]
	if containerID == "" || directoryID == "" {
		t.Fatalf("unexpected aliases: %#v", byAlias)
	}
	for _, obj := range renderResp.Objects {
		if len(obj.SnapshotHistory) == 0 {
			t.Fatalf("expected snapshot history for object: %#v", obj)
		}
		if obj.ObjectID == containerID {
			fields, ok := obj.CurrentState["fields"].(map[string]any)
			if !ok || len(fields) == 0 {
				t.Fatalf("expected container current state fields, got %#v", obj.CurrentState)
			}
		}
	}

	depEdgeFound := false
	for _, edge := range renderResp.Edges {
		if edge.Kind != "depends-on" {
			continue
		}
		if edge.FromID == containerID && edge.ToID == directoryID && edge.EvidenceCount >= 1 {
			depEdgeFound = true
			break
		}
	}
	if !depEdgeFound {
		t.Fatalf("expected depends-on edge from container to directory, edges=%#v", renderResp.Edges)
	}

	viewReq := httptest.NewRequest(http.MethodGet, "/api/v2/views/object/render?traceID="+traceIDHex+"&focusObjectID="+containerID+"&dependencyHops=0", nil)
	viewRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(viewRec, viewReq)
	if viewRec.Code != http.StatusOK {
		t.Fatalf("v2 object view render failed: status=%d body=%s", viewRec.Code, viewRec.Body.String())
	}
	var viewResp struct {
		Context struct {
			Mode string `json:"mode"`
			View string `json:"view"`
		} `json:"context"`
		Objects []struct {
			ObjectID string `json:"objectID"`
		} `json:"objects"`
	}
	if err := json.Unmarshal(viewRec.Body.Bytes(), &viewResp); err != nil {
		t.Fatalf("decode object view response: %v", err)
	}
	if viewResp.Context.Mode != "object" || viewResp.Context.View != "object" {
		t.Fatalf("unexpected object view context: %#v", viewResp.Context)
	}
	if len(viewResp.Objects) != 1 || viewResp.Objects[0].ObjectID != containerID {
		t.Fatalf("unexpected object view objects: %#v", viewResp.Objects)
	}

	keepReq := httptest.NewRequest(http.MethodGet, "/api/v2/render?traceID="+traceIDHex+"&keepRules=default", nil)
	keepRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(keepRec, keepReq)
	if keepRec.Code != http.StatusOK {
		t.Fatalf("v2 render keepRules failed: status=%d body=%s", keepRec.Code, keepRec.Body.String())
	}
	var keepResp struct {
		Context struct {
			KeepRulesApplied bool `json:"keepRulesApplied"`
		} `json:"context"`
	}
	if err := json.Unmarshal(keepRec.Body.Bytes(), &keepResp); err != nil {
		t.Fatalf("decode keepRules response: %v", err)
	}
	if !keepResp.Context.KeepRulesApplied {
		t.Fatalf("expected keepRulesApplied=true in response context")
	}

	badViewReq := httptest.NewRequest(http.MethodGet, "/api/v2/views/nope/render?traceID="+traceIDHex, nil)
	badViewRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(badViewRec, badViewReq)
	if badViewRec.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request for unknown view, got %d", badViewRec.Code)
	}

	badKeepReq := httptest.NewRequest(http.MethodGet, "/api/v2/render?traceID="+traceIDHex+"&keepRules=wat", nil)
	badKeepRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(badKeepRec, badKeepReq)
	if badKeepRec.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request for invalid keepRules, got %d", badKeepRec.Code)
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
	if !strings.Contains(listRec.Body.String(), "home.js") {
		t.Fatalf("expected traces list page html, got %q", listRec.Body.String())
	}

	traceReq := httptest.NewRequest(http.MethodGet, "/traces/abc123", nil)
	traceRec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(traceRec, traceReq)
	if traceRec.Code != http.StatusOK {
		t.Fatalf("trace page failed: %d %s", traceRec.Code, traceRec.Body.String())
	}
	if !strings.Contains(traceRec.Body.String(), "backBtn") {
		t.Fatalf("expected trace page html, got %q", traceRec.Body.String())
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
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal output state: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func mustDecodeHex(t *testing.T, v string) []byte {
	t.Helper()
	b, err := hex.DecodeString(v)
	if err != nil {
		t.Fatalf("decode hex %q: %v", v, err)
	}
	return b
}
