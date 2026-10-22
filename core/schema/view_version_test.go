package schema

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/cmd/codegen/introspection/sdl"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
)

// Callers' declared versions, mapped to views as the engine does, against
// view gates at the identifier casing cut and at boundaries set while views
// dropped prereleases.
var viewGateCases = []struct {
	version string
	// post: past engine.IdentifierCasingVersion
	post bool
	// v1: in the v1.0 API, as gates before the cut see it
	v1 bool
}{
	{version: "v0.21.6"},
	{version: "v1.0.0-0", v1: true},
	{version: "v1.0.0-beta.7", v1: true},
	{version: "v1.0.0-beta.19", v1: true},
	{version: "v1.0.0-beta.20", v1: true},
	{version: "v1.0.0-beta.21", post: true, v1: true},
	{version: "v1.0.0-beta.22", post: true, v1: true},
	{version: "v1.0.0-dev-0123456789ab", post: true, v1: true},
	{version: "v1.0.0", post: true, v1: true},
}

func newViewGateTestServer(t *testing.T) (context.Context, *dagql.Server) {
	t.Helper()
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "view-gate", SessionID: "view-gate"})

	root := core.NewRoot(&currentTypeDefsTestServer{})
	srv, err := dagql.NewServer(ctx, root)
	require.NoError(t, err)

	callerView := func(ctx context.Context, _ *core.Query, _ struct{}) (dagql.String, error) {
		return dagql.String(core.CallerView(ctx)), nil
	}
	supports := func(ctx context.Context, _ *core.Query, _ struct{}) (dagql.Boolean, error) {
		return dagql.Boolean(core.Supports(ctx, engine.IdentifierCasingVersion)), nil
	}
	dagql.Fields[*core.Query]{
		dagql.Func("postCut", callerView).View(AfterVersion(engine.IdentifierCasingVersion)),
		dagql.Func("preCut", callerView).View(BeforeVersion(engine.IdentifierCasingVersion)),
		dagql.Func("v1", callerView).View(AfterVersion("v1.0.0-0")),
		dagql.Func("beta17", callerView).View(AfterVersion("v1.0.0-beta.17")),
		dagql.Func("legacy", callerView).View(BeforeVersion("v1.0.0-0")),
		dagql.Func("supportsCut", supports).View(AllVersion),
		dagql.Func("unscoped", callerView),
	}.Install(srv)
	return ctx, srv
}

// A field gated at the cut is visible to callers declaring it or later,
// including dev builds and the release, and hidden from earlier ones, while
// gates set before views kept prereleases keep applying to every v1.0.0
// prerelease.
func TestViewGateAtIdentifierCasingVersion(t *testing.T) {
	ctx, srv := newViewGateTestServer(t)
	for _, tc := range viewGateCases {
		t.Run(tc.version, func(t *testing.T) {
			view := call.View(engine.APIViewVersion(tc.version))
			fields := srv.SchemaForView(view).Query.Fields
			has := func(name string) bool { return fields.ForName(name) != nil }
			require.Equal(t, tc.post, has("postCut"), "postCut in view %s", view)
			require.Equal(t, !tc.post, has("preCut"), "preCut in view %s", view)
			require.Equal(t, tc.v1, has("v1"), "v1 in view %s", view)
			require.Equal(t, tc.v1, has("beta17"), "beta17 in view %s", view)
			require.Equal(t, !tc.v1, has("legacy"), "legacy in view %s", view)

			var res struct {
				SupportsCut bool
			}
			srv.View = view
			require.NoError(t, srv.Select(ctx, srv.Root(), &res.SupportsCut, dagql.Selector{Field: "supportsCut", View: view}))
			require.Equal(t, tc.post, res.SupportsCut, "core.Supports in view %s", view)
		})
	}
}

// View-scoped fields carry the view in their call identity, so callers on
// either side of the cut never share a cached result, while pre-cut callers,
// which share a view, do. An unscoped field's identity has no view: a
// resolver whose result depends on the caller's version must be view-scoped.
func TestViewGateCacheIdentity(t *testing.T) {
	ctx, srv := newViewGateTestServer(t)
	selectIn := func(version, field string) string {
		t.Helper()
		view := call.View(engine.APIViewVersion(version))
		srv.View = view
		sel := dagql.Selector{Field: field, View: view}
		var out string
		require.NoError(t, srv.Select(ctx, srv.Root(), &out, sel))
		return out
	}

	require.Equal(t, "v1.0.0-beta.20", selectIn("v1.0.0-beta.19", "v1"))
	require.Equal(t, "v1.0.0-beta.20", selectIn("v1.0.0-beta.20", "v1"))
	require.Equal(t, "v1.0.0-beta.21", selectIn("v1.0.0-beta.21", "v1"))
	require.Equal(t, "v1.0.0", selectIn("v1.0.0", "v1"))

	first := selectIn("v1.0.0-beta.20", "unscoped")
	require.Equal(t, first, selectIn("v1.0.0-beta.21", "unscoped"),
		"unscoped fields share one cached result across views")
}

// Every v1.0.0 prerelease before the cut gets the same core schema, so no
// view gate set before views kept prereleases tells them apart, and the gates
// they saw still apply: the v1.0 API (Workspace, Container.withGPU,
// disableDaggerInDagger) and @reevaluate (from v1.0.0-beta.17).
func TestPreCutCoreSchemaShared(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "pre-cut", SessionID: "pre-cut"})
	srv := &currentTypeDefsTestServer{}
	base, err := NewCoreSchemaBase(ctx, srv)
	require.NoError(t, err)
	root := core.NewRoot(srv)

	schemaFor := func(version string) (*ast.Schema, string) {
		view := call.View(engine.APIViewVersion(version))
		dag, err := base.Fork(ctx, root, view)
		require.NoError(t, err)
		data, err := getSchemaJSON(nil, nil, view, dag)
		require.NoError(t, err)
		resp := decodeSchemaResponse(t, data)
		var out strings.Builder
		sdl.Format(&out, resp.Schema)
		return dag.SchemaForView(view), out.String()
	}

	_, want := schemaFor("v1.0.0-beta.20")
	for _, version := range []string{"v1.0.0-0", "v1.0.0-beta.7", "v1.0.0-beta.19"} {
		schema, got := schemaFor(version)
		require.Equal(t, want, got, "schema for %s", version)
		require.NotNil(t, schema.Types["Workspace"], version)
		require.NotNil(t, schema.Types["Container"].Fields.ForName("withGPU"), version)
		require.NotNil(t, schema.Types["Container"].Fields.ForName("withExec").Arguments.ForName("disableDaggerInDagger"), version)
		require.Contains(t, schema.Directives, "reevaluate", version)
	}
}
