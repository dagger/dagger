package dagql

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/engine/wcprof"
	telemetry "github.com/dagger/otel-go"
)

// Step is one step inside an engine operation, such as each phase of
// gathering files into a directory. It is recorded only when the work under
// its context is profiled: as an internal op in wcprof's native profile, and,
// when the engine's telemetry records there too, as an internal OTel span that
// nests the same way. Work that isn't profiled records nothing and allocates
// nothing. End it once.
type Step struct {
	op   *wcprof.Op
	span trace.Span
}

// BeginStep begins a step named <operation>.<step> (for example
// "withFiles.copyFile") under ctx, and returns the context its own steps begin
// under. A step that runs once per item begins once per item.
func BeginStep(ctx context.Context, name string) (context.Context, Step) {
	if !wcprof.Enabled(ctx) {
		return ctx, Step{}
	}
	ctx, op := wcprof.BeginOp(ctx, wcprof.OpKindInternal, name, wcprof.OpOpts{})
	var span trace.Span
	if OTelProfActive(ctx) {
		prev := trace.SpanContextFromContext(ctx)
		ctx, span = Tracer(ctx).Start(ctx, name,
			// Profiling bookkeeping, like dagql.publishResult: not a row in
			// the UI.
			telemetry.Passthrough(),
			telemetry.Internal(),
			trace.WithAttributes(
				attribute.String(telemetryattrs.WcprofOpKindAttr, wcprof.OpKindInternal.String()),
			),
		)
		ctx = MarkProfilingSpan(ctx, prev)
	}
	return ctx, Step{op: op, span: span}
}

// End ends the step, failed when err isn't nil.
func (s Step) End(err error) {
	s.op.EndErr(err)
	if s.span == nil {
		return
	}
	if err != nil {
		s.span.SetStatus(codes.Error, err.Error())
	}
	s.span.End()
}
