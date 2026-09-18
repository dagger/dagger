package schema

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

const (
	parentSession      = "module-implementation-scoped-parent"
	firstScopedSession = "module-implementation-scoped-first"
	nextScopedSession  = "module-implementation-scoped-next"
)

type implementationScopedTest struct {
	cache          *dagql.Cache
	dag            *dagql.Server
	parent         dagql.ObjectResult[*core.Module]
	parentCtx      context.Context
	firstScopedCtx context.Context
	nextScopedCtx  context.Context
}

func TestModuleImplementationScopedDoesNotLeaveStaleSelfDependency(t *testing.T) {
	f := newImplementationScopedTest(t)
	_, err := core.ImplementationScopedModule(f.firstScopedCtx, f.parent)
	require.NoError(t, err)
	require.NoError(t, f.cache.ReleaseSession(f.firstScopedCtx, firstScopedSession))

	_, err = core.ImplementationScopedModule(f.nextScopedCtx, f.parent)
	require.NoError(t, err)
}

// Installed fields must remain callable after their installing session ends,
// without keeping the old implementation-scoped module alive indefinitely.
func TestModuleImplementationScopedFieldProvenanceAfterSessionRelease(t *testing.T) {
	f := newImplementationScopedTest(t)
	provenance, err := core.NewUserMod(f.parent).ResultCallModule(f.firstScopedCtx)
	require.NoError(t, err)
	require.Zero(t, provenance.ResultRef.ResultID)
	require.NotNil(t, provenance.ResultRef.Call)
	scoped, err := core.ImplementationScopedModule(f.firstScopedCtx, f.parent)
	require.NoError(t, err)
	scopedID, err := scoped.ID()
	require.NoError(t, err)

	calls := 0
	field := dagql.Func("provenanceProbe", func(context.Context, *core.Query, struct{}) (dagql.String, error) {
		calls++
		return dagql.String("ok"), nil
	})
	field.Spec.Module = provenance
	dagql.Fields[*core.Query]{field}.Install(f.dag)

	first, err := f.dag.Root().Select(f.firstScopedCtx, f.dag, dagql.Selector{Field: "provenanceProbe"})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	firstRecipe, err := first.RecipeDigest(f.firstScopedCtx)
	require.NoError(t, err)
	firstCall, err := first.ResultCall()
	require.NoError(t, err)
	require.Nil(t, firstCall.Module.ResultRef.Call, "runtime frames must remain result-backed")
	require.Equal(t, scopedID.EngineResultID(), firstCall.Module.ResultRef.ResultID)
	firstContent, err := firstCall.ContentPreferredDigest(f.firstScopedCtx)
	require.NoError(t, err)
	require.NoError(t, f.cache.ReleaseSession(f.firstScopedCtx, firstScopedSession))
	_, err = f.dag.Load(f.nextScopedCtx, scopedID)
	require.ErrorContains(t, err, "missing shared result", "schema provenance must not pin the old scoped result")

	// The parent is retained, but no field/provenance metadata is reinstalled.
	parentID, err := f.parent.ID()
	require.NoError(t, err)
	_, err = f.dag.Load(f.parentCtx, parentID)
	require.NoError(t, err)
	next, err := f.dag.Root().Select(f.nextScopedCtx, f.dag, dagql.Selector{Field: "provenanceProbe"})
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	nextRecipe, err := next.RecipeDigest(f.nextScopedCtx)
	require.NoError(t, err)
	require.Equal(t, firstRecipe, nextRecipe)
	nextCall, err := next.ResultCall()
	require.NoError(t, err)
	require.Nil(t, nextCall.Module.ResultRef.Call)
	require.NotZero(t, nextCall.Module.ResultRef.ResultID)
	require.NotEqual(t, scopedID.EngineResultID(), nextCall.Module.ResultRef.ResultID)
	nextContent, err := nextCall.ContentPreferredDigest(f.nextScopedCtx)
	require.NoError(t, err)
	require.Equal(t, firstContent, nextContent)
	_, err = f.dag.Root().Select(f.nextScopedCtx, f.dag, dagql.Selector{Field: "provenanceProbe"})
	require.NoError(t, err)
	require.Equal(t, 2, calls, "repeated calls must still hit the cache")

	// Releasing the reader must also permit the parent to be released: schema
	// metadata must not introduce a parent -> scoped -> parent ownership cycle.
	require.NoError(t, f.cache.ReleaseSession(f.nextScopedCtx, nextScopedSession))
	require.NoError(t, f.cache.ReleaseSession(f.parentCtx, parentSession))
	const cleanupSession = "module-implementation-scoped-cleanup"
	cleanupCtx := engine.ContextWithClientMetadata(f.parentCtx, &engine.ClientMetadata{
		ClientID: cleanupSession, SessionID: cleanupSession,
	})
	t.Cleanup(func() { _ = f.cache.ReleaseSession(context.Background(), cleanupSession) })
	_, err = f.dag.Load(cleanupCtx, parentID)
	require.ErrorContains(t, err, "missing shared result")
}

type implementationScopedProbe struct{}

func (*implementationScopedProbe) Type() *ast.Type {
	return &ast.Type{NamedType: "ImplementationScopedProbe", NonNull: true}
}

// The cache retains an object's original class as a fallback for schemas that
// do not install its type. Its fields must remain callable after the session
// that installed their module provenance has ended.
func TestModuleImplementationScopedCachedClassAfterSessionRelease(t *testing.T) {
	f := newImplementationScopedTest(t)
	provenance, err := core.NewUserMod(f.parent).ResultCallModule(f.firstScopedCtx)
	require.NoError(t, err)

	probeValue := &implementationScopedProbe{}
	dagql.Fields[*implementationScopedProbe]{}.Install(f.dag)
	probeCall := implementationScopedTestSyntheticCall("implementation-scoped-probe", probeValue)
	probeDetached, err := dagql.NewObjectResultForCall(probeValue, f.dag, probeCall)
	require.NoError(t, err)
	probeAny, err := f.cache.GetOrInitCall(f.parentCtx, parentSession, f.dag, &dagql.CallRequest{
		ResultCall: probeCall,
	}, dagql.ValueFunc(probeDetached))
	require.NoError(t, err)
	probe, ok := probeAny.(dagql.AnyObjectResult)
	require.True(t, ok)

	calls := 0
	field := dagql.Func("provenanceProbe", func(context.Context, *implementationScopedProbe, struct{}) (dagql.String, error) {
		calls++
		return dagql.String("ok"), nil
	})
	field.Spec.Module = provenance
	dagql.Fields[*implementationScopedProbe]{field}.Install(f.dag)
	_, err = probe.Select(f.firstScopedCtx, f.dag, dagql.Selector{Field: "provenanceProbe"})
	require.NoError(t, err)
	require.Equal(t, 1, calls)

	// Load through a schema without the probe's type, forcing the cache's
	// objClass fallback rather than the caller's current class. Keep the core
	// Module type installed so its provenance recipe can be replayed.
	reader, err := dagql.NewServer(f.nextScopedCtx, core.NewRoot(&currentTypeDefsTestServer{}))
	require.NoError(t, err)
	moduleClass, ok := f.dag.ObjectType("Module")
	require.True(t, ok)
	reader.InstallObject(moduleClass)
	_, installed := reader.ObjectType(probeValue.Type().Name())
	require.False(t, installed)
	probeID, err := probe.ID()
	require.NoError(t, err)
	require.NoError(t, f.cache.ReleaseSession(f.firstScopedCtx, firstScopedSession))

	loaded, err := reader.Load(f.nextScopedCtx, probeID)
	require.NoError(t, err)
	loadedID, err := loaded.ID()
	require.NoError(t, err)
	require.Equal(t, probeID.EngineResultID(), loadedID.EngineResultID())
	cachedField, ok := loaded.ObjectType().FieldSpec("provenanceProbe", "")
	require.True(t, ok)
	require.Zero(t, cachedField.Module.ResultRef.ResultID)
	require.NotNil(t, cachedField.Module.ResultRef.Call)

	_, err = loaded.Select(f.nextScopedCtx, reader, dagql.Selector{Field: "provenanceProbe"})
	require.NoError(t, err)
	require.Equal(t, 2, calls)
}

func TestModuleImplementationScopedFieldProvenanceSeparatesImplementations(t *testing.T) {
	f := newImplementationScopedTest(t)
	otherMod := f.parent.Self().Clone()
	otherMod.AsModuleVariantDigest = "other-implementation"
	otherCall := implementationScopedTestSyntheticCall("other-implementation-parent", otherMod)
	otherDetached, err := dagql.NewObjectResultForCall(otherMod, f.dag, otherCall)
	require.NoError(t, err)
	otherAny, err := f.cache.GetOrInitCall(f.parentCtx, parentSession, f.dag, &dagql.CallRequest{
		ResultCall: otherCall,
	}, dagql.ValueFunc(otherDetached))
	require.NoError(t, err)
	other, ok := otherAny.(dagql.ObjectResult[*core.Module])
	require.True(t, ok)

	firstProvenance, err := core.NewUserMod(f.parent).ResultCallModule(f.firstScopedCtx)
	require.NoError(t, err)
	otherProvenance, err := core.NewUserMod(other).ResultCallModule(f.firstScopedCtx)
	require.NoError(t, err)
	require.Equal(t, firstProvenance.Name, otherProvenance.Name)
	require.Equal(t, firstProvenance.Ref, otherProvenance.Ref)
	require.Equal(t, firstProvenance.Pin, otherProvenance.Pin)

	calls := 0
	field := dagql.Func("provenanceProbe", func(context.Context, *core.Query, struct{}) (dagql.Int, error) {
		calls++
		return dagql.Int(calls), nil
	})
	var results []dagql.AnyResult
	for _, provenance := range []*dagql.ResultCallModule{firstProvenance, otherProvenance, firstProvenance} {
		field.Spec.Module = provenance
		dagql.Fields[*core.Query]{field}.Install(f.dag)
		result, err := f.dag.Root().Select(f.firstScopedCtx, f.dag, dagql.Selector{Field: "provenanceProbe"})
		require.NoError(t, err)
		results = append(results, result)
	}
	require.Equal(t, 2, calls, "implementation changes must isolate cached calls")
	firstRecipe, err := results[0].RecipeDigest(f.firstScopedCtx)
	require.NoError(t, err)
	otherRecipe, err := results[1].RecipeDigest(f.firstScopedCtx)
	require.NoError(t, err)
	require.NotEqual(t, firstRecipe, otherRecipe)
	cachedRecipe, err := results[2].RecipeDigest(f.firstScopedCtx)
	require.NoError(t, err)
	require.Equal(t, firstRecipe, cachedRecipe)
}

func newImplementationScopedTest(t *testing.T) implementationScopedTest {
	t.Helper()
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
		Kind:               core.ModuleSourceKindDir,
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

	return implementationScopedTest{
		cache:          cache,
		dag:            dag,
		parent:         parent,
		parentCtx:      parentCtx,
		firstScopedCtx: firstScopedCtx,
		nextScopedCtx:  nextScopedCtx,
	}
}

func implementationScopedTestSyntheticCall(op string, typ dagql.Typed) *dagql.ResultCall {
	return &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: op,
		Type:        dagql.NewResultCallType(typ.Type()),
	}
}
