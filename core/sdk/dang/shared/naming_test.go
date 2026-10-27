package dangshared_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/core"
	dangshared "github.com/dagger/dagger/core/sdk/dang/shared"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

type testQuery struct{}

func (testQuery) Type() *ast.Type { return &ast.Type{NamedType: "Query", NonNull: true} }

type testTypeDef struct{}

func (*testTypeDef) Type() *ast.Type { return &ast.Type{NamedType: "TypeDef", NonNull: true} }

type testModule struct{}

func (*testModule) Type() *ast.Type { return &ast.Type{NamedType: "Module", NonNull: true} }

// NamingSelectors stamps the module's naming view on the typedef constructors
// core names with (core.IsNamingField), by the type each selector selects
// from, and leaves other selectors alone.
func TestNamingSelectors(t *testing.T) {
	srv, err := dagql.NewServer(t.Context(), testQuery{})
	require.NoError(t, err)
	srv.InstallObject(dagql.NewClass[*testTypeDef](srv))
	srv.InstallObject(dagql.NewClass[*testModule](srv))
	typeDef := func(context.Context, *testTypeDef, struct{ Name string }) (*testTypeDef, error) {
		return &testTypeDef{}, nil
	}
	module := func(context.Context, *testModule, struct{ Name string }) (*testModule, error) {
		return &testModule{}, nil
	}
	dagql.Fields[testQuery]{
		dagql.Func("typeDef", func(context.Context, testQuery, struct{}) (*testTypeDef, error) {
			return &testTypeDef{}, nil
		}),
		dagql.Func("module", func(context.Context, testQuery, struct{}) (*testModule, error) {
			return &testModule{}, nil
		}),
	}.Install(srv)
	dagql.Fields[*testTypeDef]{
		dagql.Func("withObject", typeDef).View(core.AllVersion),
		dagql.Func("withCollectionKeys", typeDef).View(core.AfterVersion("v1.0.0-0")),
		dagql.Func("withDescription", typeDef),
	}.Install(srv)
	dagql.Fields[*testModule]{
		dagql.Func("withObject", module).View(core.AllVersion),
	}.Install(srv)

	src := &core.ModuleSource{EngineVersion: "v1.0.0-beta.21"}
	ctx := dangshared.WithModuleNaming(t.Context(), src)
	view := src.NamingView()

	views := func(sels []dagql.Selector) []call.View {
		out := make([]call.View, len(sels))
		for i, sel := range sels {
			out[i] = sel.View
		}
		return out
	}

	got := dangshared.NamingSelectors(ctx, srv,
		dagql.Selector{Field: "typeDef"},
		dagql.Selector{Field: "withObject"},
		dagql.Selector{Field: "withDescription"},
		dagql.Selector{Field: "withCollectionKeys"},
	)
	require.Equal(t, []call.View{"", view, "", view}, views(got))

	// Module.withObject installs a typedef, it doesn't name one.
	got = dangshared.NamingSelectors(ctx, srv,
		dagql.Selector{Field: "module"},
		dagql.Selector{Field: "withObject"},
	)
	require.Equal(t, []call.View{"", ""}, views(got))

	// An explicit view is kept.
	got = dangshared.NamingSelectors(ctx, srv,
		dagql.Selector{Field: "typeDef"},
		dagql.Selector{Field: "withObject", View: "v0.20.0"},
	)
	require.Equal(t, []call.View{"", "v0.20.0"}, views(got))

	// Without a module's naming view, nothing is stamped.
	got = dangshared.NamingSelectors(t.Context(), srv,
		dagql.Selector{Field: "typeDef"},
		dagql.Selector{Field: "withObject"},
	)
	require.Equal(t, []call.View{"", ""}, views(got))
}
