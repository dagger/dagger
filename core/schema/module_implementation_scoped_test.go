package schema

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
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

func TestModuleImplementationScopedAliasRecipeReplay(t *testing.T) {
	for _, release := range []bool{false, true} {
		t.Run(map[bool]string{false: "live alias", true: "released alias"}[release], func(t *testing.T) {
			f := newImplementationScopedTest(t)
			originalProvenance, err := core.NewUserMod(f.parent).ResultCallModule(f.nextScopedCtx)
			require.NoError(t, err)

			// Aliases share implementation content, but their root constructors have
			// different names. Keep both parents, and only the original scoped result.
			renamedMod := f.parent.Self().Clone()
			renamedMod.NameField = "renamed"
			renamedCall := implementationScopedTestSyntheticCall("renamed-parent", renamedMod)
			renamedDetached, err := dagql.NewObjectResultForCall(renamedMod, f.dag, renamedCall)
			require.NoError(t, err)
			renamedAny, err := f.cache.GetOrInitCall(f.parentCtx, parentSession, f.dag, &dagql.CallRequest{
				ResultCall: renamedCall,
			}, dagql.ValueFunc(renamedDetached))
			require.NoError(t, err)
			renamed, ok := renamedAny.(dagql.ObjectResult[*core.Module])
			require.True(t, ok)
			obj := &core.ModuleObject{Module: renamed, TypeDef: core.NewObjectTypeDef("Codegen", "", nil)}
			require.NoError(t, obj.Install(f.firstScopedCtx, f.dag))
			renamedSpec, ok := f.dag.Root().ObjectType().FieldSpec("renamed", "")
			require.True(t, ok)
			require.NotEqual(t,
				originalProvenance.ResultRef.Call.ContentDigest(),
				renamedSpec.Module.ResultRef.Call.ContentDigest(),
				"implementation-equivalent aliases must retain distinct defining schemas")
			if release {
				require.NoError(t, f.cache.ReleaseSession(f.firstScopedCtx, firstScopedSession))
			}

			result, err := f.dag.Root().Select(f.nextScopedCtx, f.dag, dagql.Selector{Field: "renamed"})
			require.NoError(t, err)
			id, err := result.RecipeID(f.nextScopedCtx)
			require.NoError(t, err)
			frame, err := result.ResultCall()
			require.NoError(t, err)
			require.Equal(t, "renamed", frame.Module.Name)

			// The schema loader mirrors ModDepsForCall: the actual module result,
			// rather than the display-only Module.Name, determines which constructor
			// is installed. Use real ModuleObject installation on that module.
			f.dag.SetResultServerForCall(func(ctx context.Context, frame *dagql.ResultCall) (*dagql.Server, error) {
				module, err := f.dag.Load(ctx, call.NewEngineResultID(frame.Module.ResultRef.ResultID, call.NewType((&core.Module{}).Type())))
				if err != nil {
					return nil, err
				}
				actual, ok := module.(dagql.ObjectResult[*core.Module])
				require.True(t, ok)
				t.Logf("replay %s uses actual module %s", frame.Field, actual.Self().Name())
				root, ok := dagql.UnwrapAs[*core.Query](f.dag.Root())
				require.True(t, ok)
				reader, err := dagql.NewServer(ctx, root)
				if err != nil {
					return nil, err
				}
				moduleClass, ok := f.dag.ObjectType("Module")
				require.True(t, ok)
				reader.InstallObject(moduleClass)
				object := &core.ModuleObject{Module: actual, TypeDef: core.NewObjectTypeDef("Codegen", "", nil)}
				if err := object.Install(ctx, reader); err != nil {
					return nil, err
				}
				return reader, nil
			})
			require.NoError(t, f.cache.ReleaseSession(f.nextScopedCtx, nextScopedSession))
			// Constructors are persistable: evict the warm result so replay must
			// recover its defining schema. Both module parents remain retained.
			_, err = f.cache.Prune(f.parentCtx, []dagql.CachePrunePolicy{{All: true}})
			require.NoError(t, err)
			_, err = f.dag.Load(f.parentCtx, id)
			require.NoError(t, err, "a renamed constructor must replay in its defining module schema")
		})
	}
}

type implementationScopedReplayServer struct {
	*currentTypeDefsTestServer
	defaultDeps *core.SchemaBuilder
}

func (s *implementationScopedReplayServer) DefaultDeps(context.Context) (*core.SchemaBuilder, error) {
	return s.defaultDeps, nil
}

func TestModuleImplementationScopedBootstrapRecipeReplay(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(map[bool]string{false: "parent definition", true: "scoped definition"}[nested], func(t *testing.T) {
			testModuleImplementationScopedBootstrapRecipeReplay(t, nested)
		})
	}
}

func testModuleImplementationScopedBootstrapRecipeReplay(t *testing.T, nested bool) {
	f := newImplementationScopedTest(t)
	root, ok := dagql.UnwrapAs[*core.Query](f.dag.Root())
	require.True(t, ok)
	root.Server = &implementationScopedReplayServer{
		currentTypeDefsTestServer: root.Server.(*currentTypeDefsTestServer),
		defaultDeps:               core.NewSchemaBuilder(root, nil),
	}
	bootstrap, err := core.ImplementationScopedModule(f.parentCtx, f.parent)
	require.NoError(t, err)
	require.Empty(t, bootstrap.Self().ObjectDefs)

	// Initialization adds the schema without changing implementation content.
	// Retain both parents and the bootstrap scope, but not the full scope.
	dagql.Fields[*core.ObjectTypeDef]{}.Install(f.dag)
	dagql.Fields[*core.TypeDef]{}.Install(f.dag)
	obj := core.NewObjectTypeDef("Codegen", "", nil)
	objDef, err := dagql.NewObjectResultForCall(obj, f.dag, implementationScopedTestSyntheticCall("full-object", obj))
	require.NoError(t, err)
	typ := (&core.TypeDef{}).WithObjectTypeDef(objDef)
	typeDef, err := dagql.NewObjectResultForCall(typ, f.dag, implementationScopedTestSyntheticCall("full-type", typ))
	require.NoError(t, err)
	fullMod := f.parent.Self().Clone()
	fullMod.ObjectDefs = dagql.ObjectResultArray[*core.TypeDef]{typeDef}
	fullCall := implementationScopedTestSyntheticCall("full-parent", fullMod)
	fullDetached, err := dagql.NewObjectResultForCall(fullMod, f.dag, fullCall)
	require.NoError(t, err)
	fullAny, err := f.cache.GetOrInitCall(f.parentCtx, parentSession, f.dag, &dagql.CallRequest{ResultCall: fullCall}, dagql.ValueFunc(fullDetached))
	require.NoError(t, err)
	full, ok := fullAny.(dagql.ObjectResult[*core.Module])
	require.True(t, ok)
	fullParent := full
	if nested {
		// Reconstructed schemas install the scoped module itself, so capturing
		// their field provenance adds another scope to the defining recipe.
		full, err = core.ImplementationScopedModule(f.firstScopedCtx, full)
		require.NoError(t, err)
	}
	require.NoError(t, (&core.ModuleObject{Module: full, TypeDef: obj}).Install(f.firstScopedCtx, f.dag))
	spec, ok := f.dag.Root().ObjectType().FieldSpec("codegen", "")
	require.True(t, ok)
	bootstrapCall, err := bootstrap.ResultCall()
	require.NoError(t, err)
	require.Equal(t, bootstrapCall.ContentDigest(), spec.Module.ResultRef.Call.ContentDigest())
	require.NoError(t, f.cache.ReleaseSession(f.firstScopedCtx, firstScopedSession))
	moduleRecipe, err := spec.Module.ResultRef.Call.RecipeID(f.nextScopedCtx)
	require.NoError(t, err)
	// Reinstalling a schema after collection must not capture the surviving
	// bootstrap representative's recipe in the first place.
	const captureSession = "implementation-scoped-recapture"
	captureCtx := engine.ContextWithClientMetadata(f.parentCtx, &engine.ClientMetadata{ClientID: captureSession, SessionID: captureSession})
	t.Cleanup(func() { _ = f.cache.ReleaseSession(context.Background(), captureSession) })
	recaptured, err := core.NewUserMod(fullParent).ResultCallModule(captureCtx)
	require.NoError(t, err)
	recapturedID, err := recaptured.ResultRef.RecipeID(captureCtx)
	require.NoError(t, err)
	parentRecipe, err := fullParent.RecipeID(captureCtx)
	require.NoError(t, err)
	require.Equal(t, parentRecipe.Digest(), recapturedID.Receiver().Digest())
	require.NoError(t, f.cache.ReleaseSession(captureCtx, captureSession))

	// Omitting the content hint forces an ordinary structural hit, which
	// teaches the full recipe digest onto the bootstrap result. Schema loads
	// must verify producing provenance, not trust even an exact digest posting.
	contentHit, err := f.dag.LoadType(f.nextScopedCtx, moduleRecipe.With(call.WithContentDigest("")))
	require.NoError(t, err)
	contentModule, ok := dagql.UnwrapAs[*core.Module](contentHit)
	require.True(t, ok)
	require.Empty(t, contentModule.ObjectDefs, "ordinary loads must still reuse implementation content")
	strictHit, err := f.dag.LoadTypeForSchema(f.nextScopedCtx, moduleRecipe)
	require.NoError(t, err)
	strictModule, ok := dagql.UnwrapAs[*core.Module](strictHit)
	require.True(t, ok)
	require.Len(t, strictModule.ObjectDefs, 1, "strict recipe loading must retain initialized definitions")

	result, err := f.dag.Root().Select(f.nextScopedCtx, f.dag, dagql.Selector{Field: "codegen"})
	require.NoError(t, err)
	actualObj, ok := dagql.UnwrapAs[*core.ModuleObject](result)
	require.True(t, ok)
	require.Len(t, actualObj.Module.Self().ObjectDefs, 1, "dispatch must not bind the bootstrap module")
	id, err := result.RecipeID(f.nextScopedCtx)
	require.NoError(t, err)
	require.Equal(t, moduleRecipe.Digest(), id.Module().ID().Digest(), "dispatch must retain its actual defining recipe")

	replays := 0
	f.dag.SetResultServerForCall(func(ctx context.Context, frame *dagql.ResultCall) (*dagql.Server, error) {
		deps, err := root.ModDepsForCall(ctx, frame)
		if err != nil {
			return nil, err
		}
		module, ok := deps.Lookup("codegen")
		require.True(t, ok)
		actual := module.ModuleResult()
		require.Len(t, actual.Self().ObjectDefs, 1, "replay must install the full defining schema")
		replays++
		reader, err := dagql.NewServer(ctx, root)
		if err != nil {
			return nil, err
		}
		moduleClass, ok := f.dag.ObjectType("Module")
		require.True(t, ok)
		reader.InstallObject(moduleClass)
		for _, def := range actual.Self().ObjectDefs {
			if err := (&core.ModuleObject{Module: actual, TypeDef: def.Self().AsObject.Value.Self()}).Install(ctx, reader); err != nil {
				return nil, err
			}
		}
		return reader, nil
	})
	require.NoError(t, f.cache.ReleaseSession(f.nextScopedCtx, nextScopedSession))
	_, err = f.cache.Prune(f.parentCtx, []dagql.CachePrunePolicy{{All: true}})
	require.NoError(t, err)
	// Lazy type discovery must load the full schema without evaluating the
	// object: this deliberately names a constructor that does not exist.
	typeOnlyID := call.New().Append(result.Type(), "missingConstructor", call.WithModule(id.Module()))
	const typeSession = "implementation-scoped-type-discovery"
	typeCtx := engine.ContextWithClientMetadata(f.parentCtx, &engine.ClientMetadata{ClientID: typeSession, SessionID: typeSession})
	t.Cleanup(func() { _ = f.cache.ReleaseSession(context.Background(), typeSession) })
	_, _, ok, err = f.dag.ObjectTypeAndServerForID(typeCtx, typeOnlyID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 1, replays)
	require.NoError(t, f.cache.ReleaseSession(typeCtx, typeSession))
	_, err = f.cache.Prune(f.parentCtx, []dagql.CachePrunePolicy{{All: true}})
	require.NoError(t, err)
	_, err = f.dag.Load(f.parentCtx, id)
	require.NoError(t, err)
	require.Equal(t, 2, replays, "the pruned constructor must replay its defining schema")
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
