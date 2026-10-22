package telemetry

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/dagger/dagger/engine/telemetryattrs"
)

// The spans a container starts directly under its traceparent get the exec's
// exec.run span as their causal parent; the others, and a span that names
// its own, keep theirs. A malformed header stamps nothing.
func TestStampWcprofHost(t *testing.T) {
	propagated := trace.SpanID{9}
	execRun := trace.SpanID{0xa, 0xb, 0xc, 0xd, 1, 2, 3, 4}
	header := WcprofHostHeaderValue(propagated, execRun)
	require.Equal(t, "0900000000000000/0a0b0c0d01020304", header)

	own := &commonpb.KeyValue{Key: telemetryattrs.WcprofParentAttr, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "own"}}}
	request := func() *coltracepb.ExportTraceServiceRequest {
		return &coltracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{
			{Name: "sdk span", SpanId: []byte{1}, ParentSpanId: propagated[:]},
			{Name: "child", SpanId: []byte{2}, ParentSpanId: []byte{1}},
			{Name: "engine callback", SpanId: []byte{3}, ParentSpanId: []byte{7, 7, 7, 7, 7, 7, 7, 7}},
			{Name: "named", SpanId: []byte{4}, ParentSpanId: propagated[:], Attributes: []*commonpb.KeyValue{own}},
		}}}}}}
	}
	parents := func(req *coltracepb.ExportTraceServiceRequest) map[string]string {
		out := map[string]string{}
		for _, span := range req.ResourceSpans[0].ScopeSpans[0].Spans {
			for _, kv := range span.Attributes {
				if kv.Key == telemetryattrs.WcprofParentAttr {
					out[span.Name] = kv.Value.GetStringValue()
				}
			}
		}
		return out
	}

	req := request()
	StampWcprofHost(req, header)
	require.Equal(t, map[string]string{"sdk span": "0a0b0c0d01020304", "named": "own"}, parents(req))
	StampWcprofHost(req, header)
	require.Len(t, req.ResourceSpans[0].ScopeSpans[0].Spans[0].Attributes, 1, "a re-export of the same span is stamped once")

	for _, bad := range []string{"", "0900000000000000", "zz00000000000000/0a0b0c0d01020304", "0900000000000000/0a0b", "09/0a0b0c0d01020304"} {
		req := request()
		StampWcprofHost(req, bad)
		require.Equal(t, map[string]string{"named": "own"}, parents(req), "header %q", bad)
	}
}
