package dagql

import (
	"context"
	"net/http"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/engine/wcprof"
	telemetry "github.com/dagger/otel-go"
)

// Step is one step inside an engine operation, such as each phase of
// gathering files into a directory. It is recorded only when the work under
// its context is profiled, and only as an internal op in wcprof's native
// profile, never as an OTel span: the UI hides everything under an internal
// span, and a step can run user-facing work, such as evaluating a lazy input
// and its exec. Work that isn't profiled records nothing and allocates
// nothing. End it once.
type Step struct {
	op *wcprof.Op
}

// BeginStep begins a step named <operation>.<step> (for example
// "withFiles.copyFile") under ctx, and returns the context its own steps begin
// under. A step that runs once per item begins once per item.
func BeginStep(ctx context.Context, name string) (context.Context, Step) {
	if !wcprof.Enabled(ctx) {
		return ctx, Step{}
	}
	ctx, op := wcprof.BeginOp(ctx, wcprof.OpKindInternal, name, wcprof.OpOpts{})
	return ctx, Step{op: op}
}

// RecordStep records a step that already ran from start to end, timed by
// someone else (such as gqlgen's own timings of a request), the same way
// BeginStep does. Nothing can run under it.
func RecordStep(ctx context.Context, name string, start, end time.Time) {
	if !wcprof.Enabled(ctx) || start.IsZero() || end.IsZero() {
		return
	}
	wcprof.RecordOp(ctx, wcprof.OpKindInternal, name, wcprof.OpOpts{}, wcprof.TimeNS(start), wcprof.TimeNS(end), wcprof.OutcomeOK)
	if OTelProfActive(ctx) {
		_, span := Tracer(ctx).Start(ctx, name,
			telemetry.Passthrough(),
			telemetry.Internal(),
			trace.WithTimestamp(start),
			trace.WithAttributes(
				attribute.String(telemetryattrs.WcprofOpKindAttr, wcprof.OpKindInternal.String()),
			),
		)
		span.End(trace.WithTimestamp(end))
	}
}

// ServeHTTPWithWriteStep serves r with h, a handler built for r alone, and,
// when r is profiled, records writing its response as the graphql.write step:
// from the handler returning the response to its being written. A WebSocket
// connection (gqlgen's own test for one), which writes many responses, each
// as it's ready, records none.
func ServeHTTPWithWriteStep(h *handler.Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !wcprof.Enabled(ctx) || r.Header.Get("Upgrade") != "" {
		h.ServeHTTP(w, r)
		return
	}
	var returned time.Time
	h.AroundResponses(func(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
		res := next(ctx)
		returned = time.Now()
		return res
	})
	h.ServeHTTP(w, r)
	RecordStep(ctx, "graphql.write", returned, time.Now())
}

// End ends the step, failed when err isn't nil.
func (s Step) End(err error) {
	s.op.EndErr(err)
}
