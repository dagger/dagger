package schema

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

func TestModuleImplementationScopedDoesNotLeaveStaleSelfDependency(t *testing.T) {
	ctx := t.Context()

	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)

	srv := &currentTypeDefsTestServer{}
	root := core.NewRoot(srv)
	dag, err := dagql.NewServer(ctx, root)
	require.NoError(t, err)
	srv.dag = dag
	ctx = core.ContextWithQuery(ctx, root)
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.Module]{Typed: &core.Module{}}))
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.ModuleSource]{Typed: &core.ModuleSource{}}))
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.Directory]{Typed: &core.Directory{}}))
	dagql.Fields[*core.Module]{
		dagql.NodeFunc("_implementationScoped", (&moduleSchema{}).moduleImplementationScoped),
	}.Install(dag)
	dagql.Fields[*core.Directory]{
		dagql.NodeFunc("digest", func(context.Context, dagql.ObjectResult[*core.Directory], struct{}) (dagql.String, error) {
			return dagql.String("test-directory-digest"), nil
		}),
	}.Install(dag)

	const (
		parentSession      = "module-implementation-scoped-parent"
		firstScopedSession = "module-implementation-scoped-first"
		nextScopedSession  = "module-implementation-scoped-next"
	)
	t.Cleanup(func() {
		_ = cache.ReleaseSession(context.Background(), parentSession)
		_ = cache.ReleaseSession(context.Background(), firstScopedSession)
		_ = cache.ReleaseSession(context.Background(), nextScopedSession)
	})

	parentCtx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  parentSession,
		SessionID: parentSession,
	})
	firstScopedCtx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  firstScopedSession,
		SessionID: firstScopedSession,
	})
	nextScopedCtx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  nextScopedSession,
		SessionID: nextScopedSession,
	})

	dir := &core.Directory{}
	dirDetached, err := dagql.NewObjectResultForCall(dir, dag, implementationScopedTestSyntheticCall("implementation-scoped-dir", dir))
	require.NoError(t, err)
	src := &core.ModuleSource{
		ModuleName:         "codegen",
		ModuleOriginalName: "codegen",
		ContextDirectory:   dirDetached,
	}
	srcDetached, err := dagql.NewObjectResultForCall(src, dag, implementationScopedTestSyntheticCall("implementation-scoped-source", src))
	require.NoError(t, err)
	parentMod := &core.Module{
		NameField:         "codegen",
		OriginalName:      "codegen",
		Source:            dagql.NonNull(srcDetached),
		Deps:              core.NewSchemaBuilder(root, nil),
		IncludeSelfInDeps: true,
	}
	parentCall := implementationScopedTestSyntheticCall("implementation-scoped-parent", parentMod)
	parentDetached, err := dagql.NewObjectResultForCall(parentMod, dag, parentCall)
	require.NoError(t, err)

	parentAny, err := cache.GetOrInitCall(parentCtx, parentSession, dag, &dagql.CallRequest{
		ResultCall: parentCall,
	}, dagql.ValueFunc(parentDetached))
	require.NoError(t, err)
	parent, ok := parentAny.(dagql.ObjectResult[*core.Module])
	require.Truef(t, ok, "expected attached module result, got %T", parentAny)

	_, err = core.ImplementationScopedModule(firstScopedCtx, parent)
	require.NoError(t, err)
	require.NoError(t, cache.ReleaseSession(firstScopedCtx, firstScopedSession))

	_, err = core.ImplementationScopedModule(nextScopedCtx, parent)
	require.NoError(t, err)
}

func implementationScopedTestSyntheticCall(op string, typ dagql.Typed) *dagql.ResultCall {
	return &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: op,
		Type:        dagql.NewResultCallType(typ.Type()),
	}
}
