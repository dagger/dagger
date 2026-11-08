package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
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
