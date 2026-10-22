package schema

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
)

// namingConstructors are the typedef constructors that normalize a name
// they're given (core.IsNamingField), with the type they're installed on.
var namingConstructors = []struct{ typeName, field string }{
	{"Query", "function"},
	{"Query", "__functionArg"},
	{"Query", "__fieldTypeDef"},
	{"Query", "__enumMemberTypeDef"},
	{"Query", "__objectTypeDef"},
	{"Query", "__interfaceTypeDef"},
	{"Query", "__scalarTypeDef"},
	{"Query", "__enumTypeDef"},
	{"Function", "withArg"},
	{"TypeDef", "withObject"},
	{"TypeDef", "withInterface"},
	{"TypeDef", "withEnum"},
	{"TypeDef", "withScalar"},
	{"TypeDef", "withField"},
	{"TypeDef", "withEnumMember"},
	{"TypeDef", "withCollectionKeys"},
	{"TypeDef", "withCollectionGet"},
	{"TypeDef", "withCollectionDelta"},
	{"ObjectTypeDef", "__withCollectionMember"},
}

// The typedef constructors name by the naming rules of their caller's view
// (core.NamerFromContext), so they must be view-scoped: a field without a
// view filter has no view in its call identity, and modules on either side
// of the naming cut would share one cached, wrongly named result.
func TestTypeDefNamingCacheIdentity(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "naming", SessionID: "naming"})
	srv := &currentTypeDefsTestServer{platform: core.Platform{OS: "linux", Architecture: "amd64"}}
	base, err := NewCoreSchemaBase(ctx, srv)
	require.NoError(t, err)
	root := core.NewRoot(srv)
	ctx = core.ContextWithQuery(ctx, root)

	fork := func(version string) (call.View, *dagql.Server) {
		t.Helper()
		view := call.View(engine.ModuleAPIViewVersion(version))
		dag, err := base.Fork(ctx, root, view)
		require.NoError(t, err)
		srv.dag = dag
		return view, dag
	}

	view, dag := fork("v1.0.0-beta.21")
	for _, c := range namingConstructors {
		objType, ok := dag.ObjectType(c.typeName)
		require.True(t, ok, c.typeName)
		spec, ok := objType.FieldSpec(c.field, view)
		require.True(t, ok, "%s.%s", c.typeName, c.field)
		require.NotNil(t, spec.ViewFilter, "%s.%s must be view-scoped", c.typeName, c.field)
	}

	names := func(version string) (object, field string) {
		t.Helper()
		view, dag := fork(version)
		dag.View = view
		strKind := dagql.Selector{Field: "withKind", View: view, Args: []dagql.NamedInput{
			{Name: "kind", Value: core.TypeDefKindString},
		}}
		var typeDef dagql.ObjectResult[*core.TypeDef]
		require.NoError(t, dag.Select(ctx, dag.Root(), &typeDef, dagql.Selector{Field: "typeDef", View: view}, strKind))
		typeDefID, err := typeDef.ID()
		require.NoError(t, err)
		var obj dagql.ObjectResult[*core.TypeDef]
		require.NoError(t, dag.Select(ctx, dag.Root(), &obj,
			dagql.Selector{Field: "typeDef", View: view},
			dagql.Selector{Field: "withObject", View: view, Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String("HTTPClient")},
			}},
			dagql.Selector{Field: "withField", View: view, Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String("baseURL")},
				{Name: "typeDef", Value: dagql.NewID[*core.TypeDef](typeDefID)},
			}},
		))
		def := obj.Self().AsObject.Value.Self()
		require.Len(t, def.Fields, 1)
		return def.Name, def.Fields[0].Self().Name
	}

	// Interleaved, so each side would get the other's cached result if the
	// view weren't in the calls' identity.
	for _, tc := range []struct{ version, object, field string }{
		{"v1.0.0-beta.21", "HTTPClient", "baseURL"},
		{"v1.0.0-beta.20", "Httpclient", "baseUrl"},
		{"v1.0.0-beta.21", "HTTPClient", "baseURL"},
		{"v1.0.0-0", "Httpclient", "baseUrl"},
		{"v1.0.0-beta.19", "Httpclient", "baseUrl"},
	} {
		object, field := names(tc.version)
		require.Equal(t, tc.object, object, tc.version)
		require.Equal(t, tc.field, field, tc.version)
	}
}
