package core

import (
	"runtime"
	"testing"
	"weak"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

// A module object's class rides along wherever its object results go: another
// module object's field values, a check's receiver, an interface value. Its
// resolvers must not keep the server the class was installed into alive.
func TestModuleObjectClassDoesNotRetainServer(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	root := &Query{}
	server := &moduleObjectTestServer{mockServer: &mockServer{}, cache: cache, root: root}
	root.Server = server
	// Long-lived, like the engine's core schema.
	core := newCoreDagqlServerForTest(t, root)
	server.dag = core
	installModuleObjectTestModuleClass(core)
	installTypeDefTestClasses(core)
	ctx = engine.ContextWithClientMetadata(ContextWithQuery(ctx, root), &engine.ClientMetadata{ClientID: "retention-client", SessionID: "retention-session"})
	ctx = dagql.ContextWithCache(ctx, cache)

	source := newTypeDefAttachedResult(t, ctx, cache, core, "source", &ModuleSource{
		Kind: ModuleSourceKindLocal, Local: &LocalModuleSource{ContextDirectoryPath: "/test"},
	})
	stringType := newTypeDefAttachedResult(t, ctx, cache, core, "string", &TypeDef{Kind: TypeDefKindString})
	typeDef := NewObjectTypeDef("Test", "", nil)
	typeDef.Functions = dagql.ObjectResultArray[*Function]{
		newTypeDefAttachedResult(t, ctx, cache, core, "hello", NewFunction("hello", stringType)),
	}
	objDef := newTypeDefAttachedResult(t, ctx, cache, core, "object-def", typeDef)
	mod := newTypeDefAttachedResult(t, ctx, cache, core, "module", &Module{
		NameField: "Test", OriginalName: "Test", Source: dagql.NonNull(source),
		Deps:       NewSchemaBuilder(root, nil),
		ObjectDefs: dagql.ObjectResultArray[*TypeDef]{newTypeDefAttachedResult(t, ctx, cache, core, "type-def", (&TypeDef{}).WithObjectTypeDef(objDef))},
	})

	inner, served := func() (dagql.ObjectResult[*ModuleObject], weak.Pointer[dagql.Server]) {
		// Short-lived, like a client's served schema.
		served := newCoreDagqlServerForTest(t, root)
		obj := &ModuleObject{Module: mod, TypeDef: typeDef, Fields: map[string]any{}}
		require.NoError(t, obj.Install(ctx, served, InstallOpts{SkipConstructor: true}))
		inner := newTypeDefAttachedResult(t, ctx, cache, served, "inner", obj)
		for _, field := range []string{"hello", rebindModuleObjectStateField} {
			_, ok := inner.ObjectType().FieldSpec(field, "")
			require.True(t, ok, field)
		}
		return inner, weak.Make(served)
	}()
	// Like a cached module object holding another module's object in a field.
	outer := &ModuleObject{Module: mod, TypeDef: typeDef, Fields: map[string]any{"inner": inner}}

	for range 3 {
		runtime.GC()
	}
	require.True(t, served.Value() == nil, "server the class was installed into is still reachable from its object result")
	runtime.KeepAlive(outer)
}

func TestInstalledServerFallsBackOnceCollected(t *testing.T) {
	root := &Query{}
	current := newCoreDagqlServerForTest(t, root)
	ctx := dagql.ContextWithServer(t.Context(), current)

	installed := newCoreDagqlServerForTest(t, root)
	ref := newInstalledServer(installed)
	require.Same(t, installed, ref.orCurrent(ctx))
	srv, err := ref.forObject(ctx, nil)
	require.NoError(t, err)
	require.Same(t, installed, srv)
	runtime.KeepAlive(installed)

	ref = func() installedServer {
		return newInstalledServer(newCoreDagqlServerForTest(t, root))
	}()
	for range 3 {
		runtime.GC()
	}
	require.Same(t, current, ref.orCurrent(ctx))
}
