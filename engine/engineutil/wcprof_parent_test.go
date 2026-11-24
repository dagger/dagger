package engineutil

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	enginetel "github.com/dagger/dagger/engine/telemetry"
)

func testCallSpanContext() trace.SpanContext {
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3},
		SpanID:     trace.SpanID{9},
		TraceFlags: trace.FlagsSampled,
	})
}

func testExecRunSpanContext() trace.SpanContext {
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3},
		SpanID:     trace.SpanID{0xa, 0xb, 0xc, 0xd, 1, 2, 3, 4},
		TraceFlags: trace.FlagsSampled,
	})
}

// A nested client's request carries the exec that hosts it, so the engine
// can name the exec as the causal parent of the client's work.
func TestNestedClientRequestCarriesItsHostExec(t *testing.T) {
	t.Parallel()
	host := dagql.OTelHostExec{ExecRun: testExecRunSpanContext(), Propagated: testCallSpanContext()}
	manager := newNestedClientTransportManager(t.Context(), &nestedTransportSessionHandler{}, &engine.ClientMetadata{SessionID: "session", ClientID: "client"}, "parent", true)
	manager.otelHostExec = host
	req := httptest.NewRequest(http.MethodPost, "/query", nil)
	require.Equal(t, host, dagql.OTelHostExecFromContext(manager.hostedRequest(req).Context()))

	manager.otelHostExec = dagql.OTelHostExec{}
	require.Same(t, req, manager.hostedRequest(req), "with no exec span, the request is unchanged")
	require.False(t, dagql.OTelHostExecFromContext(req.Context()).ExecRun.IsValid())
}

// A container's telemetry exports name its exec, for the engine to stamp the
// exec as the causal parent of the spans it starts under its traceparent. A
// header the container sent itself is never passed on.
func TestSetWcprofHostHeader(t *testing.T) {
	t.Parallel()
	h := http.Header{}
	h.Set(enginetel.WcprofHostHeader, "spoofed")
	setWcprofHostHeader(h, dagql.OTelHostExec{ExecRun: testExecRunSpanContext(), Propagated: testCallSpanContext()})
	require.Equal(t, "0900000000000000/0a0b0c0d01020304", h.Get(enginetel.WcprofHostHeader))

	h.Set(enginetel.WcprofHostHeader, "spoofed")
	setWcprofHostHeader(h, dagql.OTelHostExec{Propagated: testCallSpanContext()})
	require.Empty(t, h.Values(enginetel.WcprofHostHeader))
}
