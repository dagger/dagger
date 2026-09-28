package schema

import (
	"context"
	"fmt"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/hashutil"
)

// moduleOwnershipSchemaServer supplies the default dependency set that schema
// recovery (Query.ModDepsForCall) starts from.
type moduleOwnershipSchemaServer struct {
	*currentTypeDefsTestServer
	root *core.Query
}

func (s *moduleOwnershipSchemaServer) DefaultDeps(context.Context) (*core.SchemaBuilder, error) {
	return core.NewSchemaBuilder(s.root, nil), nil
}

// moduleOwnershipScopedTest runs the production implementation-scoped resolver
// against operational modules attached in distinct sessions.
type moduleOwnershipScopedTest struct {
	t           *testing.T
	cache       *dagql.Cache
	dag         *dagql.Server
	root        *core.Query
	dir         dagql.ObjectResult[*core.Directory]
	moduleCount int
}

func newModuleOwnershipScopedTest(t *testing.T) *moduleOwnershipScopedTest {
	t.Helper()
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })

	srv := &currentTypeDefsTestServer{}
	root := core.NewRoot(nil)
	root.Server = &moduleOwnershipSchemaServer{currentTypeDefsTestServer: srv, root: root}
	dag, err := dagql.NewServer(ctx, root)
	require.NoError(t, err)
	srv.dag = dag
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
	dagql.Fields[*core.TypeDef]{}.Install(dag)
	dagql.Fields[*core.ObjectTypeDef]{}.Install(dag)
	dagql.Fields[*core.FieldTypeDef]{}.Install(dag)

	dir := &core.Directory{}
	dirDetached, err := dagql.NewObjectResultForCall(dir, dag, implementationScopedTestSyntheticCall("ownership-dir", dir))
	require.NoError(t, err)
	return &moduleOwnershipScopedTest{t: t, cache: cache, dag: dag, root: root, dir: dirDetached}
}

func (f *moduleOwnershipScopedTest) session(id string) context.Context {
	ctx := engine.ContextWithClientMetadata(f.t.Context(), &engine.ClientMetadata{ClientID: id, SessionID: id})
	ctx = core.ContextWithQuery(dagql.ContextWithCache(ctx, f.cache), f.root)
	f.t.Cleanup(func() { _ = f.cache.ReleaseSession(context.Background(), id) })
	return ctx
}

func (f *moduleOwnershipScopedTest) release(ctx context.Context) {
	f.t.Helper()
	md, err := engine.ClientMetadataFromContext(ctx)
	require.NoError(f.t, err)
	require.NoError(f.t, f.cache.ReleaseSession(ctx, md.SessionID))
}

func (f *moduleOwnershipScopedTest) assertReleased(msg string) {
	f.t.Helper()
	_, err := f.cache.Prune(f.t.Context(), []dagql.CachePrunePolicy{{All: true}})
	require.NoError(f.t, err)
	require.Equal(f.t, 0, f.cache.Size(), msg)
}

type moduleOwnershipParentOpts struct {
	// name is the module's display name; originalName defaults to name. An
	// alias keeps the original name and changes only the display name.
	name         string
	originalName string
	variant      string
	// definitions installs an object named after the original name with one
	// string field, so the module's constructor lands on Query.
	definitions bool
}

func (f *moduleOwnershipScopedTest) objectTypeDef(name string) (dagql.ObjectResult[*core.TypeDef], *core.ObjectTypeDef) {
	f.t.Helper()
	stringDef := &core.TypeDef{Kind: core.TypeDefKindString}
	stringRes, err := dagql.NewObjectResultForCall(stringDef, f.dag, implementationScopedTestSyntheticCall("ownership-string-"+name, stringDef))
	require.NoError(f.t, err)
	field := core.NewFieldTypeDef("message", stringRes, "", nil)
	fieldRes, err := dagql.NewObjectResultForCall(field, f.dag, implementationScopedTestSyntheticCall("ownership-field-"+name, field))
	require.NoError(f.t, err)
	obj := core.NewObjectTypeDef(name, "", nil)
	obj.Fields = dagql.ObjectResultArray[*core.FieldTypeDef]{fieldRes}
	objRes, err := dagql.NewObjectResultForCall(obj, f.dag, implementationScopedTestSyntheticCall("ownership-object-"+name, obj))
	require.NoError(f.t, err)
	typ := (&core.TypeDef{}).WithObjectTypeDef(objRes)
	typRes, err := dagql.NewObjectResultForCall(typ, f.dag, implementationScopedTestSyntheticCall("ownership-type-"+name, typ))
	require.NoError(f.t, err)
	return typRes, obj
}

// parent attaches an operational module in the session of ctx. Every call
// creates a distinct row: operational identity is per producer, even for
// equal implementations.
func (f *moduleOwnershipScopedTest) parent(ctx context.Context, opts moduleOwnershipParentOpts) dagql.ObjectResult[*core.Module] {
	f.t.Helper()
	if opts.originalName == "" {
		opts.originalName = opts.name
	}
	src := &core.ModuleSource{
		Kind:               core.ModuleSourceKindDir,
		ModuleName:         opts.name,
		ModuleOriginalName: opts.originalName,
		ContextDirectory:   f.dir,
	}
	srcDetached, err := dagql.NewObjectResultForCall(src, f.dag, implementationScopedTestSyntheticCall("ownership-source-"+opts.name, src))
	require.NoError(f.t, err)
	mod := &core.Module{
		NameField:             opts.name,
		OriginalName:          opts.originalName,
		Source:                dagql.NonNull(srcDetached),
		Deps:                  core.NewSchemaBuilder(f.root, nil),
		AsModuleVariantDigest: opts.variant,
	}
	if opts.definitions {
		typRes, _ := f.objectTypeDef(opts.originalName)
		mod.ObjectDefs = dagql.ObjectResultArray[*core.TypeDef]{typRes}
	}
	md, err := engine.ClientMetadataFromContext(ctx)
	require.NoError(f.t, err)
	f.moduleCount++
	modCall := implementationScopedTestSyntheticCall(fmt.Sprintf("ownership-parent-%s-%s-%d", opts.name, md.SessionID, f.moduleCount), mod)
	detached, err := dagql.NewObjectResultForCall(mod, f.dag, modCall)
	require.NoError(f.t, err)
	attachedAny, err := f.cache.GetOrInitCall(ctx, md.SessionID, f.dag, &dagql.CallRequest{ResultCall: modCall}, dagql.ValueFunc(detached))
	require.NoError(f.t, err)
	attached, ok := attachedAny.(dagql.ObjectResult[*core.Module])
	require.True(f.t, ok)
	return attached
}

func (f *moduleOwnershipScopedTest) install(ctx context.Context, dag *dagql.Server, mod dagql.ObjectResult[*core.Module]) {
	f.t.Helper()
	require.NotEmpty(f.t, mod.Self().ObjectDefs)
	objDef := mod.Self().ObjectDefs[0].Self().AsObject.Value.Self()
	require.NoError(f.t, (&core.ModuleObject{Module: mod, TypeDef: objDef}).Install(ctx, dag))
}

func (f *moduleOwnershipScopedTest) scopedDigest(ctx context.Context, mod dagql.ObjectResult[*core.Module]) digest.Digest {
	f.t.Helper()
	sourceDigest, err := mod.Self().Source.Value.Self().SourceImplementationDigest(ctx)
	require.NoError(f.t, err)
	inputs := []string{"Module._implementationScoped", sourceDigest.String()}
	if mod.Self().AsModuleVariantDigest != "" {
		inputs = append(inputs, mod.Self().AsModuleVariantDigest)
	}
	return hashutil.HashStrings(inputs...)
}

func (f *moduleOwnershipScopedTest) frameOf(ctx context.Context, resultID uint64) *dagql.ResultCall {
	f.t.Helper()
	md, err := engine.ClientMetadataFromContext(ctx)
	require.NoError(f.t, err)
	frame, err := f.cache.ResultCallByResultID(ctx, md.SessionID, resultID)
	require.NoError(f.t, err)
	return frame
}

func scopedResultID(t *testing.T, res dagql.AnyResult) uint64 {
	t.Helper()
	id, err := res.ID()
	require.NoError(t, err)
	require.NotNil(t, id)
	return id.EngineResultID()
}

func selectObject(t *testing.T, ctx context.Context, dag *dagql.Server, field string) dagql.ObjectResult[*core.ModuleObject] {
	t.Helper()
	var obj dagql.ObjectResult[*core.ModuleObject]
	require.NoError(t, dag.Select(ctx, dag.Root(), &obj, dagql.Selector{Field: field}))
	return obj
}

// A constructor installed in one session dispatches in another after the
// installing session ends. The installing session's scoped row is gone; the
// calling session scopes the operational module again under the unchanged
// digest formula, the call keeps a stable recipe, the result owns the
// operational module, and releasing the last holder collects everything.
func TestModuleObjectConstructorDispatchesAfterInstallingSessionRelease(t *testing.T) {
	f := newModuleOwnershipScopedTest(t)
	parentCtx, installCtx, firstCtx, nextCtx := f.session("parent"), f.session("install"), f.session("first"), f.session("next")
	parent := f.parent(parentCtx, moduleOwnershipParentOpts{name: "codegen", definitions: true})
	parentID := scopedResultID(t, parent)
	f.install(installCtx, f.dag, parent)
	spec, ok := f.dag.Root().ObjectType().FieldSpec("codegen", "")
	require.True(t, ok)
	require.Equal(t, "codegen", spec.Module.Name)

	// The installing session scopes the module for its own purposes; that
	// row belongs to it alone.
	installScoped, err := core.ImplementationScopedModule(installCtx, parent)
	require.NoError(t, err)
	installScopedID := scopedResultID(t, installScoped)
	expectedDigest := f.scopedDigest(installCtx, parent)
	require.Equal(t, expectedDigest, f.frameOf(installCtx, installScopedID).ContentDigest())
	f.release(installCtx)
	_, err = f.cache.LoadResultByResultID(firstCtx, "first", f.dag, installScopedID)
	require.ErrorContains(t, err, "missing shared result", "nothing pins the installing session's scoped row")

	first := selectObject(t, firstCtx, f.dag, "codegen")
	require.Equal(t, parentID, scopedResultID(t, first.Self().Module), "the payload captures the operational module")
	firstFrame, err := first.ResultCall()
	require.NoError(t, err)
	require.Equal(t, "codegen", firstFrame.Module.Name)
	firstScopedID := firstFrame.Module.ResultRef.ResultID
	require.NotZero(t, firstScopedID)
	require.NotEqual(t, installScopedID, firstScopedID, "the calling session recreates the scoped row")
	require.Equal(t, expectedDigest, f.frameOf(firstCtx, firstScopedID).ContentDigest(), "the scoped digest formula is unchanged")
	firstRecipe, err := first.RecipeDigest(firstCtx)
	require.NoError(t, err)
	require.Nil(t, spec.Module.ResultRef, "the installed schema holds no session's scoped result")

	next := selectObject(t, nextCtx, f.dag, "codegen")
	require.Equal(t, scopedResultID(t, first), scopedResultID(t, next), "an equivalent scope hits the cached constructor")
	nextRecipe, err := next.RecipeDigest(nextCtx)
	require.NoError(t, err)
	require.Equal(t, firstRecipe, nextRecipe)
	var message string
	require.NoError(t, f.dag.Select(nextCtx, next, &message, dagql.Selector{Field: "message"}))
	require.Equal(t, "", message)

	// Variants of the same implementation scope to distinct digests.
	variant := f.parent(parentCtx, moduleOwnershipParentOpts{name: "codegen", variant: "other-variant"})
	variantScoped, err := core.ImplementationScopedModule(nextCtx, variant)
	require.NoError(t, err)
	variantDigest := f.frameOf(nextCtx, scopedResultID(t, variantScoped)).ContentDigest()
	require.Equal(t, f.scopedDigest(nextCtx, variant), variantDigest)
	require.NotEqual(t, expectedDigest, variantDigest)

	// The cached object is now the only holder of the operational module.
	f.release(firstCtx)
	f.release(parentCtx)
	_, err = f.cache.LoadResultByResultID(nextCtx, "next", f.dag, parentID)
	require.NoError(t, err, "the cached object keeps the operational module alive")
	require.NoError(t, f.dag.Select(nextCtx, next, &message, dagql.Selector{Field: "message"}))

	f.release(nextCtx)
	f.assertReleased("objects, scoped rows, and the operational module are released together")
}

// The cache keeps an object's original class as a fallback for schemas that
// do not install its type. That class's fields dispatch after the installing
// session ends, scoping the module the cached object owns through the reading
// server. The object is published without module provenance of its own, so
// nothing but the class refers to any scoped row.
func TestModuleObjectCachedClassDispatchesWithoutInstalledType(t *testing.T) {
	f := newModuleOwnershipScopedTest(t)
	parentCtx, installCtx, readerCtx := f.session("parent"), f.session("install"), f.session("reader")
	parent := f.parent(parentCtx, moduleOwnershipParentOpts{name: "codegen", definitions: true})
	parentID := scopedResultID(t, parent)
	f.install(installCtx, f.dag, parent)
	objDef := parent.Self().ObjectDefs[0].Self().AsObject.Value.Self()
	obj := &core.ModuleObject{Module: parent, TypeDef: objDef}
	objCall := implementationScopedTestSyntheticCall("ownership-cached-object", obj)
	objDetached, err := dagql.NewObjectResultForCall(obj, f.dag, objCall)
	require.NoError(t, err)
	objAny, err := f.cache.GetOrInitCall(installCtx, "install", f.dag, &dagql.CallRequest{ResultCall: objCall}, dagql.ValueFunc(objDetached))
	require.NoError(t, err)
	objID, err := objAny.ID()
	require.NoError(t, err)

	reader, err := dagql.NewServer(readerCtx, f.root)
	require.NoError(t, err)
	moduleClass, ok := f.dag.ObjectType("Module")
	require.True(t, ok)
	reader.InstallObject(moduleClass)
	_, installed := reader.ObjectType("Codegen")
	require.False(t, installed)

	loaded, err := reader.Load(readerCtx, objID)
	require.NoError(t, err)
	require.Equal(t, objID.EngineResultID(), scopedResultID(t, loaded))
	f.release(installCtx)
	f.release(parentCtx)
	_, err = f.cache.LoadResultByResultID(readerCtx, "reader", f.dag, parentID)
	require.NoError(t, err, "the loaded object keeps the operational module alive")

	message, err := loaded.Select(readerCtx, reader, dagql.Selector{Field: "message"})
	require.NoError(t, err)
	require.Equal(t, dagql.String(""), message.Unwrap())
	frame, err := message.ResultCall()
	require.NoError(t, err)
	require.NotZero(t, frame.Module.ResultRef.ResultID)
	require.Equal(t, f.scopedDigest(readerCtx, parent), f.frameOf(readerCtx, frame.Module.ResultRef.ResultID).ContentDigest())
	cachedField, ok := loaded.ObjectType().FieldSpec("message", "")
	require.True(t, ok)
	require.Nil(t, cachedField.Module.ResultRef)

	f.release(readerCtx)
	f.assertReleased("the fallback class holds no rows of its own")
}

// Aliases and SDK bootstrap modules share implementation content with the
// modules whose classes are installed, so their scoped rows are equivalent.
// Dispatch must still execute the exact operational module the class
// captured; schema recovery from the frame is probed separately because it
// selects a canonical equivalent, which is unchanged by ownership.
func TestModuleImplementationScopedEquivalentModulesKeepOperationalModule(t *testing.T) {
	t.Run("renamed alias", func(t *testing.T) {
		f := newModuleOwnershipScopedTest(t)
		parentCtx, installCtx, callCtx := f.session("parent"), f.session("install"), f.session("call")
		original := f.parent(parentCtx, moduleOwnershipParentOpts{name: "codegen", definitions: true})
		renamed := f.parent(parentCtx, moduleOwnershipParentOpts{name: "renamed", originalName: "codegen", definitions: true})
		renamedID := scopedResultID(t, renamed)
		require.Equal(t, f.scopedDigest(parentCtx, original), f.scopedDigest(parentCtx, renamed), "aliases share implementation identity")

		// The original's scoped row exists first, as the alias's equivalent.
		originalScoped, err := core.ImplementationScopedModule(parentCtx, original)
		require.NoError(t, err)
		originalScopedID := scopedResultID(t, originalScoped)

		f.install(installCtx, f.dag, renamed)
		_, ok := f.dag.Root().ObjectType().FieldSpec("renamed", "")
		require.True(t, ok)
		obj := selectObject(t, callCtx, f.dag, "renamed")
		require.Equal(t, renamedID, scopedResultID(t, obj.Self().Module), "dispatch constructs through the alias's operational module")
		frame, err := obj.ResultCall()
		require.NoError(t, err)
		require.Equal(t, "renamed", frame.Module.Name)
		moduleRow := frame.Module.ResultRef.ResultID
		t.Logf("alias frame module row %d; original scoped row %d", moduleRow, originalScopedID)

		// Recovery from the live frame prefers an installed schema module and
		// otherwise the recorded scoped row, so it yields the alias (its
		// operational module or the frame's scoped clone of it), never the
		// equivalent original.
		deps, err := f.root.ModDepsForCall(callCtx, frame)
		require.NoError(t, err)
		names := make([]string, 0, len(deps.Mods()))
		for _, mod := range deps.Mods() {
			names = append(names, mod.Name())
		}
		recovered, ok := deps.Lookup("renamed")
		require.True(t, ok, "schema recovery must select the alias, got %v", names)
		require.Equal(t, "renamed", recovered.ModuleResult().Self().Name())
		require.Len(t, recovered.ModuleResult().Self().ObjectDefs, 1)
		require.Contains(t, []uint64{renamedID, moduleRow}, scopedResultID(t, recovered.ModuleResult()))

		f.release(installCtx)
		f.release(parentCtx)
		require.NoError(t, f.dag.Select(callCtx, obj, new(string), dagql.Selector{Field: "message"}))
		f.release(callCtx)
		f.assertReleased("alias rows are released with their holders")
	})

	t.Run("bootstrap module", func(t *testing.T) {
		f := newModuleOwnershipScopedTest(t)
		core.InstallCoreSchemaLoaders(f.dag)
		parentCtx, installCtx, callCtx := f.session("parent"), f.session("install"), f.session("call")
		bootstrap := f.parent(parentCtx, moduleOwnershipParentOpts{name: "codegen"})
		require.Empty(t, bootstrap.Self().ObjectDefs)
		bootstrapScoped, err := core.ImplementationScopedModule(parentCtx, bootstrap)
		require.NoError(t, err)
		bootstrapScopedID := scopedResultID(t, bootstrapScoped)

		full := f.parent(parentCtx, moduleOwnershipParentOpts{name: "codegen", definitions: true})
		fullID := scopedResultID(t, full)
		require.Equal(t, f.scopedDigest(parentCtx, bootstrap), f.scopedDigest(parentCtx, full), "initialization does not change implementation identity")
		f.install(installCtx, f.dag, full)

		obj := selectObject(t, callCtx, f.dag, "codegen")
		require.Equal(t, fullID, scopedResultID(t, obj.Self().Module), "dispatch constructs through the initialized module")
		require.Len(t, obj.Self().Module.Self().ObjectDefs, 1)
		frame, err := obj.ResultCall()
		require.NoError(t, err)
		moduleRow := frame.Module.ResultRef.ResultID
		t.Logf("frame module row %d; bootstrap scoped row %d", moduleRow, bootstrapScopedID)

		// Recovery from the live frame may list the bootstrap equivalent
		// alongside the defining module; the recovered schema must still
		// define the constructor, and type recovery from the recipe must
		// bind the defining module's class.
		deps, err := f.root.ModDepsForCall(callCtx, frame)
		require.NoError(t, err)
		recovered, ok := deps.Lookup("codegen")
		require.True(t, ok)
		t.Logf("live-frame recovery lookup selected row %d with %d definitions", scopedResultID(t, recovered.ModuleResult()), len(recovered.ModuleResult().Self().ObjectDefs))
		recoveredSchema, err := deps.Schema(callCtx)
		require.NoError(t, err)
		_, ok = recoveredSchema.Root().ObjectType().FieldSpec("codegen", "")
		require.True(t, ok, "the schema recovered from the live frame must define the constructor")
		recipeID, err := obj.RecipeID(callCtx)
		require.NoError(t, err)
		objType, typeServer, ok, err := f.dag.ObjectTypeAndServerForID(callCtx, recipeID)
		require.NoError(t, err)
		require.True(t, ok)
		typeModule, ok := objType.Typed().(*core.ModuleObject)
		require.True(t, ok)
		require.Len(t, typeModule.Module.Self().ObjectDefs, 1, "type recovery must bind the defining module")
		_, ok = typeServer.Root().ObjectType().FieldSpec("codegen", "")
		require.True(t, ok)

		f.release(installCtx)
		f.release(parentCtx)
		require.NoError(t, f.dag.Select(callCtx, obj, new(string), dagql.Selector{Field: "message"}))
		f.release(callCtx)
		f.assertReleased("bootstrap rows are released with their holders")
	})
}
