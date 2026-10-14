package dagql_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/introspection"
	"github.com/dagger/dagger/engine"
)

// renamedPolicy's values are renamed in view v1: PerSession is PER_SESSION
// there, and Never is NEVER. The old names are hidden aliases in v1, but
// PER_SESSION also claims Never, which decodes to another value.
type renamedPolicy string

var renamedPolicies = dagql.NewEnum[renamedPolicy]()

var (
	renamedPolicyPerSession = renamedPolicies.RegisterView("PerSession", dagql.ExactView("v0"))
	renamedPolicyNever      = renamedPolicies.RegisterView("Never", dagql.ExactView("v0"))
	_                       = renamedPolicies.WithAliases(
		renamedPolicies.RenameView("PER_SESSION", renamedPolicyPerSession, dagql.ExactView("v1")),
		dagql.Aliases{Names: []string{"PerSession", "Never"}, View: dagql.ExactView("v1")})
	_ = renamedPolicies.WithAliases(
		renamedPolicies.RenameView("NEVER", renamedPolicyNever, dagql.ExactView("v1")),
		dagql.Aliases{Names: []string{"Never"}, View: dagql.ExactView("v1")})
)

func (renamedPolicy) Decoder() dagql.InputDecoder {
	return renamedPolicies
}

func (p renamedPolicy) ToLiteral() call.Literal {
	return renamedPolicies.Literal(p)
}

func (renamedPolicy) Type() *ast.Type {
	return &ast.Type{NamedType: "RenamedPolicy", NonNull: true}
}

type renamedCache struct {
	Policy string `field:"true"`
}

func (*renamedCache) Type() *ast.Type {
	return &ast.Type{NamedType: "RenamedCache", NonNull: true}
}

// newEnumRenameTestServer serves Query.cache(policy: RenamedPolicy!):
// RenamedCache, at view, counting the calls.
func newEnumRenameTestServer(t *testing.T, view call.View) (context.Context, *dagql.Server, *atomic.Int32) {
	t.Helper()
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		ClientID:              "dagql-enum-rename-client",
		SessionID:             "dagql-enum-rename-session",
		UseRecipeIDsByDefault: true,
	})
	ctx = dagql.ContextWithCache(ctx, newCache(t))

	srv := newExternalDagqlServerForTest(t, Query{})
	srv.View = view
	introspection.Install[Query](srv)
	renamedPolicies.Install(srv)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*renamedCache]{}))
	var calls atomic.Int32
	dagql.Fields[Query]{
		dagql.Func("cache", func(ctx context.Context, self Query, args struct {
			Policy renamedPolicy
		}) (*renamedCache, error) {
			calls.Add(1)
			return &renamedCache{Policy: string(args.Policy)}, nil
		}),
	}.Install(srv)
	dagql.Fields[*renamedCache]{}.Install(srv)
	return ctx, srv, &calls
}

func enumValueNames(def *ast.Definition) map[string]ast.DirectiveList {
	values := map[string]ast.DirectiveList{}
	for _, value := range def.EnumValues {
		values[value.Name] = value.Directives
	}
	return values
}

// A renamed value has its new name in the views it's renamed in, with no
// @enumValue directive, and its old name everywhere else.
func TestEnumValueRenameSchema(t *testing.T) {
	_, srv, _ := newEnumRenameTestServer(t, "v0")
	require.Equal(t, map[string]ast.DirectiveList{"PerSession": nil, "Never": nil},
		enumValueNames(srv.SchemaForView("v0").Types["RenamedPolicy"]))
	require.Equal(t, map[string]ast.DirectiveList{"PER_SESSION": nil, "NEVER": nil},
		enumValueNames(srv.SchemaForView("v1").Types["RenamedPolicy"]))

	// The old names are aliases in v1, but Never only of NEVER: as an alias
	// of PER_SESSION, it would decode to another value.
	aliased := srv.SchemaWithAliasesForView("v1").Types["RenamedPolicy"]
	require.ElementsMatch(t, []string{"PER_SESSION", "PerSession", "NEVER", "Never"}, mapsKeys(enumValueNames(aliased)))
	require.Equal(t, "Use `PER_SESSION` instead.", deprecationReason(aliased.EnumValues.ForName("PerSession").Directives))
	require.Equal(t, "Use `NEVER` instead.", deprecationReason(aliased.EnumValues.ForName("Never").Directives))
	require.Equal(t, srv.SchemaForView("v0").Types["RenamedPolicy"], srv.SchemaWithAliasesForView("v0").Types["RenamedPolicy"])

	// Both decode to the value the old name is.
	for _, name := range []string{"PER_SESSION", "PerSession"} {
		decoded, err := renamedPolicies.DecodeInput(name)
		require.NoError(t, err)
		require.Equal(t, renamedPolicyPerSession, decoded)
	}
}

// The old view only has the old names. In the new view, the old names make
// the same calls as the new ones.
func TestEnumValueRenameQuery(t *testing.T) {
	ctx, srv, _ := newEnumRenameTestServer(t, "v0")
	data, err := srv.Query(ctx, `{ cache(policy: PerSession) { policy } }`, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"cache": map[string]any{"policy": "PerSession"}}, roundTrip(t, data))
	_, err = srv.Query(ctx, `{ cache(policy: PER_SESSION) { policy } }`, nil)
	require.ErrorContains(t, err, `Value "PER_SESSION" does not exist`)

	ctx, srv, calls := newEnumRenameTestServer(t, "v1")
	id := func(query string, vars map[string]any) string {
		t.Helper()
		data, err := srv.Query(ctx, query, vars)
		require.NoError(t, err)
		res := roundTrip(t, data)["cache"].(map[string]any)
		require.Equal(t, "PerSession", res["policy"])
		return res["id"].(string)
	}
	canonical := id(`{ cache(policy: PER_SESSION) { id policy } }`, nil)
	require.Equal(t, canonical, id(`{ cache(policy: PerSession) { id policy } }`, nil))
	require.Equal(t, canonical, id(`query($p: RenamedPolicy!) { cache(policy: $p) { id policy } }`, map[string]any{"p": "PerSession"}))
	require.Equal(t, int32(1), calls.Load())

	_, err = srv.Query(ctx, `{ cache(policy: Never) { policy } }`, nil)
	require.NoError(t, err)
}

func mapsKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
