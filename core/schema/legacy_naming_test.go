package schema

import (
	"context"
	"net/http"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
)

// newLegacyNamingTestServers returns servers at the v1.0.0 view and at the
// same view marked for the legacy naming rules, sharing one cache, as the
// session serves a module that keeps those rules next to a v1 client.
func newLegacyNamingTestServers(t *testing.T) (context.Context, *dagql.Server, *dagql.Server) {
	t.Helper()
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "legacy-naming", SessionID: "legacy-naming"})
	srv := &currentTypeDefsTestServer{platform: core.Platform{OS: "linux", Architecture: "amd64"}}
	base, err := NewCoreSchemaBase(ctx, srv)
	require.NoError(t, err)
	v1, err := base.Fork(ctx, core.NewRoot(srv), "v1.0.0")
	require.NoError(t, err)
	legacy, err := base.Fork(ctx, core.NewRoot(srv), core.LegacyNamingView("v1.0.0"))
	require.NoError(t, err)
	srv.dag = v1
	return ctx, v1, legacy
}

func graphQLClient(ctx context.Context, srv *dagql.Server) *client.Client {
	h := dagql.NewDefaultHandler(srv)
	return client.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(ctx))
	}))
}

// A module that keeps the legacy naming rules is served at its view marked
// by core.LegacyNamingView: it's the same API, but the typedefs it declares
// are named by the legacy rules, and the mark keys the cache, so the same
// calls from a v1 client keep the new names.
func TestLegacyNamingViewTypeDefs(t *testing.T) {
	ctx, v1, legacy := newLegacyNamingTestServers(t)

	const query = `{
		typeDef {
			withObject(name: "httpUrlFetcher") {
				asObject { name }
			}
		}
	}`
	name := func(c *client.Client) string {
		var res struct {
			TypeDef struct {
				WithObject struct {
					AsObject struct {
						Name string
					}
				}
			}
		}
		require.NoError(t, c.Post(query, &res))
		return res.TypeDef.WithObject.AsObject.Name
	}

	require.Equal(t, "HttpUrlFetcher", name(graphQLClient(ctx, legacy)))
	require.Equal(t, "HTTPURLFetcher", name(graphQLClient(ctx, v1)))
	// and again, after both results are cached
	require.Equal(t, "HttpUrlFetcher", name(graphQLClient(ctx, legacy)))

	// Loading the marked call from its ID names it the same way, wherever
	// it's loaded.
	var fn dagql.ObjectResult[*core.Function]
	require.NoError(t, legacy.Select(ctx, legacy.Root(), &fn, dagql.Selector{
		Field: "function",
		View:  legacy.View,
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String("getUrl")},
			{Name: "returnType", Value: dagql.NewID[*core.TypeDef](stringTypeDefID(ctx, t, legacy))},
		},
	}))
	require.Equal(t, "getUrl", fn.Self().Name)
	id, err := fn.RecipeID(ctx)
	require.NoError(t, err)
	require.Equal(t, core.LegacyNamingView("v1.0.0"), id.View())
	// on another server, with a cold cache
	otherCtx, other := newNestingTestServer(t, "v1.0.0")
	loaded, err := other.Load(otherCtx, id)
	require.NoError(t, err)
	loadedFn, ok := dagql.UnwrapAs[*core.Function](loaded)
	require.True(t, ok)
	require.Equal(t, "getUrl", loadedFn.Name)

	var latest dagql.ObjectResult[*core.Function]
	require.NoError(t, v1.Select(ctx, v1.Root(), &latest, dagql.Selector{
		Field: "function",
		View:  v1.View,
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String("getUrl")},
			{Name: "returnType", Value: dagql.NewID[*core.TypeDef](stringTypeDefID(ctx, t, v1))},
		},
	}))
	require.Equal(t, "getURL", latest.Self().Name)
}

// The marked view hides Query.formatIdentifiers, the gate SDK codegen and
// module runtimes use, so a legacy-naming module's codegen keeps the legacy
// conversion, and reports the unmarked version as __schemaVersion, which SDK
// codegen parses.
func TestLegacyNamingViewSchema(t *testing.T) {
	ctx, v1, legacy := newLegacyNamingTestServers(t)

	for _, tc := range []struct {
		srv       *dagql.Server
		hasFormat bool
	}{
		{v1, true},
		{legacy, false},
	} {
		data, err := getSchemaJSON(nil, nil, tc.srv.View, tc.srv)
		require.NoError(t, err)
		resp := decodeSchemaResponse(t, data)
		require.Equal(t, "v1.0.0", resp.SchemaVersion, "view %q", tc.srv.View)
		require.Equal(t, tc.hasFormat, resp.Schema.HasFormatIdentifiers(), "view %q", tc.srv.View)

		var res struct {
			SchemaVersion string `json:"__schemaVersion"`
		}
		require.NoError(t, graphQLClient(ctx, tc.srv).Post(`{ __schemaVersion }`, &res))
		require.Equal(t, "v1.0.0", res.SchemaVersion, "view %q", tc.srv.View)
	}

	// The rest of the identifier API stays: it's the same API.
	var res struct {
		Identifier struct {
			Format string
		}
	}
	require.NoError(t, graphQLClient(ctx, legacy).Post(`{ identifier(name: "httpUrl") { format(casing: CAMEL) } }`, &res))
	require.Equal(t, "httpURL", res.Identifier.Format)
}

func stringTypeDefID(ctx context.Context, t *testing.T, dag *dagql.Server) *call.ID {
	t.Helper()
	var td dagql.ObjectResult[*core.TypeDef]
	require.NoError(t, dag.Select(ctx, dag.Root(), &td,
		dagql.Selector{Field: "typeDef"},
		dagql.Selector{Field: "withKind", Args: []dagql.NamedInput{{Name: "kind", Value: core.TypeDefKindString}}},
	))
	id, err := td.ID()
	require.NoError(t, err)
	return id
}
