package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// A filtered view holds concrete installed objects, even when two installations
// share the same source and receiver content. Their cache keys must differ.
func TestArtifactViewCacheSeparatesInstalledTypes(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	root := &Query{}
	server := &moduleObjectTestServer{mockServer: &mockServer{}, cache: cache, root: root}
	root.Server = server
	srv := newCoreDagqlServerForTest(t, root)
	server.dag = srv
	installModuleObjectTestModuleClass(srv)
	installTypeDefTestClasses(srv)
	ctx = engine.ContextWithClientMetadata(ContextWithQuery(dagql.ContextWithCache(ctx, cache), root), &engine.ClientMetadata{ClientID: "view-client", SessionID: "view-session"})
	source := newTypeDefAttachedResult(t, ctx, cache, srv, "view-source", &ModuleSource{Kind: ModuleSourceKindLocal, Local: &LocalModuleSource{ContextDirectoryPath: "/test"}})
	keys := make([]string, 0, 3)
	for _, name := range []string{"GoPackages", "RenamedPackages", "GoPackages"} {
		mod := newTypeDefAttachedResult(t, ctx, cache, srv, "view-module-"+name, &Module{NameField: name, OriginalName: "Go", Source: dagql.NonNull(source), Deps: NewSchemaBuilder(root, nil)})
		obj := &ModuleObject{Module: mod, TypeDef: NewObjectTypeDef(name, "", nil)}
		field, err := obj.artifactViewField(srv)
		require.NoError(t, err)
		// The content-addressed receiver and view input are otherwise identical.
		req := &dagql.CallRequest{ResultCall: moduleObjectTestSyntheticCall("same-view-content", dagql.String(""))}
		if field.Spec.GetDynamicInput != nil {
			require.NoError(t, field.Spec.GetDynamicInput(ctx, nil, nil, "", req))
		}
		key, err := req.ResultCall.ContentPreferredDigest(ctx)
		require.NoError(t, err)
		keys = append(keys, string(key))
	}
	require.NotEqual(t, keys[0], keys[1], "renamed installation must not load the original installed type")
	require.Equal(t, keys[0], keys[2], "the same installed type must retain a stable cache key")
}

// A host checkout's Workspace and a module's recipe differ in every session, so
// a filtered view's origin must not hold them, or no check under a filter caches
// across sessions. What tells two collections apart must still reach it.
func TestArtifactViewOriginHoldsNoPerSessionInputs(t *testing.T) {
	named := func(name string) *ast.Type { return ast.NonNullNamedType(name, nil) }
	workspace := func(nonce string) *call.ID {
		return (*call.ID)(nil).Append(named("Workspace"), "currentWorkspace",
			call.WithImplicitInputs(call.NewArgument("cachePerCall", call.NewLiteralString(nonce), false)))
	}
	module := func(name, nonce string) *call.Module {
		id := (*call.ID)(nil).Append(named("Module"), "module", call.WithArgs(call.NewArgument("nonce", call.NewLiteralString(nonce), false)))
		return call.NewModule(id, name, "ref", "")
	}
	collection := func(mod *call.Module, field string, ws *call.ID) *call.ID {
		root := (*call.ID)(nil).Append(named("Tool"), "tool", call.WithModule(mod))
		return root.Append(named("Tests"), field, call.WithModule(mod), call.WithArgs(call.NewArgument("ws", call.NewLiteralID(ws), false)))
	}
	view := func(id *call.ID) *call.ID {
		return id.Append(id.Type().ToAST(), artifactViewField, call.WithArgs(call.NewArgument("view", call.NewLiteralString("state"), false)))
	}

	origin := artifactViewOrigin(collection(module("tool", "one"), "tests", workspace("one")))
	require.NotEqual(t, origin, artifactViewOrigin(collection(module("other", "one"), "tests", workspace("one"))), "another module's collection")
	require.NotEqual(t, origin, artifactViewOrigin(collection(module("tool", "one"), "builds", workspace("one"))), "another field's collection")
	overlaid := workspace("one").Append(named("Workspace"), "withNewFile", call.WithArgs(call.NewArgument("path", call.NewLiteralString("checks.json"), false)))
	require.NotEqual(t, origin, artifactViewOrigin(collection(module("tool", "one"), "tests", overlaid)), "an explicit Workspace overlay")

	spine := (*call.ID)(nil).Append(named("Tool"), "tool")
	sameWorkspace := func(nonce string) string {
		return artifactViewOrigin(spine.Append(named("Tests"), "tests", call.WithArgs(call.NewArgument("ws", call.NewLiteralID(workspace(nonce)), false))))
	}
	require.Equal(t, sameWorkspace("one"), sameWorkspace("two"), "the ambient Workspace of another session")
	scoped := spine.Append(named("Tests"), "tests", call.WithArgs(call.NewArgument("ws", call.NewLiteralID(workspace("one")), false)))
	require.Equal(t, artifactViewOrigin(scoped), artifactViewOrigin(view(scoped)), "a view wrapper")
}

func TestArtifactViewLazyReferenceDefersProducerAndRetainsSelection(t *testing.T) {
	// Both engines install the same schema, but share no cached results.
	setup := func() (context.Context, *dagql.Server, *int, *ModuleObject) {
		ctx := t.Context()
		cache, err := dagql.NewCache(ctx, "", nil, nil)
		require.NoError(t, err)
		root := &Query{}
		server := &moduleObjectTestServer{mockServer: &mockServer{}, cache: cache, root: root}
		root.Server = server
		srv := newCoreDagqlServerForTest(t, root)
		server.dag = srv
		installModuleObjectTestModuleClass(srv)
		installTypeDefTestClasses(srv)
		ctx = engine.ContextWithClientMetadata(ContextWithQuery(dagql.ContextWithCache(ctx, cache), root), &engine.ClientMetadata{ClientID: "lazy-view-client", SessionID: "lazy-view-session"})
		obj, _ := collectionTestObject(t, TypeDefKindString, []any{"a", "b", "c"})
		obj.TypeDef = newTypeDefAttachedResult(t, ctx, cache, srv, "lazy-view-type", obj.TypeDef).Self()
		source := newTypeDefAttachedResult(t, ctx, cache, srv, "lazy-view-source", &ModuleSource{Kind: ModuleSourceKindLocal, Local: &LocalModuleSource{ContextDirectoryPath: "/test"}})
		obj.Module = newTypeDefAttachedResult(t, ctx, cache, srv, "lazy-view-module", &Module{NameField: "test", Source: dagql.NonNull(source), Deps: NewSchemaBuilder(nil, nil)})
		class := dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: obj})
		viewField, err := obj.artifactViewField(srv)
		require.NoError(t, err)
		viewField.Spec.Module, viewField.Spec.ModuleProvider = nil, nil
		class.Install(viewField)
		srv.InstallObject(class)
		calls := new(int)
		dagql.Fields[*Query]{{
			Spec: &dagql.FieldSpec{Name: "lazyCollection", Type: obj},
			Func: func(ctx context.Context, _ dagql.ObjectResult[*Query], _ map[string]dagql.Input, _ call.View) (dagql.AnyResult, error) {
				*calls++
				return dagql.NewObjectResultForCurrentCall(ctx, srv, &ModuleObject{Module: obj.Module, TypeDef: obj.TypeDef, Fields: map[string]any{"Keys": []any{"a", "b", "c"}}})
			},
		}}.Install(srv)
		return ctx, srv, calls, obj
	}
	ctx, srv, calls, obj := setup()
	view := &ArtifactView{state: artifactViewState{Rules: []artifactViewRule{{Dimension: "/cases", Type: obj.TypeDef.Name, Keys: []string{"b"}, UniqueType: true}}}}
	producer := call.New().Append(obj.Type(), "lazyCollection")
	deferred, err := view.ApplyToID(ctx, srv, producer)
	require.NoError(t, err)
	require.Equal(t, 0, *calls, "binding must not run the producer")
	require.Equal(t, artifactViewField, deferred.Field())
	again, err := view.ApplyToID(ctx, srv, deferred)
	require.NoError(t, err)
	require.Equal(t, deferred.Digest(), again.Digest(), "the second argument pass must not add another wrapper")
	encoded, err := deferred.Encode()
	require.NoError(t, err)
	var restored call.ID
	require.NoError(t, restored.Decode(encoded))
	freshCtx, freshServer, freshCalls, _ := setup()
	result, err := freshServer.Load(freshCtx, &restored)
	require.NoError(t, err)
	require.Equal(t, 1, *freshCalls, "explicit loading runs the producer once")
	selected, ok := dagql.UnwrapAs[*ModuleObject](result)
	require.True(t, ok)
	keys, err := selected.collectionKeys(freshCtx)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	require.Equal(t, "b", keys[0].text)
	delta, err := selected.collectionDelta(freshCtx)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "c"}, delta.RemovedKeys)
	require.Equal(t, 0, *calls, "the original engine must never evaluate the producer")
}
