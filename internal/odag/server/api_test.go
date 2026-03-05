package server

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

func mustDecodeHex(t *testing.T, v string) []byte {
	t.Helper()
	b, err := hex.DecodeString(v)
	if err != nil {
		t.Fatalf("decode hex %q: %v", v, err)
	}
	return b
}
