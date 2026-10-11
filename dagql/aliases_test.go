package dagql_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/introspection"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

type aliasThing struct {
	Name string `field:"true"`
}

func (*aliasThing) Type() *ast.Type {
	return &ast.Type{NamedType: "AliasThing", NonNull: true}
}

func aliasesOf(names ...string) dagql.Aliases {
	return dagql.Aliases{Names: names}
}

// newAliasTestServer serves Query.thing(shortSHA: String!, legacyJSON:
// Boolean): AliasThing, with the hidden aliases Query.oldThing, shortSha,
// legacyJson, AliasThing.getUrl and the type AliasThingOld. It counts the
// calls to thing.
func newAliasTestServer(t *testing.T) (context.Context, *dagql.Server, *atomic.Int32) {
	t.Helper()
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		ClientID:              "dagql-alias-client",
		SessionID:             "dagql-alias-session",
		UseRecipeIDsByDefault: true,
	})
	ctx = dagql.ContextWithCache(ctx, newCache(t))

	srv := newExternalDagqlServerForTest(t, Query{})
	introspection.Install[Query](srv)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*aliasThing]{
		Aliases: aliasesOf("AliasThingOld"),
	}))

	var calls atomic.Int32
	dagql.Fields[Query]{
		dagql.Func("thing", func(ctx context.Context, self Query, args struct {
			ShortSHA   string `name:"shortSHA"`
			LegacyJSON bool   `name:"legacyJSON" default:"false"`
		}) (*aliasThing, error) {
			calls.Add(1)
			name := args.ShortSHA
			if args.LegacyJSON {
				name += "+json"
			}
			return &aliasThing{Name: name}, nil
		}).
			WithAliases(aliasesOf("oldThing")).
			Args(
				dagql.Arg("shortSHA").WithAliases(aliasesOf("shortSha")),
				dagql.Arg("legacyJSON").WithAliases(aliasesOf("legacyJson")),
			),
	}.Install(srv)
	dagql.Fields[*aliasThing]{
		dagql.Func("getURL", func(ctx context.Context, self *aliasThing, _ struct{}) (string, error) {
			return "https://example.com/" + self.Name, nil
		}).WithAliases(aliasesOf("getUrl")),
	}.Install(srv)
	return ctx, srv, &calls
}

// Queries may use aliases wherever the canonical names go: they validate,
// and the response keeps the names the query used.
func TestAliasesQuery(t *testing.T) {
	_, srv, _ := newAliasTestServer(t)
	gql := newTestClient(srv)

	var res struct {
		OldThing struct {
			Name   string
			GetUrl string //nolint:revive,staticcheck // the alias is the response key
		}
		Thing struct {
			GetURL string
		}
	}
	req(t, gql, `{
		oldThing(shortSha: "abc", legacyJson: true) { name getUrl }
		thing(shortSHA: "def") { getURL }
	}`, &res)
	require.Equal(t, "abc+json", res.OldThing.Name)
	require.Equal(t, "https://example.com/abc+json", res.OldThing.GetUrl)
	require.Equal(t, "https://example.com/def", res.Thing.GetURL)

	// GraphQL aliases keep working on top.
	var aliased struct {
		X struct{ Y string }
	}
	req(t, gql, `{ x: oldThing(shortSha: "abc") { y: getUrl } }`, &aliased)
	require.Equal(t, "https://example.com/abc", aliased.X.Y)

	// The same through Server.Query, which validates by itself.
	ctx, srv, _ := newAliasTestServer(t)
	data, err := srv.Query(ctx, `{ oldThing(shortSha: "abc") { getUrl } }`, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"oldThing": map[string]any{"getUrl": "https://example.com/abc"}}, roundTrip(t, data))
}

// A query using aliases makes the canonical query's calls: same IDs, same
// cache entries.
func TestAliasesCanonicalCalls(t *testing.T) {
	ctx, srv, calls := newAliasTestServer(t)

	id := func(query string) string {
		t.Helper()
		data, err := srv.Query(ctx, query, nil)
		require.NoError(t, err)
		var res struct {
			Thing    *struct{ ID string }
			OldThing *struct{ ID string }
		}
		require.NoError(t, json.Unmarshal(mustJSON(t, data), &res))
		if res.Thing != nil {
			return res.Thing.ID
		}
		require.NotNil(t, res.OldThing)
		return res.OldThing.ID
	}

	canonical := id(`{ thing(shortSHA: "abc", legacyJSON: true) { id } }`)
	require.Equal(t, int32(1), calls.Load())
	for _, query := range []string{
		`{ oldThing(shortSha: "abc", legacyJson: true) { id } }`,
		`{ thing(shortSha: "abc", legacyJSON: true) { id } }`,
		`{ oldThing(legacyJson: true, shortSHA: "abc") { id } }`,
	} {
		require.Equal(t, canonical, id(query), query)
	}
	require.Equal(t, int32(1), calls.Load(), "aliased queries should hit the canonical call's cache entry")
}

func TestAliasesBothSpellings(t *testing.T) {
	ctx, srv, calls := newAliasTestServer(t)
	_, err := srv.Query(ctx, `{ thing(shortSHA: "a", shortSha: "b") { name } }`, nil)
	require.ErrorContains(t, err, `argument "shortSHA" given twice, as "shortSHA" and "shortSha"`)
	require.Equal(t, int32(0), calls.Load())

	reqFail(t, newTestClient(srv), `{ thing(shortSHA: "a", shortSha: "b") { name } }`, "given twice")

	// Selecting a field by both names is two selections, like any two fields.
	data, err := srv.Query(ctx, `{ thing(shortSHA: "a") { getURL getUrl } }`, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"thing": map[string]any{
		"getURL": "https://example.com/a",
		"getUrl": "https://example.com/a",
	}}, roundTrip(t, data))
}

// A required argument stays required, by either name.
func TestAliasesRequiredArgument(t *testing.T) {
	ctx, srv, _ := newAliasTestServer(t)
	_, err := srv.Query(ctx, `{ thing { name } }`, nil)
	require.ErrorContains(t, err, `missing required argument: "shortSHA"`)

	// Without aliases, validation still rejects it.
	_, err = srv.Query(ctx, `{ oldThing { name } }`, nil)
	require.ErrorContains(t, err, `missing required argument: "shortSHA"`)
}

// Nothing a client can read shows the aliases: not the schema, not
// introspection.
func TestAliasesHidden(t *testing.T) {
	ctx, srv, _ := newAliasTestServer(t)

	schema := srv.Schema()
	require.Nil(t, schema.Types["AliasThingOld"])
	require.Nil(t, schema.Query.Fields.ForName("oldThing"))
	thing := schema.Query.Fields.ForName("thing")
	require.NotNil(t, thing)
	require.Nil(t, thing.Arguments.ForName("shortSha"))
	require.Nil(t, thing.Arguments.ForName("legacyJson"))
	require.True(t, thing.Arguments.ForName("shortSHA").Type.NonNull)
	require.Nil(t, schema.Types["AliasThing"].Fields.ForName("getUrl"))

	data, err := srv.Query(ctx, `{
		query: __type(name: "Query") { fields(includeDeprecated: true) { name args(includeDeprecated: true) { name } } }
		thing: __type(name: "AliasThing") { fields(includeDeprecated: true) { name } }
		schema: __schema { types { name } }
	}`, nil)
	require.NoError(t, err)
	raw := string(mustJSON(t, data))
	for _, alias := range []string{"oldThing", "shortSha", "legacyJson", "getUrl", "AliasThingOld"} {
		require.NotContains(t, raw, `"`+alias+`"`)
	}
	require.Contains(t, raw, `"shortSHA"`)
	require.Contains(t, raw, `"getURL"`)
	_, err = srv.Query(ctx, `{ __type(name: "AliasThingOld") { name } }`, nil)
	require.ErrorContains(t, err, `unknown type: "AliasThingOld"`)
}

// The schema with aliases has them, marked deprecated, and the canonical
// required argument is optional in it, since either name may give it.
func TestAliasesSchemaWithAliases(t *testing.T) {
	_, srv, _ := newAliasTestServer(t)
	schema := srv.SchemaWithAliases()

	oldThing := schema.Query.Fields.ForName("oldThing")
	require.NotNil(t, oldThing)
	require.Equal(t, "Use `thing` instead.", deprecationReason(oldThing.Directives))
	for _, field := range []*ast.FieldDefinition{oldThing, schema.Query.Fields.ForName("thing")} {
		require.False(t, field.Arguments.ForName("shortSHA").Type.NonNull)
		require.Nil(t, field.Arguments.ForName("shortSHA").Directives.ForName("deprecated"))
		shortSha := field.Arguments.ForName("shortSha")
		require.NotNil(t, shortSha)
		require.False(t, shortSha.Type.NonNull)
		require.Equal(t, "Use `shortSHA` instead.", deprecationReason(shortSha.Directives))
		require.NotNil(t, field.Arguments.ForName("legacyJson"))
	}
	require.NotNil(t, schema.Types["AliasThing"].Fields.ForName("getUrl"))

	old := schema.Types["AliasThingOld"]
	require.NotNil(t, old)
	require.Equal(t, "Use `AliasThing` instead.", deprecationReason(old.Directives))
	require.NotNil(t, old.Fields.ForName("getUrl"))
	require.Equal(t, map[string]string{"AliasThingOld": "AliasThing"}, srv.TypeAliasesForView(srv.View))

	// The schema itself didn't change.
	require.Nil(t, srv.Schema().Types["AliasThingOld"])
	require.True(t, srv.Schema().Query.Fields.ForName("thing").Arguments.ForName("shortSHA").Type.NonNull)
}

// A fragment on a type alias matches where one on its type does, including
// how generated clients load objects: node(id:) { ... on Type { ... } }.
func TestAliasesFragmentTypeCondition(t *testing.T) {
	ctx, srv, _ := newAliasTestServer(t)
	gql := newTestClient(srv)

	var res struct {
		Thing struct {
			ID     string
			GetURL string
			Name   string
		}
	}
	req(t, gql, `{
		thing(shortSHA: "abc") {
			id
			... on AliasThingOld { getURL }
			...oldFragment
		}
	}
	fragment oldFragment on AliasThingOld { name }`, &res)
	require.Equal(t, "https://example.com/abc", res.Thing.GetURL)
	require.Equal(t, "abc", res.Thing.Name)

	var loaded struct {
		Node struct {
			Name   string
			GetUrl string //nolint:revive,staticcheck // the alias is the response key
		}
	}
	// Load it from its recipe, as a generated client would.
	data, err := srv.Query(ctx, `{ thing(shortSHA: "abc") { id } }`, nil)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(mustJSON(t, data), &res))
	data, err = srv.Query(ctx, `query($id: ID!) { node(id: $id) { ... on AliasThingOld { name getUrl } } }`, map[string]any{"id": res.Thing.ID})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(mustJSON(t, data), &loaded))
	require.Equal(t, "abc", loaded.Node.Name)
	require.Equal(t, "https://example.com/abc", loaded.Node.GetUrl)

	// __typename reports the canonical name.
	data, err = srv.Query(ctx, `query($id: ID!) { node(id: $id) { ... on AliasThingOld { __typename } } }`, map[string]any{"id": res.Thing.ID})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"node": map[string]any{"__typename": "AliasThing"}}, roundTrip(t, data))
}

// An alias that collides with a real name, or that two names claim, is
// ignored.
func TestAliasesCollisions(t *testing.T) {
	ctx := dagql.ContextWithCache(testContext(), newCache(t))
	srv := newExternalDagqlServerForTest(t, Query{})
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*aliasThing]{
		// collides with the Query type
		Aliases: aliasesOf("Query", "AliasThing"),
	}))
	str := func(s string) func(context.Context, Query, struct{ A, B string }) (string, error) {
		return func(context.Context, Query, struct{ A, B string }) (string, error) { return s, nil }
	}
	dagql.Fields[Query]{
		dagql.Func("one", str("one")).
			// "two" is a real field; "uno" is claimed by "won" too
			WithAliases(aliasesOf("two", "uno", "one", "ein")).
			Args(dagql.Arg("a").WithAliases(aliasesOf("b", "x"))),
		dagql.Func("two", str("two")),
		dagql.Func("won", str("won")).WithAliases(aliasesOf("uno")),
		dagql.Func("thing", func(context.Context, Query, struct{}) (*aliasThing, error) {
			return &aliasThing{Name: "thing"}, nil
		}),
	}.Install(srv)

	query := func(q string) (map[string]any, error) {
		data, err := srv.Query(ctx, q, nil)
		if err != nil {
			return nil, err
		}
		return roundTrip(t, data), nil
	}
	data, err := query(`{ two(a: "", b: "") ein(a: "", b: "") }`)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"two": "two", "ein": "one"}, data)

	_, err = query(`{ uno(a: "", b: "") }`)
	require.ErrorContains(t, err, `Cannot query field "uno"`)

	// "x" is an alias of a; "b" is a real argument.
	data, err = query(`{ one(x: "", b: "") }`)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"one": "one"}, data)

	require.Empty(t, srv.TypeAliasesForView(srv.View))
	_, err = query(`{ thing { ... on Query { __typename } } }`)
	require.Error(t, err)
}

// Aliases limited to some views are accepted only in those.
func TestAliasesView(t *testing.T) {
	ctx := dagql.ContextWithCache(testContext(), newCache(t))
	srv := newExternalDagqlServerForTest(t, Query{})
	dagql.Fields[Query]{
		dagql.Func("getURL", func(context.Context, Query, struct{}) (string, error) {
			return "url", nil
		}).WithAliases(dagql.Aliases{Names: []string{"getUrl"}, View: dagql.ExactView("v1")}),
	}.Install(srv)

	srv.View = "v0"
	_, err := srv.Query(ctx, `{ getUrl }`, nil)
	require.ErrorContains(t, err, `Cannot query field "getUrl"`)

	srv.View = "v1"
	data, err := srv.Query(ctx, `{ getUrl }`, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"getUrl": "url"}, roundTrip(t, data))
}

// Aliases are part of the schema digest: they change what it accepts.
func TestAliasesSchemaDigest(t *testing.T) {
	without := newExternalDagqlServerForTest(t, Query{})
	with := newExternalDagqlServerForTest(t, Query{})
	field := func() dagql.Field[Query] {
		return dagql.Func("getURL", func(context.Context, Query, struct{}) (string, error) {
			return "url", nil
		})
	}
	dagql.Fields[Query]{field()}.Install(without)
	dagql.Fields[Query]{field().WithAliases(aliasesOf("getUrl"))}.Install(with)
	require.NotEqual(t, without.SchemaDigest(), with.SchemaDigest())
}

// Using an alias adds an event naming it and its canonical name to the
// current span.
func TestAliasesDeprecationEvent(t *testing.T) {
	ctx, srv, _ := newAliasTestServer(t)
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(sr))
	ctx, span := tp.Tracer("aliases-test").Start(ctx, "query")
	_, err := srv.Query(ctx, `{ oldThing(shortSha: "a") { ... on AliasThingOld { name } } }`, nil)
	require.NoError(t, err)
	span.End()

	var got []map[string]string
	for _, s := range sr.Ended() {
		if s.Name() != "query" {
			continue
		}
		for _, ev := range s.Events() {
			if ev.Name != telemetryattrs.DagAliasEvent {
				continue
			}
			attrs := map[string]string{}
			for _, kv := range ev.Attributes {
				attrs[string(kv.Key)] = kv.Value.AsString()
			}
			got = append(got, attrs)
		}
	}
	require.Equal(t, []map[string]string{
		{
			telemetryattrs.DagAliasKindAttr:      "field",
			telemetryattrs.DagAliasParentAttr:    "Query",
			telemetryattrs.DagAliasNameAttr:      "oldThing",
			telemetryattrs.DagAliasCanonicalAttr: "thing",
		},
		{
			telemetryattrs.DagAliasKindAttr:      "argument",
			telemetryattrs.DagAliasParentAttr:    "Query.thing",
			telemetryattrs.DagAliasNameAttr:      "shortSha",
			telemetryattrs.DagAliasCanonicalAttr: "shortSHA",
		},
		{
			telemetryattrs.DagAliasKindAttr:      "type",
			telemetryattrs.DagAliasNameAttr:      "AliasThingOld",
			telemetryattrs.DagAliasCanonicalAttr: "AliasThing",
		},
	}, got)
}

func deprecationReason(dirs ast.DirectiveList) string {
	dir := dirs.ForName("deprecated")
	if dir == nil {
		return "<not deprecated>"
	}
	return dir.Arguments.ForName("reason").Value.Raw
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}

func roundTrip(t *testing.T, v any) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(mustJSON(t, v), &out))
	return out
}
