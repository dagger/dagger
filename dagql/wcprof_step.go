package dagql

import (
	"context"

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

// End ends the step, failed when err isn't nil.
func (s Step) End(err error) {
	s.op.EndErr(err)
}
