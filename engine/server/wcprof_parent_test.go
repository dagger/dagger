package server

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// A nested client's request span made directly under the traceparent its
// exec's container got names the exec (its exec.run span) as its causal
// parent, wcprof.parent: its OTel parent is the call that made the exec,
// beside the exec, and the profile needs the work under the exec. A request
// under a span of the client's own, or with no hosting exec, names none.
func TestQueryRequestSpanAttrsNameTheHostExec(t *testing.T) {
	t.Parallel()
	span := func(id byte) trace.SpanContext {
		return trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    trace.TraceID{1},
			SpanID:     trace.SpanID{0xa, 0xb, 0xc, 0xd, 1, 2, 3, id},
			TraceFlags: trace.FlagsSampled,
			Remote:     true,
		})
	}
	host := dagql.OTelHostExec{ExecRun: span(4), Propagated: span(9)}
	md := &engine.ClientMetadata{ClientID: "client"}
	under := func(parent trace.SpanContext, host dagql.OTelHostExec) []attribute.KeyValue {
		ctx := trace.ContextWithRemoteSpanContext(t.Context(), parent)
		return queryRequestSpanAttrs(dagql.ContextWithOTelHostExec(ctx, host), md)
	}
	require.Contains(t, under(span(9), host), attribute.String(telemetryattrs.WcprofParentAttr, "0a0b0c0d01020304"))

	for _, attrs := range [][]attribute.KeyValue{
		under(span(7), host),                 // under a span of the client's own
		under(span(9), dagql.OTelHostExec{}), // no hosting exec
		queryRequestSpanAttrs(t.Context(), md),
	} {
		for _, kv := range attrs {
			require.NotEqual(t, telemetryattrs.WcprofParentAttr, string(kv.Key))
		}
	}
}
