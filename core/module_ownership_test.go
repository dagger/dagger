package core

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
)

// moduleOwnershipRuntime is an in-process stand-in for a container runtime. It
// scopes the module it is handed exactly as Container.WithExec does, so a call
// through a stale operational module fails the same way it would in the
// engine, and it records which operational module executed each call.
type moduleOwnershipRuntime struct {
	moduleSourceAttachTestSDK

	selfCalls bool

	mu               sync.Mutex
	constructorCalls int
	executedModules  []uint64
}

func (sdk *moduleOwnershipRuntime) AlwaysEnablesSelfCalls() bool { return sdk.selfCalls }

func (sdk *moduleOwnershipRuntime) AsRuntime() (Runtime, bool)             { return sdk, true }
func (sdk *moduleOwnershipRuntime) CloneForModuleSource(*ModuleSource) SDK { return sdk }
func (sdk *moduleOwnershipRuntime) Runtime(context.Context, *SchemaBuilder, dagql.ObjectResult[*ModuleSource]) (ModuleRuntime, error) {
	return sdk, nil
}

func (*moduleOwnershipRuntime) AsContainer() (dagql.ObjectResult[*Container], bool) {
	return dagql.ObjectResult[*Container]{}, false
}

func (sdk *moduleOwnershipRuntime) Call(ctx context.Context, _ *engineutil.ExecutionMetadata, fnCall *FunctionCall, mod dagql.ObjectResult[*Module]) error {
	if _, err := ImplementationScopedModule(ctx, mod); err != nil {
		return err
	}
	modID, err := mod.ID()
	if err != nil {
		return err
	}
	sdk.mu.Lock()
	sdk.executedModules = append(sdk.executedModules, modID.EngineResultID())
	if fnCall.Name == "" {
		sdk.constructorCalls++
	}
	sdk.mu.Unlock()
	switch fnCall.Name {
	case "", "copy":
		return fnCall.ReturnValue(ctx, JSON(`{"message":"from runtime"}`))
	case "check":
		for _, arg := range fnCall.InputArgs {
			if arg.Name == "label" {
				return fnCall.ReturnValue(ctx, arg.Value)
			}
		}
		return fnCall.ReturnValue(ctx, JSON(`"ok"`))
	default:
		return fmt.Errorf("unexpected function %q", fnCall.Name)
	}
}

func (sdk *moduleOwnershipRuntime) executed() []uint64 {
	sdk.mu.Lock()
	defer sdk.mu.Unlock()
	return append([]uint64(nil), sdk.executedModules...)
}

func (sdk *moduleOwnershipRuntime) constructed() int {
	sdk.mu.Lock()
	defer sdk.mu.Unlock()
	return sdk.constructorCalls
}

// installModuleOwnershipTestClasses installs the Module classes with an
// implementation-scoped resolver that follows the production shape: a clone of
// the operational module under a content digest derived only from the
// implementation and variant, never from the operational module's identity.
func installModuleOwnershipTestClasses(dag *dagql.Server) {
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*Module]{Typed: &Module{}}))
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*ModuleSource]{Typed: &ModuleSource{}}))
	dagql.Fields[*Module]{
		dagql.NodeFunc("_implementationScoped", func(ctx context.Context, mod dagql.ObjectResult[*Module], _ struct{}) (dagql.ObjectResult[*Module], error) {
			res, err := dagql.NewObjectResultForCurrentCall(ctx, dag, mod.Self().Clone())
			if err != nil {
				return res, err
			}
			return res.WithContentDigest(ctx, digest.FromString("Module._implementationScoped:test-implementation:"+mod.Self().AsModuleVariantDigest))
		}),
	}.Install(dag)
}

// moduleOwnershipTypeDef declares an object with a string field, a function
// taking an argument, a function returning the object type, and optionally an
// explicit constructor that goes through the runtime.
func moduleOwnershipTypeDef(t *testing.T, dag *dagql.Server, objectName string, explicitConstructor bool) dagql.ObjectResult[*TypeDef] {
	t.Helper()
	prefix := "ownership-" + objectName
	stringType := newTypeDefDetachedResult(t, dag, prefix+"-string", &TypeDef{Kind: TypeDefKindString})
	objectRef := newTypeDefDetachedResult(t, dag, prefix+"-ref", NewObjectTypeDef(objectName, "", nil))
	objectType := newTypeDefDetachedResult(t, dag, prefix+"-ref-type", (&TypeDef{}).WithObjectTypeDef(objectRef))
	obj := NewObjectTypeDef(objectName, "", nil)
	obj.Fields = dagql.ObjectResultArray[*FieldTypeDef]{
		newTypeDefDetachedResult(t, dag, prefix+"-message", NewFieldTypeDef("message", stringType, "", nil)),
	}
	check := NewFunction("check", stringType)
	check.Args = dagql.ObjectResultArray[*FunctionArg]{
		newTypeDefDetachedResult(t, dag, prefix+"-label", NewFunctionArg("label", stringType, "", nil, "", "", nil, nil)),
	}
	obj.Functions = dagql.ObjectResultArray[*Function]{
		newTypeDefDetachedResult(t, dag, prefix+"-check", check),
		newTypeDefDetachedResult(t, dag, prefix+"-copy", NewFunction("copy", objectType)),
	}
	if explicitConstructor {
		obj.Constructor = dagql.NonNull(newTypeDefDetachedResult(t, dag, prefix+"-constructor", NewFunction("", objectType)))
	}
	objRes := newTypeDefDetachedResult(t, dag, prefix+"-def", obj)
	return newTypeDefDetachedResult(t, dag, prefix+"-type", (&TypeDef{}).WithObjectTypeDef(objRes))
}

type moduleOwnershipTest struct {
	t           *testing.T
	cache       *dagql.Cache
	root        *Query
	dag         *dagql.Server
	runtime     *moduleOwnershipRuntime
	moduleCount int
}

func newModuleOwnershipTest(t *testing.T) *moduleOwnershipTest {
	t.Helper()
	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	root := &Query{}
	testSrv := &moduleObjectTestServer{
		mockServer: &mockServer{moduleSource: &ModuleSource{Kind: ModuleSourceKindDir}},
		cache:      cache,
		root:       root,
	}
	root.Server = testSrv
	dag := newCoreDagqlServerForTest(t, root)
	testSrv.dag = dag
	installModuleOwnershipTestClasses(dag)
	installTypeDefTestClasses(dag)
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*LLM]{Typed: &LLM{}}))
	return &moduleOwnershipTest{t: t, cache: cache, root: root, dag: dag, runtime: &moduleOwnershipRuntime{}}
}

// publishLLM publishes an LLM value carrying the given bindings as a cache
// row in the session of ctx, exactly as a resolver returning it would, so the
// row's dependency attachment runs.
func (f *moduleOwnershipTest) publishLLM(ctx context.Context, mcp *MCP) dagql.ObjectResult[*LLM] {
	f.t.Helper()
	md, err := engine.ClientMetadataFromContext(ctx)
	require.NoError(f.t, err)
	llm := &LLM{mcp: mcp}
	llmCall := moduleObjectTestSyntheticCall("ownership-llm-"+md.SessionID, llm)
	detached, err := dagql.NewObjectResultForCall(llm, f.dag, llmCall)
	require.NoError(f.t, err)
	publishedAny, err := f.cache.GetOrInitCall(ctx, md.SessionID, f.dag, &dagql.CallRequest{ResultCall: llmCall}, dagql.ValueFunc(detached))
	require.NoError(f.t, err)
	published, ok := publishedAny.(dagql.ObjectResult[*LLM])
	require.True(f.t, ok)
	return published
}

func (f *moduleOwnershipTest) session(id string) context.Context {
	ctx := engine.ContextWithClientMetadata(f.t.Context(), &engine.ClientMetadata{ClientID: id, SessionID: id})
	ctx = ContextWithQuery(dagql.ContextWithCache(ctx, f.cache), f.root)
	f.t.Cleanup(func() { _ = f.cache.ReleaseSession(context.Background(), id) })
	return ctx
}

func (f *moduleOwnershipTest) release(ctx context.Context) {
	f.t.Helper()
	md, err := engine.ClientMetadataFromContext(ctx)
	require.NoError(f.t, err)
	require.NoError(f.t, f.cache.ReleaseSession(ctx, md.SessionID))
}

type moduleOwnershipModuleOpts struct {
	variant             string
	explicitConstructor bool
	deps                []Mod
	includeSelf         bool
}

// module builds and attaches an operational module in the given session. Its
// main object is named after the module so the default or explicit constructor
// lands on Query.
func (f *moduleOwnershipTest) module(ctx context.Context, name string, opts moduleOwnershipModuleOpts) dagql.ObjectResult[*Module] {
	f.t.Helper()
	src := &ModuleSource{
		Kind:               ModuleSourceKindDir,
		ModuleName:         name,
		ModuleOriginalName: name,
		SDKImpl:            f.runtime,
		UserDefaults:       NewEnvFile(true),
	}
	srcRes := newTypeDefDetachedResult(f.t, f.dag, "ownership-source-"+name, src)
	mod := &Module{
		NameField:             name,
		OriginalName:          name,
		Source:                dagql.NonNull(srcRes),
		Deps:                  NewSchemaBuilder(f.root, opts.deps),
		ObjectDefs:            dagql.ObjectResultArray[*TypeDef]{moduleOwnershipTypeDef(f.t, f.dag, name, opts.explicitConstructor)},
		AsModuleVariantDigest: opts.variant,
		IncludeSelfInDeps:     opts.includeSelf,
	}
	md, err := engine.ClientMetadataFromContext(ctx)
	require.NoError(f.t, err)
	// Each call produces a distinct operational module row, even for the same
	// name in the same session: the operational identity is per producer.
	f.moduleCount++
	modCall := moduleObjectTestSyntheticCall(fmt.Sprintf("ownership-module-%s-%s-%d", name, md.SessionID, f.moduleCount), mod)
	detached, err := dagql.NewObjectResultForCall(mod, f.dag, modCall)
	require.NoError(f.t, err)
	attachedAny, err := f.cache.GetOrInitCall(ctx, md.SessionID, f.dag, &dagql.CallRequest{ResultCall: modCall}, dagql.ValueFunc(detached))
	require.NoError(f.t, err)
	attached, ok := attachedAny.(dagql.ObjectResult[*Module])
	require.True(f.t, ok)
	return attached
}

func (f *moduleOwnershipTest) install(ctx context.Context, dag *dagql.Server, mod dagql.ObjectResult[*Module]) *ObjectTypeDef {
	f.t.Helper()
	objDef := mod.Self().ObjectDefs[0].Self().AsObject.Value.Self()
	require.NoError(f.t, (&ModuleObject{Module: mod, TypeDef: objDef}).Install(ctx, dag))
	return objDef
}

// assertReleased prunes persisted retention roots (constructors and functions
// are persistable) and then requires that nothing else retains any row: the
// ownership edges added by holders must not form cycles.
func (f *moduleOwnershipTest) assertReleased(msg string) {
	f.t.Helper()
	_, err := f.cache.Prune(f.t.Context(), []dagql.CachePrunePolicy{{All: true}})
	require.NoError(f.t, err)
	require.Equal(f.t, 0, f.cache.Size(), msg)
}

func resultID(t *testing.T, res dagql.AnyResult) uint64 {
	t.Helper()
	id, err := res.ID()
	require.NoError(t, err)
	require.NotNil(t, id)
	return id.EngineResultID()
}

func loadable(ctx context.Context, dag *dagql.Server, res dagql.AnyResult) error {
	id, err := res.ID()
	if err != nil {
		return err
	}
	_, err = dag.Load(ctx, id)
	return err
}

// A class installed in one session keeps dispatching after that session ends,
// as long as some cache row holds the operational module. A cached module
// object is such a holder: it owns its module, the module is scoped again in
// the calling session, and the runtime still executes the exact operational
// module the class captured.
func TestModuleObjectMethodsDispatchAfterInstallingSessionRelease(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "default constructor", true: "explicit constructor"}[explicit], func(t *testing.T) {
			f := newModuleOwnershipTest(t)
			producer, consumer := f.session("producer"), f.session("consumer")
			mod := f.module(producer, "example", moduleOwnershipModuleOpts{explicitConstructor: explicit})
			modID := resultID(t, mod)
			f.install(producer, f.dag, mod)

			var first dagql.ObjectResult[*ModuleObject]
			require.NoError(t, f.dag.Select(producer, f.dag.Root(), &first, dagql.Selector{Field: "example"}))
			require.Equal(t, modID, resultID(t, first.Self().Module), "the payload captures the operational module")
			firstFrame, err := first.ResultCall()
			require.NoError(t, err)
			require.Equal(t, "example", firstFrame.Module.Name)
			scopedID := firstFrame.Module.ResultRef.ResultID
			require.NotZero(t, scopedID)
			require.NotEqual(t, modID, scopedID, "calls are keyed by the scoped module, not the operational one")

			// The consumer retains the object before the producer leaves.
			firstID, err := first.ID()
			require.NoError(t, err)
			heldAny, err := f.dag.Load(consumer, firstID)
			require.NoError(t, err)
			held, ok := heldAny.(dagql.ObjectResult[*ModuleObject])
			require.True(t, ok)
			f.release(producer)
			require.NoError(t, loadable(consumer, f.dag, mod), "the object must keep its module alive")

			var out string
			require.NoError(t, f.dag.Select(consumer, held, &out, dagql.Selector{
				Field: "check",
				Args:  []dagql.NamedInput{{Name: "label", Value: dagql.String("from consumer")}},
			}))
			require.Equal(t, "from consumer", out)
			executed := f.runtime.executed()
			require.Equal(t, modID, executed[len(executed)-1], "the runtime executes the captured operational module")

			var copied dagql.ObjectResult[*ModuleObject]
			require.NoError(t, f.dag.Select(consumer, held, &copied, dagql.Selector{Field: "copy"}))
			require.Equal(t, modID, resultID(t, copied.Self().Module), "results keep the operational module")
			copiedFrame, err := copied.ResultCall()
			require.NoError(t, err)
			require.NotZero(t, copiedFrame.Module.ResultRef.ResultID)
			require.Equal(t, firstFrame.ContentDigest() != "", copiedFrame.ContentDigest() != "")
			require.NoError(t, f.dag.Select(consumer, copied, &out, dagql.Selector{Field: "message"}))
			require.Equal(t, "from runtime", out)

			// Equivalent scoping across sessions: a fresh session re-scopes the
			// module and lands on the same implementation identity.
			later := f.session("later")
			copiedID, err := copied.ID()
			require.NoError(t, err)
			_, err = f.dag.Load(later, copiedID)
			require.NoError(t, err)
			var again dagql.ObjectResult[*ModuleObject]
			require.NoError(t, f.dag.Select(later, held, &again, dagql.Selector{Field: "copy"}))
			require.Equal(t, resultID(t, copied), resultID(t, again), "warm dispatch reuses the cached result")

			f.release(consumer)
			f.release(later)
			f.assertReleased("object, scoped rows, and module must all be released together")
		})
	}
}

// Retaining a module object retains the module it embeds, including for empty
// objects, and the module never retains the object in return.
func TestModuleObjectRetainsEmbeddedModule(t *testing.T) {
	for _, withFields := range []bool{false, true} {
		t.Run(fmt.Sprintf("fields=%v", withFields), func(t *testing.T) {
			f := newModuleOwnershipTest(t)
			producer, reader := f.session("producer"), f.session("reader")
			mod := f.module(producer, "owner", moduleOwnershipModuleOpts{})
			modID := resultID(t, mod)
			objDef := f.install(producer, f.dag, mod)

			obj := &ModuleObject{Module: mod, TypeDef: objDef}
			if withFields {
				obj.Fields = map[string]any{"message": "value"}
			}
			// Deliberately omit module provenance from the call: the payload's
			// module must be owned even when the frame references nothing.
			objCall := moduleObjectTestSyntheticCall("ownership-object", obj)
			objRes, err := dagql.NewObjectResultForCall(obj, f.dag, objCall)
			require.NoError(t, err)
			objAny, err := f.cache.GetOrInitCall(producer, "producer", f.dag, &dagql.CallRequest{ResultCall: objCall}, dagql.ValueFunc(objRes))
			require.NoError(t, err)
			objID, err := objAny.ID()
			require.NoError(t, err)
			_, err = f.dag.Load(reader, objID)
			require.NoError(t, err)

			f.release(producer)
			require.NoError(t, loadable(reader, f.dag, mod), "retaining the object must retain its embedded module")
			f.release(reader)
			f.assertReleased("the module must not retain the object in return")
			_ = modID
		})
	}
}

// A module owns its dependency modules, so the schema memoized on the module
// stays dispatchable after the session that first built it ends: the
// dependency's class scopes the dependency module in the calling session.
func TestModuleDependencySchemaDispatchesAfterInstallingSessionRelease(t *testing.T) {
	f := newModuleOwnershipTest(t)
	producer, consumer := f.session("producer"), f.session("consumer")
	dep := f.module(producer, "dep", moduleOwnershipModuleOpts{})
	depID := resultID(t, dep)
	owner := f.module(producer, "owner", moduleOwnershipModuleOpts{deps: []Mod{NewUserMod(dep)}})

	depSchema, err := owner.Self().Deps.Schema(producer)
	require.NoError(t, err)
	_, ok := depSchema.Root().ObjectType().FieldSpec("dep", "")
	require.True(t, ok, "the dependency's constructor is installed in the owner's schema")

	require.NoError(t, loadable(consumer, f.dag, owner))
	f.release(producer)
	require.NoError(t, loadable(consumer, f.dag, dep), "the owner keeps its dependency alive")

	memoized, err := owner.Self().Deps.Schema(consumer)
	require.NoError(t, err)
	require.Same(t, depSchema, memoized, "the schema is memoized on the module")

	var obj dagql.ObjectResult[*ModuleObject]
	require.NoError(t, depSchema.Select(consumer, depSchema.Root(), &obj, dagql.Selector{Field: "dep"}))
	require.Equal(t, depID, resultID(t, obj.Self().Module))
	var out string
	require.NoError(t, depSchema.Select(consumer, obj, &out, dagql.Selector{
		Field: "check",
		Args:  []dagql.NamedInput{{Name: "label", Value: dagql.String("through owner")}},
	}))
	require.Equal(t, "through owner", out)
	executed := f.runtime.executed()
	require.Equal(t, depID, executed[len(executed)-1])

	f.release(consumer)
	f.assertReleased("owner, dependency, schema objects, and scoped rows must all be released")
}

// A module that includes itself in its dependency schema installs its own
// class there, capturing itself. That self entry stays a non-owning reference,
// so the module, its schema, and objects created through it are collectible.
func TestModuleSelfDependencySchemaIsCollectible(t *testing.T) {
	f := newModuleOwnershipTest(t)
	producer := f.session("producer")
	f.runtime.selfCalls = true
	mod := f.module(producer, "selfish", moduleOwnershipModuleOpts{includeSelf: true})
	modID := resultID(t, mod)
	var selfEntry bool
	for _, dep := range mod.Self().Deps.Mods() {
		if resultID(t, dep.ModuleResult()) == modID {
			selfEntry = true
		}
	}
	require.True(t, selfEntry, "attachment appends the module to its own dependencies")

	selfSchema, err := mod.Self().Deps.Schema(producer)
	require.NoError(t, err)
	var obj dagql.ObjectResult[*ModuleObject]
	require.NoError(t, selfSchema.Select(producer, selfSchema.Root(), &obj, dagql.Selector{Field: "selfish"}))
	require.Equal(t, modID, resultID(t, obj.Self().Module))
	var out string
	require.NoError(t, selfSchema.Select(producer, obj, &out, dagql.Selector{
		Field: "check",
		Args:  []dagql.NamedInput{{Name: "label", Value: dagql.String("self")}},
	}))
	require.Equal(t, "self", out)

	f.release(producer)
	f.assertReleased("the self entry must not create an ownership cycle")
}

// Bound tools dispatch through the class they were composed with, so the LLM
// row holding the binding owns that class's module for eager and lazy bindings
// alike. A lazy binding never constructs its receiver until a tool runs, the
// module is never reloaded, and an eager binding keeps the composition-time
// class even when the receiver was loaded through another same-named class.
func TestBoundToolsOwnCompositionClassModule(t *testing.T) {
	for _, lazy := range []bool{false, true} {
		t.Run(map[bool]string{false: "eager", true: "lazy"}[lazy], func(t *testing.T) {
			f := newModuleOwnershipTest(t)
			producer, publisher, consumer := f.session("producer"), f.session("publisher"), f.session("consumer")
			mod := f.module(producer, "example", moduleOwnershipModuleOpts{explicitConstructor: true})
			modID := resultID(t, mod)
			f.install(producer, f.dag, mod)
			objType, ok := f.dag.ObjectType("Example")
			require.True(t, ok)
			classModule, ok := ModuleObjectTypeModule(objType)
			require.True(t, ok)
			require.Equal(t, modID, resultID(t, classModule))

			runnerID := call.New().Append(objType.Typed().Type(), "example")
			var mcp *MCP
			if lazy {
				mcp = newMCP().WithLazyTools(runnerID, objType, f.dag.Schema(), nil)
			} else {
				runner, err := f.dag.Load(publisher, runnerID)
				require.NoError(t, err)
				mcp = newMCP().WithTools(runner, f.dag.Schema(), nil)
				require.Equal(t, 1, f.runtime.constructed())
			}

			// The LLM row is published by one session and retained by another;
			// the module must survive both the producer and the publisher.
			published := f.publishLLM(publisher, mcp)
			publishedID, err := published.ID()
			require.NoError(t, err)
			heldAny, err := f.dag.Load(consumer, publishedID)
			require.NoError(t, err)
			held, ok := heldAny.(dagql.ObjectResult[*LLM])
			require.True(t, ok)
			if lazy {
				require.Zero(t, f.runtime.constructed(), "publishing and loading the binding must not construct the receiver")
			}
			f.release(producer)
			f.release(publisher)
			require.NoError(t, loadable(consumer, f.dag, mod), "the LLM row keeps the class's module alive")

			toolsets, err := held.Self().mcp.boundToolsets(f.dag)
			require.NoError(t, err)
			require.Len(t, toolsets, 1)
			var check LLMTool
			for _, tool := range toolsets[0].tools {
				if tool.Name == "check" {
					check = tool
				}
			}
			require.NotEmpty(t, check.Name)
			if lazy {
				require.Zero(t, f.runtime.constructed(), "listing tools must not construct the receiver")
			}
			out, err := check.Call(consumer, map[string]any{"label": "still active"})
			require.NoError(t, err)
			require.Equal(t, "still active", out)
			require.Equal(t, 1, f.runtime.constructed(), "the receiver is constructed exactly once")
			for _, executed := range f.runtime.executed() {
				require.Equal(t, modID, executed, "every call executes the composition-time module")
			}
			out, err = check.Call(consumer, map[string]any{"label": "still active"})
			require.NoError(t, err)
			require.Equal(t, "still active", out)
			require.Equal(t, 1, f.runtime.constructed())

			f.release(consumer)
			f.assertReleased("the module is released with the LLM row's final owner")
		})
	}

	t.Run("receiver loaded through another class", func(t *testing.T) {
		f := newModuleOwnershipTest(t)
		producer, publisher := f.session("producer"), f.session("publisher")
		composed := f.module(producer, "example", moduleOwnershipModuleOpts{variant: "composed"})
		composedID := resultID(t, composed)
		f.install(producer, f.dag, composed)
		composedType, ok := f.dag.ObjectType("Example")
		require.True(t, ok)

		// A second schema defines the same type from a distinct operational
		// module of the same implementation family.
		other := f.module(producer, "example", moduleOwnershipModuleOpts{variant: "other"})
		otherID := resultID(t, other)
		require.NotEqual(t, composedID, otherID, "the receiver's module must be a distinct producer")
		otherDag := newCoreDagqlServerForTest(t, f.root)
		installModuleOwnershipTestClasses(otherDag)
		f.install(producer, otherDag, other)
		var runner dagql.ObjectResult[*ModuleObject]
		require.NoError(t, otherDag.Select(producer, otherDag.Root(), &runner, dagql.Selector{Field: "example"}))
		require.Equal(t, otherID, resultID(t, runner.Self().Module))

		// The binding was composed with the first class and then rebound with
		// a receiver wrapped by the other class, as a state-returning tool
		// loaded through another schema would be.
		bound, err := composedType.New(runner)
		require.NoError(t, err)
		mcp := newMCP().WithTools(bound, f.dag.Schema(), nil)
		require.NoError(t, mcp.rebindBoundTool("Example", runner))
		published := f.publishLLM(publisher, mcp)
		ownedModule, ok := ModuleObjectTypeModule(published.Self().mcp.boundTools[0].objType)
		require.True(t, ok)
		require.Equal(t, composedID, resultID(t, ownedModule), "the binding keeps the composition-time class")

		f.release(producer)
		require.NoError(t, loadable(publisher, f.dag, composed), "the LLM row owns the composition-time class's module")
		require.NoError(t, loadable(publisher, f.dag, other), "the rewrapped receiver still owns its own module")
		f.release(publisher)
		f.assertReleased("both modules are released with the LLM row")
	})
}

// A decoded module object declares the module its decoding class supplied, so
// the cache can make the decoded row own it.
func TestModuleObjectDecodeDeclaresDecodingModule(t *testing.T) {
	f := newModuleOwnershipTest(t)
	producer := f.session("producer")
	mod := f.module(producer, "example", moduleOwnershipModuleOpts{})
	objDef := f.install(producer, f.dag, mod)
	objType, ok := f.dag.ObjectType("Example")
	require.True(t, ok)
	template, ok := objType.Typed().(*ModuleObject)
	require.True(t, ok)
	require.Equal(t, resultID(t, mod), resultID(t, template.Module))

	decoded, err := template.DecodePersistedObject(producer, f.dag, 0, nil, []byte(`{}`))
	require.NoError(t, err)
	withDeps, ok := decoded.(dagql.HasDecodedDependencyResults)
	require.True(t, ok)
	deps := withDeps.DecodedDependencyResults()
	require.Len(t, deps, 1)
	require.Equal(t, resultID(t, mod), resultID(t, deps[0]))

	require.Nil(t, (&ModuleObject{TypeDef: objDef}).DecodedDependencyResults())
	require.Nil(t, (*ModuleObject)(nil).DecodedDependencyResults())
}
