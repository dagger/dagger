package server

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"dagger.io/dagger/telemetry"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func TestSpanRecordsFromOTLP(t *testing.T) {
	t.Parallel()

	req := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						{
							Key: "service.name",
							Value: &commonpb.AnyValue{
								Value: &commonpb.AnyValue_StringValue{
									StringValue: "dagger-cli",
								},
							},
						},
					},
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{
							Name:    "dagger.io/dagql",
							Version: "1.0.0",
						},
						Spans: []*tracepb.Span{
							{
								TraceId:           mustHex(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
								SpanId:            mustHex(t, "bbbbbbbbbbbbbbbb"),
								ParentSpanId:      mustHex(t, "cccccccccccccccc"),
								Name:              "Query.container",
								StartTimeUnixNano: 10,
								EndTimeUnixNano:   20,
								Attributes: []*commonpb.KeyValue{
									{
										Key: telemetry.DagDigestAttr,
										Value: &commonpb.AnyValue{
											Value: &commonpb.AnyValue_StringValue{
												StringValue: "dgst1",
											},
										},
									},
									{
										Key: telemetry.DagInputsAttr,
										Value: &commonpb.AnyValue{
											Value: &commonpb.AnyValue_ArrayValue{
												ArrayValue: &commonpb.ArrayValue{
													Values: []*commonpb.AnyValue{
														{
															Value: &commonpb.AnyValue_StringValue{
																StringValue: "i1",
															},
														},
														{
															Value: &commonpb.AnyValue_StringValue{
																StringValue: "i2",
															},
														},
													},
												},
											},
										},
									},
								},
								Status: &tracepb.Status{
									Code:    tracepb.Status_STATUS_CODE_OK,
									Message: "",
								},
							},
						},
					},
				},
			},
		},
	}

	records, err := spanRecordsFromOTLP(req)
	if err != nil {
		t.Fatalf("convert OTLP spans: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 span record, got %d", len(records))
	}

	record := records[0]
	if record.TraceID != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("unexpected trace id: %q", record.TraceID)
	}
	if record.SpanID != "bbbbbbbbbbbbbbbb" {
		t.Fatalf("unexpected span id: %q", record.SpanID)
	}
	if record.ParentSpanID != "cccccccccccccccc" {
		t.Fatalf("unexpected parent span id: %q", record.ParentSpanID)
	}
	if record.StatusCode != "STATUS_CODE_OK" {
		t.Fatalf("unexpected status code: %q", record.StatusCode)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(record.DataJSON), &payload); err != nil {
		t.Fatalf("unmarshal data_json: %v", err)
	}

	attrs, ok := payload["attributes"].(map[string]any)
	if !ok {
		t.Fatalf("attributes missing from data_json: %#v", payload)
	}
	if got := attrs[telemetry.DagDigestAttr]; got != "dgst1" {
		t.Fatalf("unexpected dag digest attr: %v", got)
	}
	inputs, ok := attrs[telemetry.DagInputsAttr].([]any)
	if !ok || len(inputs) != 2 || inputs[0] != "i1" || inputs[1] != "i2" {
		t.Fatalf("unexpected dag inputs attr: %#v", attrs[telemetry.DagInputsAttr])
	}

	resource, ok := payload["resource"].(map[string]any)
	if !ok || resource["service.name"] != "dagger-cli" {
		t.Fatalf("unexpected resource payload: %#v", payload["resource"])
	}
	scope, ok := payload["scope"].(map[string]any)
	if !ok || scope["name"] != "dagger.io/dagql" || scope["version"] != "1.0.0" {
		t.Fatalf("unexpected scope payload: %#v", payload["scope"])
	}
}

func TestSpanRecordsFromOTLPTreatsZeroParentAsEmpty(t *testing.T) {
	t.Parallel()

	req := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Spans: []*tracepb.Span{
							{
								TraceId:      mustHex(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
								SpanId:       mustHex(t, "bbbbbbbbbbbbbbbb"),
								ParentSpanId: mustHex(t, "0000000000000000"),
								Name:         "Query.container",
							},
						},
					},
				},
			},
		},
	}

	records, err := spanRecordsFromOTLP(req)
	if err != nil {
		t.Fatalf("convert OTLP spans: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 span record, got %d", len(records))
	}
	if records[0].ParentSpanID != "" {
		t.Fatalf("expected zero parent span id to normalize to empty, got %q", records[0].ParentSpanID)
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("decode hex %q: %v", s, err)
	}
	return b
}
