package core

import (
	"testing"

	"github.com/dagger/dagger/dagql"
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
