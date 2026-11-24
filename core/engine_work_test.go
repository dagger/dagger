package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/engineutil"
)

// engineWorkCaptureRuntime records, for each function it runs, whether the
// call was made as the engine's own work.
type engineWorkCaptureRuntime struct {
	*moduleOwnershipRuntime
	calls map[string]bool
}

func (r *engineWorkCaptureRuntime) AsRuntime() (Runtime, bool) { return r, true }

func (r *engineWorkCaptureRuntime) CloneForModuleSource(*ModuleSource) SDK { return r }

func (r *engineWorkCaptureRuntime) Runtime(context.Context, *SchemaBuilder, dagql.ObjectResult[*ModuleSource]) (ModuleRuntime, error) {
	return r, nil
}

func (r *engineWorkCaptureRuntime) Call(ctx context.Context, md *engineutil.ExecutionMetadata, call *FunctionCall, mod dagql.ObjectResult[*Module]) error {
	r.calls[call.Name] = call.EngineWork()
	return r.moduleOwnershipRuntime.Call(ctx, md, call, mod)
}

// TestUserFunctionsAreNotEngineWork covers two user functions the engine
// calls through its own internal selects: a module's constructor, while an
// object of it is evaluated, and a check's body. They're the user's work, so
// their calls must stay in the output, unlike an SDK's.
func TestUserFunctionsAreNotEngineWork(t *testing.T) {
	f := newModuleOwnershipTest(t)
	ctx := f.session("engine-work")
	mod := f.module(ctx, "example", moduleOwnershipModuleOpts{explicitConstructor: true})
	capture := &engineWorkCaptureRuntime{f.runtime, map[string]bool{}}
	mod.Self().Source.Value.Self().SDKImpl = capture
	def := mod.Self().ObjectDefs[0].Self().AsObject.Value.Self()
	// Run the function as a check body, through the engine's hidden field.
	def.Functions[0].Self().CheckReturnType = def.Functions[0].Self().ReturnType
	f.install(ctx, f.dag, mod)

	node := &ModTreeNode{Module: mod, Type: mod.Self().ObjectDefs[0], DagqlServer: f.dag}
	var obj dagql.ObjectResult[*ModuleObject]
	require.NoError(t, node.DagqlValue(ctx, &obj))
	check := &Check{Receiver: obj, Function: "check", Inputs: []CallInput{{Name: "label", Value: dagql.String("ok")}}}
	require.NoError(t, check.runFunction(ctx))

	require.Contains(t, capture.calls, "")
	require.Contains(t, capture.calls, "check")
	require.False(t, capture.calls[""], "a user's constructor ran as engine work")
	require.False(t, capture.calls["check"], "a user's check body ran as engine work")
}

// TestExecEngineWork covers which execs start processes that do the
// engine's own work: a function runtime's exec for a call made as engine
// work, and any exec asked for in a context of engine work, such as an exec
// that such a process starts, which has no function call of its own. An
// internal select alone isn't engine work.
func TestExecEngineWork(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  context.Context
		call *FunctionCall
		want bool
	}{
		{name: "user exec", ctx: t.Context()},
		{name: "internal select", ctx: dagql.WithInternalTelemetry(t.Context())},
		{name: "exec asked for by engine work", ctx: WithEngineWork(t.Context()), want: true},
		{name: "runtime of an engine-work call", ctx: t.Context(), call: &FunctionCall{engineWork: true}, want: true},
		{name: "runtime of a user call within engine work", ctx: WithEngineWork(t.Context()), call: &FunctionCall{}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctr := &Container{}
			require.NoError(t, ctr.WithExec(tc.ctx, dagql.ObjectResult[*Container]{}, ContainerExecOpts{ExperimentalPrivilegedNesting: true}, nil, dagql.ObjectResult[*Module]{}, tc.call))
			exec, ok := ctr.Lazy.(*ContainerExecLazy)
			require.True(t, ok)
			require.Equal(t, tc.want, exec.State.EngineWork)
		})
	}
}
