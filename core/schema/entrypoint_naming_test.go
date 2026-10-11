package schema

import (
	"context"
	"net/http"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

// A module entrypoint's runtime client is served at the engine's API view,
// but the typedefs it declares over GraphQL are named by the legacy rules:
// the selector view hook marks its typedef constructor calls, and the mark
// keys the cache, so the same calls from a v1 module keep the new names.
func TestEntrypointTypeDefNaming(t *testing.T) {
	ctx, dag := newNestingTestServer(t, "v1.0.0")
	entrypoint := &core.ModuleSource{
		EngineVersion: "v1.0.0",
		Entrypoint:    &modules.ModuleEntrypointConfig{Kind: "dang", Source: "./sdk/entrypoint"},
	}
	v1 := &core.ModuleSource{EngineVersion: "v1.0.0"}

	gql := func(src *core.ModuleSource) *client.Client {
		// Stand in for the hook core installs, which finds the source of
		// the calling client's module.
		dag.SetSelectorView(func(_ context.Context, typeName string, sel dagql.Selector) call.View {
			return core.NamingSelectorView(src, typeName, sel.Field, sel.View)
		})
		h := dagql.NewDefaultHandler(dag)
		return client.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.ServeHTTP(w, r.WithContext(ctx))
		}))
	}
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

	require.Equal(t, "HttpUrlFetcher", name(gql(entrypoint)))
	require.Equal(t, "HTTPURLFetcher", name(gql(v1)))
	// and again, after both results are cached
	require.Equal(t, "HttpUrlFetcher", name(gql(entrypoint)))

	// Loading the marked call from its ID names it the same way, wherever
	// it's loaded.
	var fn dagql.ObjectResult[*core.Function]
	require.NoError(t, dag.Select(ctx, dag.Root(), &fn, dagql.Selector{
		Field: "function",
		View:  core.LegacyNamingView("v1.0.0"),
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String("getUrl")},
			{Name: "returnType", Value: dagql.NewID[*core.TypeDef](stringTypeDefID(ctx, t, dag))},
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
	require.NoError(t, dag.Select(ctx, dag.Root(), &latest, dagql.Selector{
		Field: "function",
		View:  "v1.0.0",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String("getUrl")},
			{Name: "returnType", Value: dagql.NewID[*core.TypeDef](stringTypeDefID(ctx, t, dag))},
		},
	}))
	require.Equal(t, "getURL", latest.Self().Name)
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
