package schema

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/sdk"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// moduleDefinitionTestCache is a cache and server with no snapshot store
// and the real _moduleDefinition declaration installed over a counting
// resolver, so what the tests check is the field's identity as installed.
type moduleDefinitionTestResolver struct {
	runs atomic.Int32
	// typedefs, when set, supplies the definition's object typedefs.
	typedefs func() dagql.ObjectResultArray[*core.TypeDef]
}

func moduleDefinitionTestCache(t *testing.T, path, session string, stub *moduleDefinitionTestResolver) (context.Context, *dagql.Cache, *dagql.Server) {
	t.Helper()
	ctx, cache, srv, _ := moduleDefinitionTestEnv(t, path, session, stub)
	return ctx, cache, srv
}

// moduleDefinitionTestServer is the root server with a settable current
// module: what Query.currentModule answers with is the module the running
// function belongs to, which for discovery is the operation-scoped module.
type moduleDefinitionTestServer struct {
	*currentTypeDefsTestServer
	current atomic.Pointer[dagql.ObjectResult[*core.Module]]
}

func (s *moduleDefinitionTestServer) CurrentModule(context.Context) (dagql.ObjectResult[*core.Module], error) {
	if mod := s.current.Load(); mod != nil {
		return *mod, nil
	}
	return dagql.ObjectResult[*core.Module]{}, fmt.Errorf("no current module")
}

// moduleDefinitionTestEnv is moduleDefinitionTestCache plus the root
// server, for a test that builds the core schema as a module's deps or
// selects the current module.
func moduleDefinitionTestEnv(t *testing.T, path, session string, stub *moduleDefinitionTestResolver) (context.Context, *dagql.Cache, *dagql.Server, *moduleDefinitionTestServer) {
	t.Helper()
	server := &moduleDefinitionTestServer{currentTypeDefsTestServer: &currentTypeDefsTestServer{platform: core.Platform{OS: "linux", Architecture: "arm64"}}}
	query := core.NewRoot(server)
	ctx := core.ContextWithQuery(t.Context(), query)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: session, SessionID: session})
	cache, err := dagql.NewCache(ctx, path, nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	ctx = dagql.ContextWithCache(ctx, cache)
	srv, err := dagql.NewServer(ctx, query)
	require.NoError(t, err)
	server.dag = srv
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.ModuleSource]{Typed: &core.ModuleSource{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.Module]{Typed: &core.Module{}}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.Container]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.File]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.TypeDef]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.ObjectTypeDef]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.SourceMap]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.Directory]{Typed: &core.Directory{}}))
	// The real implementation scoping of modules and sources, over a
	// context directory whose digest is a constant.
	dagql.Fields[*core.Directory]{
		dagql.NodeFunc("digest", func(context.Context, dagql.ObjectResult[*core.Directory], struct{}) (dagql.String, error) {
			return "test-directory-digest", nil
		}),
	}.Install(srv)
	dagql.Fields[*core.Module]{dagql.NodeFunc("_implementationScoped", (&moduleSchema{}).moduleImplementationScoped)}.Install(srv)
	dagql.Fields[*core.ModuleSource]{dagql.NodeFunc("_implementationScoped", (&moduleSourceSchema{}).moduleSourceImplementationScoped)}.Install(srv)
	// The real currentModule: its dynamic input is the implementation-scoped
	// current module, and its name is what an SDK reads before discovery.
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.CurrentModule]{}))
	dagql.Fields[*core.Query]{dagql.FuncWithDynamicInputs("currentModule", (&moduleSchema{}).currentModule, (&moduleSchema{}).currentModuleCacheKey)}.Install(srv)
	dagql.Fields[*core.CurrentModule]{dagql.Func("name", (&moduleSchema{}).currentModuleName)}.Install(srv)
	resolver := func(ctx context.Context, src dagql.ObjectResult[*core.ModuleSource], args moduleDefinitionArgs) (dagql.ObjectResult[*core.Module], error) {
		stub.runs.Add(1)
		runtime, err := args.Runtime.Load(ctx, srv)
		if err != nil {
			return dagql.ObjectResult[*core.Module]{}, err
		}
		def := &core.Module{NameField: args.ModuleName, Description: "definition of " + args.ModuleName, Runtime: dagql.NonNull(runtime)}
		if stub.typedefs != nil {
			def.ObjectDefs = stub.typedefs()
		}
		return dagql.NewObjectResultForCurrentCall(ctx, srv, def)
	}
	dagql.Fields[*core.ModuleSource]{
		moduleDefinitionField(resolver),
		moduleTypesDefinitionField((&moduleSourceSchema{}).moduleSourceModuleTypesDefinition),
	}.Install(srv)
	return ctx, cache, srv, server
}

func withClient(ctx context.Context, client, session string) context.Context {
	return engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: client, SessionID: session})
}

func attachDefinitionTestResult[T dagql.Typed](t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, session, field string, value T) dagql.ObjectResult[T] {
	t.Helper()
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: field, Type: dagql.NewResultCallType(value.Type())}
	res, err := cache.GetOrInitCall(ctx, session, srv, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewObjectResultForCall(value, srv, frame)
	})
	require.NoError(t, err)
	return res.(dagql.ObjectResult[T])
}

type definitionTestInputs struct {
	source  dagql.ObjectResult[*core.ModuleSource]
	runtime dagql.ObjectResult[*core.Container]
	schema  dagql.ObjectResult[*core.File]
}

// definitionTestInputsFor builds the three inputs of a definition: a source
// row named by sourceField carrying the content digest scoped, and runtime
// and schema rows named by their fields.
func definitionTestInputsFor(t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, session, sourceField string, scoped digest.Digest, runtimeField, schemaField string) definitionTestInputs {
	t.Helper()
	dir := attachDefinitionTestResult(t, ctx, cache, srv, session, sourceField+"-context", &core.Directory{Platform: core.Platform{OS: "linux", Architecture: "arm64"}, Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory]), Lazy: &core.DirectoryScratchLazy{LazyState: core.NewLazyState()}})
	src := attachDefinitionTestResult(t, ctx, cache, srv, session, sourceField, &core.ModuleSource{Kind: core.ModuleSourceKindDir, ModuleName: "demo", ModuleOriginalName: "demo", ContextDirectory: dir})
	src, err := src.WithContentDigest(ctx, scoped, call.ExtraDigestLabelRemoteCache)
	require.NoError(t, err)
	runtime := attachDefinitionTestResult(t, ctx, cache, srv, session, runtimeField, &core.Container{Platform: core.Platform{OS: "linux", Architecture: "arm64"}, FS: new(core.LazyAccessor[*core.Directory, *core.Container]), MetaSnapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Container])})
	schema := attachDefinitionTestResult(t, ctx, cache, srv, session, schemaField, &core.File{Platform: core.Platform{OS: "linux", Architecture: "arm64"}, File: new(core.LazyAccessor[string, *core.File]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.File]), Lazy: &core.FileBlobLazy{LazyState: core.NewLazyState(), Filename: schemaField + ".json", Contents: []byte("{}")}})
	return definitionTestInputs{source: src, runtime: runtime, schema: schema}
}

func selectDefinition(t *testing.T, ctx context.Context, srv *dagql.Server, in definitionTestInputs, name string) dagql.ObjectResult[*core.Module] {
	t.Helper()
	runtimeID, err := in.runtime.ID()
	require.NoError(t, err)
	schemaID, err := in.schema.ID()
	require.NoError(t, err)
	var def dagql.ObjectResult[*core.Module]
	require.NoError(t, srv.Select(ctx, in.source, &def, dagql.Selector{
		Field: "_moduleDefinition",
		Args: []dagql.NamedInput{
			{Name: "runtime", Value: dagql.NewID[*core.Container](runtimeID)},
			{Name: "introspectionJson", Value: dagql.NewID[*core.File](schemaID)},
			{Name: "moduleName", Value: dagql.String(name)},
		},
	}))
	return def
}

// The definition is keyed on the scoped source, the runtime, the schema
// file and the loaded name, with no additional per-client input.
func TestModuleDefinitionIdentity(t *testing.T) {
	t.Parallel()
	stub := &moduleDefinitionTestResolver{}
	runs := &stub.runs
	ctx, cache, srv := moduleDefinitionTestCache(t, "", "s1", stub)
	scoped := digest.FromString("scoped source")
	in := definitionTestInputsFor(t, ctx, cache, srv, "s1", "source", scoped, "runtime", "schema")

	first := selectDefinition(t, ctx, srv, in, "demo")
	require.EqualValues(t, 1, runs.Load())
	require.Equal(t, "definition of demo", first.Self().Description)

	// A second client in a second session with the same inputs gets the
	// same row without a run.
	ctx2 := withClient(ctx, "c2", "s2")
	second := selectDefinition(t, ctx2, srv, in, "demo")
	require.EqualValues(t, 1, runs.Load(), "no per-client input: the second client hits")
	require.Same(t, first.Unwrap(), second.Unwrap())

	// The loaded name is part of the key.
	selectDefinition(t, ctx, srv, in, "renamed")
	require.EqualValues(t, 2, runs.Load())

	// A different runtime alone, and a different schema file alone, each
	// change the key.
	otherRuntime := definitionTestInputsFor(t, ctx, cache, srv, "s1", "source", scoped, "runtime-2", "schema")
	selectDefinition(t, ctx, srv, otherRuntime, "demo")
	require.EqualValues(t, 3, runs.Load(), "a changed runtime is a new definition")
	otherSchema := definitionTestInputsFor(t, ctx, cache, srv, "s1", "source", scoped, "runtime", "schema-2")
	selectDefinition(t, ctx, srv, otherSchema, "demo")
	require.EqualValues(t, 4, runs.Load(), "a changed schema file is a new definition")

	// A source with a different content digest, the effect of a source
	// edit, is a new definition; the same digest under another recipe is
	// not, which is the cross-client and cross-engine case.
	edited := definitionTestInputsFor(t, ctx, cache, srv, "s1", "source-edited", digest.FromString("edited source"), "runtime", "schema")
	selectDefinition(t, ctx, srv, edited, "demo")
	require.EqualValues(t, 5, runs.Load(), "a changed source digest is a new definition")
	equivalent := definitionTestInputsFor(t, ctx, cache, srv, "s1", "source-other-recipe", scoped, "runtime", "schema")
	same := selectDefinition(t, ctx, srv, equivalent, "demo")
	require.EqualValues(t, 5, runs.Load(), "the same scoped digest under another recipe hits structurally")
	require.Same(t, first.Unwrap(), same.Unwrap())
}

// Discovery of one definition runs under two scopes that key on the source
// alone unless the definition's identity is carried into them: the SDK
// operation scope (ScopeModuleForSDKOperation, keyed on the operation name
// and the source digest, whose attach returns an existing match) and the
// current module's implementation scope (Module._implementationScoped,
// keyed on the source digest and AsModuleVariantDigest), which is the
// dynamic input Query.currentModule is cached on. This runs both for real
// over the test cache, then selects currentModule.name the way an SDK does
// with each discovery's scoped module as the current one: the same source
// loaded as "demo" and as "renamed" answers with each name, and the
// original selection still answers "demo" afterwards. A changed runtime or
// schema file is a distinct current module too, carrying its own runtime.
func TestModuleDefinitionScopeCoversInputs(t *testing.T) {
	t.Parallel()
	stub := &moduleDefinitionTestResolver{}
	ctx, cache, srv, server := moduleDefinitionTestEnv(t, "", "s1", stub)
	scoped := digest.FromString("scoped source")
	base := definitionTestInputsFor(t, ctx, cache, srv, "s1", "source", scoped, "runtime", "schema")
	otherRuntime := definitionTestInputsFor(t, ctx, cache, srv, "s1", "source", scoped, "runtime-2", "schema")
	otherSchema := definitionTestInputsFor(t, ctx, cache, srv, "s1", "source", scoped, "runtime", "schema-2")
	s := &moduleSourceSchema{}
	type scopes struct {
		op            string
		operation     uint64
		currentModule uint64
		runtime       uint64
	}
	// discover builds the discovery module and its operation-scoped module,
	// as moduleDefinitionFromRuntime does before it runs the runtime.
	discover := func(in definitionTestInputs, name string) (dagql.ObjectResult[*core.Module], string) {
		mod, op, err := s.moduleDefinitionDiscovery(ctx, in.source, in.runtime, in.schema, name)
		require.NoError(t, err)
		operation, err := sdk.ScopeModuleForSDKOperation(ctx, mod, op, srv)
		require.NoError(t, err)
		return operation, op
	}
	scope := func(in definitionTestInputs, name string) scopes {
		operation, op := discover(in, name)
		current, err := core.ImplementationScopedModule(ctx, operation)
		require.NoError(t, err)
		require.True(t, current.Self().Runtime.Valid)
		return scopes{op: op, operation: persistedID(t, cache, operation), currentModule: persistedID(t, cache, current), runtime: persistedID(t, cache, current.Self().Runtime.Value)}
	}
	// currentModule selects Query.currentModule and its name with operation
	// as the running function's module, through the real dynamic-input key.
	currentModule := func(operation dagql.ObjectResult[*core.Module]) (string, uint64) {
		server.current.Store(&operation)
		var current dagql.ObjectResult[*core.CurrentModule]
		require.NoError(t, srv.Select(ctx, srv.Root(), &current, dagql.Selector{Field: "currentModule"}))
		var name dagql.String
		require.NoError(t, srv.Select(ctx, current, &name, dagql.Selector{Field: "name"}))
		require.True(t, current.Self().Module.Self().Runtime.Valid)
		return string(name), persistedID(t, cache, current.Self().Module.Self().Runtime.Value)
	}

	first := scope(base, "demo")
	require.Equal(t, first, scope(base, "demo"), "equal inputs share the operation scope and the current module")
	require.Equal(t, persistedID(t, cache, base.runtime), first.runtime)

	renamed := scope(base, "renamed")
	require.NotEqual(t, first.op, renamed.op)
	require.NotEqual(t, first.operation, renamed.operation, "a different name is a different operation scope")
	require.NotEqual(t, first.currentModule, renamed.currentModule, "a different name is a different current module")

	viaOtherRuntime := scope(otherRuntime, "demo")
	require.NotEqual(t, first.operation, viaOtherRuntime.operation, "a different runtime is a different operation scope")
	require.NotEqual(t, first.currentModule, viaOtherRuntime.currentModule)
	require.Equal(t, persistedID(t, cache, otherRuntime.runtime), viaOtherRuntime.runtime, "the current module carries its own runtime, not the first one's")

	viaOtherSchema := scope(otherSchema, "demo")
	require.NotEqual(t, first.operation, viaOtherSchema.operation, "a different schema file is a different operation scope")
	require.NotEqual(t, first.currentModule, viaOtherSchema.currentModule)

	// The behavior an SDK sees: the same source loaded under two names
	// answers currentModule.name with each name, in one session, and the
	// first selection is unchanged after the second.
	demoOperation, _ := discover(base, "demo")
	renamedOperation, _ := discover(base, "renamed")
	name, runtime := currentModule(demoOperation)
	require.Equal(t, "demo", name)
	require.Equal(t, persistedID(t, cache, base.runtime), runtime)
	name, _ = currentModule(renamedOperation)
	require.Equal(t, "renamed", name, "the renamed load's current module is not the first load's")
	name, _ = currentModule(demoOperation)
	require.Equal(t, "demo", name, "the original selection still answers its own name")
	otherRuntimeOperation, _ := discover(otherRuntime, "demo")
	name, runtime = currentModule(otherRuntimeOperation)
	require.Equal(t, "demo", name)
	require.Equal(t, persistedID(t, cache, otherRuntime.runtime), runtime, "the other runtime's current module carries that runtime")
	otherSchemaOperation, _ := discover(otherSchema, "demo")
	_, runtime = currentModule(otherSchemaOperation)
	require.Equal(t, persistedID(t, cache, base.runtime), runtime)
	require.NotEqual(t, persistedID(t, cache, otherSchemaOperation), persistedID(t, cache, demoOperation))

	// The identity is portable: it is made of recipe digests, so a result
	// referenced by a handle names the same definition.
	runtimeID, err := base.runtime.ID()
	require.NoError(t, err)
	byHandle, err := dagql.NewID[*core.Container](call.NewEngineResultID(persistedID(t, cache, base.runtime), runtimeID.Type())).Load(ctx, srv)
	require.NoError(t, err)
	wantIdentity, err := moduleDefinitionIdentity(ctx, base.runtime, base.schema, "demo")
	require.NoError(t, err)
	gotIdentity, err := moduleDefinitionIdentity(ctx, byHandle, base.schema, "demo")
	require.NoError(t, err)
	require.Equal(t, wantIdentity, gotIdentity)
	require.Equal(t, "getModDef:"+wantIdentity.String(), first.op)
}

// staleTypesSDK is an SDK whose persisted moduleTypes capability its loaded
// implementation no longer has (core.ErrStaleSDKCapability), and whose
// runtime is a container.
type staleTypesSDK struct {
	runtime      dagql.ObjectResult[*core.Container]
	typesCalls   atomic.Int32
	runtimeCalls atomic.Int32
}

func (s *staleTypesSDK) AsRuntime() (core.Runtime, bool)                     { return s, true }
func (s *staleTypesSDK) AsModuleTypes() (core.ModuleTypes, bool)             { return s, true }
func (s *staleTypesSDK) AsCodeGenerator() (core.CodeGenerator, bool)         { return nil, false }
func (s *staleTypesSDK) AsClientGenerator() (core.ClientGenerator, bool)     { return nil, false }
func (s *staleTypesSDK) AsModuleInitializer() (core.ModuleInitializer, bool) { return nil, false }
func (s *staleTypesSDK) AsClientInitializer() (core.ClientInitializer, bool) { return nil, false }
func (s *staleTypesSDK) AsRuntimeTarget() (core.RuntimeTarget, bool)         { return nil, false }
func (s *staleTypesSDK) AsModule() (dagql.ObjectResult[*core.Module], bool) {
	return dagql.ObjectResult[*core.Module]{}, false
}
func (s *staleTypesSDK) CloneForModuleSource(*core.ModuleSource) core.SDK { return s }
func (s *staleTypesSDK) AttachDependencyResults(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return nil, nil
}

func (s *staleTypesSDK) Runtime(context.Context, *core.SchemaBuilder, dagql.ObjectResult[*core.ModuleSource]) (core.ModuleRuntime, error) {
	s.runtimeCalls.Add(1)
	return &core.ContainerRuntime{Container: s.runtime}, nil
}

func (s *staleTypesSDK) ModuleTypes(context.Context, *core.SchemaBuilder, dagql.ObjectResult[*core.ModuleSource], *core.Module) (dagql.ObjectResult[*core.Module], error) {
	s.typesCalls.Add(1)
	return dagql.ObjectResult[*core.Module]{}, fmt.Errorf("persisted module source sdk does not implement module types: %w", core.ErrStaleSDKCapability)
}

// A source persisted with the moduleTypes capability whose loaded SDK no
// longer has it takes the runtime path: the real dispatch in
// runModuleDefInSDK sees the wrapped ErrStaleSDKCapability, selects the
// cached definition through the runtime, and the module keeps that runtime.
func TestModuleDefinitionStaleCapabilityFallsBackToRuntime(t *testing.T) {
	t.Parallel()
	stub := &moduleDefinitionTestResolver{}
	ctx, cache, srv, server := moduleDefinitionTestEnv(t, "", "stale", stub)
	// The module's deps are the core schema, so the introspection file the
	// definition is keyed on is the real one.
	base, err := NewCoreSchemaBase(ctx, server)
	require.NoError(t, err)
	deps := core.NewSchemaBuilder(core.NewRoot(server), []core.Mod{base.CoreMod("")})
	in := definitionTestInputsFor(t, ctx, cache, srv, "stale", "source", digest.FromString("scoped source"), "runtime", "schema")
	sdkImpl := &staleTypesSDK{runtime: in.runtime}
	source := &core.ModuleSource{Kind: core.ModuleSourceKindDir, ModuleName: "demo", ModuleOriginalName: "demo", ContextDirectory: in.source.Self().ContextDirectory, SDK: &core.SDKConfig{Source: "scripted"}, SDKImpl: sdkImpl}
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "stale-source", Type: dagql.NewResultCallType(source.Type())}
	srcAny, err := cache.GetOrInitCall(ctx, "stale", srv, &dagql.CallRequest{ResultCall: frame}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewObjectResultForCall(source, srv, frame)
	})
	require.NoError(t, err)
	src := srcAny.(dagql.ObjectResult[*core.ModuleSource])
	mod := &core.Module{Source: dagql.NonNull(src), ContextSource: dagql.NonNull(src), NameField: "demo", OriginalName: "demo", SDKConfig: source.SDK, Deps: deps}

	loaded, err := (&moduleSourceSchema{}).runModuleDefInSDK(ctx, mod)
	require.NoError(t, err)
	require.EqualValues(t, 1, sdkImpl.typesCalls.Load(), "the capability was tried")
	require.EqualValues(t, 1, sdkImpl.runtimeCalls.Load(), "then the runtime was selected")
	require.EqualValues(t, 1, stub.runs.Load(), "the definition was computed through the cached field")
	require.True(t, loaded.Definition.Valid, "the module references its definition")
	require.Equal(t, "definition of demo", loaded.Definition.Value.Self().Description)
	require.Equal(t, "definition of demo", loaded.Description)
	require.True(t, loaded.Runtime.Valid, "the module keeps the runtime it selected")
	require.Equal(t, persistedID(t, cache, in.runtime), persistedID(t, cache, loaded.Runtime.Value))
	require.Equal(t, persistedID(t, cache, in.runtime), persistedID(t, cache, loaded.Definition.Value.Self().Runtime.Value))

	// A second load of the same source hits the definition without the
	// runtime running again.
	again, err := (&moduleSourceSchema{}).runModuleDefInSDK(ctx, mod)
	require.NoError(t, err)
	require.EqualValues(t, 2, sdkImpl.typesCalls.Load())
	require.EqualValues(t, 1, stub.runs.Load(), "the second load hit the definition")
	require.Equal(t, persistedID(t, cache, loaded.Definition.Value), persistedID(t, cache, again.Definition.Value))
}

// A definition exported inside a module-object leaf and imported into a
// cache with different row numbers is hit there by equivalent inputs
// reconstructed under that cache's own recipes, with the definition's
// typedef, its SourceMap and its runtime reference relocated.
func TestModuleDefinitionImportedHit(t *testing.T) {
	t.Parallel()
	stubA := &moduleDefinitionTestResolver{}
	ctx, a, srvA := moduleDefinitionTestCache(t, filepath.Join(t.TempDir(), "a.db"), "a", stubA)
	scoped := digest.FromString("scoped source")
	inA := definitionTestInputsFor(t, ctx, a, srvA, "a", "source", scoped, "runtime", "schema")
	// The definition's typedef and its SourceMap exist before the
	// definition is attached, as the runtime's answer does.
	sourceMap := attachDefinitionTestResult(t, ctx, a, srvA, "a", "definition-sourcemap", &core.SourceMap{Module: "demo", Filename: "main.go", Line: 3, Column: 1})
	objDef := attachDefinitionTestResult(t, ctx, a, srvA, "a", "definition-object", core.NewObjectTypeDef("Holder", "a holder", nil).WithSourceMap(sourceMap))
	typeDef := attachDefinitionTestResult(t, ctx, a, srvA, "a", "definition-typedef", (&core.TypeDef{}).WithObject(objDef))
	stubA.typedefs = func() dagql.ObjectResultArray[*core.TypeDef] { return dagql.ObjectResultArray[*core.TypeDef]{typeDef} }
	defA := selectDefinition(t, ctx, srvA, inA, "demo")
	require.EqualValues(t, 1, stubA.runs.Load())
	require.Len(t, defA.Self().ObjectDefs, 1)

	// The defining module owns the definition, as asModule's result does,
	// and a module object of that module is the leaf that gets exported.
	modA := attachDefinitionTestResult(t, ctx, a, srvA, "a", "module", &core.Module{NameField: "demo", Definition: dagql.NonNull(defA), ObjectDefs: dagql.ObjectResultArray[*core.TypeDef]{typeDef}})
	shapeA := &core.ModuleObject{Module: modA, TypeDef: objDef.Self()}
	srvA.InstallObject(dagql.NewClass(srvA, dagql.ClassOpts[*core.ModuleObject]{Typed: shapeA}))
	holderA := &core.ModuleObject{Module: modA, TypeDef: objDef.Self(), Fields: map[string]any{"label": "x"}}
	// The leaf's call frame names its module by row, as a module function's
	// call does (the provider of userMod.FieldModule), which is what puts the
	// module and its definition in the leaf's exported closure.
	leafFrame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "holder", Type: dagql.NewResultCallType(holderA.Type()), Module: &dagql.ResultCallModule{Name: "demo", ResultRef: &dagql.ResultCallRef{ResultID: persistedID(t, a, modA)}}}
	leaf, err := a.GetOrInitCall(ctx, "a", srvA, &dagql.CallRequest{ResultCall: leafFrame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewObjectResultForCall(holderA, srvA, leafFrame)
	})
	require.NoError(t, err)
	var bundle dagql.ValueBundle
	require.NoError(t, a.WithExportedValues(ctx, dagql.ValueSelection{Roots: []dagql.AnyResult{leaf}}, config.RefConfig{}, func(_ context.Context, values *dagql.ExportedValues) error {
		bundle = values.Bundle
		return nil
	}))
	require.Len(t, bundle.Values, 10, "leaf, module, definition, typedef, object typedef, source map, source, its context directory, runtime, schema")

	stubB := &moduleDefinitionTestResolver{}
	bctx, b, srvB := moduleDefinitionTestCache(t, filepath.Join(t.TempDir(), "b.db"), "b", stubB)
	for i := range 7 {
		attachDefinitionTestResult(t, bctx, b, srvB, "b", "padding", &core.Module{NameField: string(rune('p' + i))})
	}
	mapping, err := b.ImportValues(bctx, bundle)
	require.NoError(t, err)
	require.Len(t, mapping, 1)

	// B reconstructs the inputs under its own recipes: the source under a
	// different recipe with the same scoped digest, the runtime and schema
	// under the same recipes, as a cold engine does.
	inB := definitionTestInputsFor(t, bctx, b, srvB, "b", "source-on-b", scoped, "runtime", "schema")
	defB := selectDefinition(t, bctx, srvB, inB, "demo")
	require.Zero(t, stubB.runs.Load(), "B hits the imported definition without running the resolver")
	require.True(t, dagql.IsImportedResult(defB))
	require.NotEqual(t, persistedID(t, a, defA), persistedID(t, b, defB), "B's row number differs")
	require.Equal(t, "definition of demo", defB.Self().Description)
	require.Len(t, defB.Self().ObjectDefs, 1, "the definition's typedef travelled")
	objB := defB.Self().ObjectDefs[0].Self().AsObject.Value.Self()
	require.Equal(t, "Holder", objB.Name)
	require.Equal(t, "a holder", objB.Description)
	require.True(t, objB.SourceMap.Valid, "the typedef's SourceMap travelled")
	require.Equal(t, &core.SourceMap{Module: "demo", Filename: "main.go", Line: 3, Column: 1}, objB.SourceMap.Value.Self())
	require.True(t, dagql.IsImportedResult(objB.SourceMap.Value))
	require.True(t, defB.Self().Runtime.Valid, "the runtime reference relocated with the definition")
	require.True(t, dagql.IsImportedResult(defB.Self().Runtime.Value))
	require.NotEqual(t, persistedID(t, a, inA.runtime), persistedID(t, b, defB.Self().Runtime.Value))
	require.Equal(t, persistedID(t, b, inB.runtime), persistedID(t, b, defB.Self().Runtime.Value), "B's own runtime lookup is the imported runtime row")

	// The leaf decodes through its module, resolved from the leaf's call
	// frame as the engine does for module-defined results, and that module
	// is B's imported row whose definition is B's definition row.
	var resolvedModules []uint64
	srvB.SetResultServerForCall(func(ctx context.Context, call *dagql.ResultCall) (*dagql.Server, error) {
		if call.Module == nil || call.Module.ResultRef == nil {
			return srvB, nil
		}
		modAny, err := b.LoadResultByResultID(ctx, "b", srvB, call.Module.ResultRef.ResultID)
		if err != nil {
			return nil, err
		}
		modB := modAny.(dagql.ObjectResult[*core.Module])
		resolvedModules = append(resolvedModules, call.Module.ResultRef.ResultID)
		srvB.InstallObject(dagql.NewClass(srvB, dagql.ClassOpts[*core.ModuleObject]{Typed: &core.ModuleObject{Module: modB, TypeDef: modB.Self().ObjectDefs[0].Self().AsObject.Value.Self()}}))
		return srvB, nil
	})
	loaded, err := b.LoadResultByResultID(bctx, "b", srvB, mapping[0].ResultID)
	require.NoError(t, err)
	holder := loaded.Unwrap().(*core.ModuleObject)
	require.Equal(t, map[string]any{"label": "x"}, holder.Fields)
	require.Len(t, resolvedModules, 1, "the leaf's frame named its module by row")
	require.NotEqual(t, persistedID(t, a, modA), resolvedModules[0], "the module row was relocated")
	require.True(t, dagql.IsImportedResult(holder.Module))
	require.True(t, holder.Module.Self().Definition.Valid)
	require.Equal(t, persistedID(t, b, defB), persistedID(t, b, holder.Module.Self().Definition.Value), "the leaf's module references B's definition row")
}

func persistedID(t *testing.T, cache *dagql.Cache, res dagql.AnyResult) uint64 {
	t.Helper()
	id, err := cache.PersistedResultID(res)
	require.NoError(t, err)
	return id
}
