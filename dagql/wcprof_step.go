package dagql

import (
	"context"
	"time"

	"github.com/dagger/dagger/engine/wcprof"
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

// End ends the step, failed when err isn't nil.
func (s Step) End(err error) {
	s.op.EndErr(err)
}
