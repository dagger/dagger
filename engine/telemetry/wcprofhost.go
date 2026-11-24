package telemetry

import (
	"bytes"
	"encoding/hex"
	"strings"

	"go.opentelemetry.io/otel/trace"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"

	"github.com/dagger/dagger/engine/telemetryattrs"
)

// WcprofHostHeader is the header the engine's per-exec telemetry endpoint sets
// on a container's trace exports, naming the exec the container runs in:
// "<traceparent span id>/<exec.run span id>", as WcprofHostHeaderValue
// formats it. The container's traceparent is the call that made the exec,
// beside the exec's exec.run span, so StampWcprofHost names exec.run as the
// causal parent (wcprof.parent) of the spans the container starts directly
// under it: an SDK's, a dagger CLI's.
const WcprofHostHeader = "X-Dagger-Wcprof-Host"

// WcprofHostHeaderValue is WcprofHostHeader's value for an exec whose
// container's traceparent is propagated and whose exec.run span is execRun.
func WcprofHostHeaderValue(propagated, execRun trace.SpanID) string {
	return propagated.String() + "/" + execRun.String()
}

// StampWcprofHost stamps wcprof.parent, the exec.run span id header names, on
// each span of req whose parent is the traceparent span header names and that
// has no wcprof.parent of its own. A malformed header stamps nothing.
func StampWcprofHost(req *coltracepb.ExportTraceServiceRequest, header string) {
	propagatedHex, execRun, ok := strings.Cut(header, "/")
	if !ok {
		return
	}
	propagated, err := hex.DecodeString(propagatedHex)
	if err != nil || len(propagated) != len(trace.SpanID{}) {
		return
	}
	if id, err := hex.DecodeString(execRun); err != nil || len(id) != len(trace.SpanID{}) {
		return
	}
	for _, rs := range req.GetResourceSpans() {
		for _, ss := range rs.GetScopeSpans() {
			for _, span := range ss.GetSpans() {
				if span == nil || !bytes.Equal(span.GetParentSpanId(), propagated) || hasAttr(span.GetAttributes(), telemetryattrs.WcprofParentAttr) {
					continue
				}
				span.Attributes = append(span.Attributes, &commonpb.KeyValue{
					Key:   telemetryattrs.WcprofParentAttr,
					Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: execRun}},
				})
			}
		}
	}
}

func hasAttr(attrs []*commonpb.KeyValue, key string) bool {
	for _, kv := range attrs {
		if kv.GetKey() == key {
			return true
		}
	}
	return false
}
