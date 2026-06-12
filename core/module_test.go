package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestNamespaceSourceMap(t *testing.T) {
	mod := &Module{NameField: "mymod"}

	t.Run("synthesizes module-name-only source map when SDK provides none", func(t *testing.T) {
		ctx := t.Context()

		cache, err := dagql.NewCache(ctx, "", nil, nil)
		require.NoError(t, err)
		ctx = dagql.ContextWithCache(ctx, cache)

		root := &Query{}
		testSrv := &moduleObjectTestServer{
			mockServer: &mockServer{},
			cache:      cache,
			root:       root,
		}
		root.Server = testSrv
		dag := newCoreDagqlServerForTest(t, root)
		testSrv.dag = dag

		dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*SourceMap]{Typed: &SourceMap{}}))
		dagql.Fields[*Query]{
			dagql.Func("sourceMap", func(_ context.Context, _ *Query, args struct {
				Module   dagql.Optional[dagql.String] `internal:"true"`
				Filename string
				Line     int
				Column   int
				URL      dagql.Optional[dagql.String] `internal:"true"`
			}) (*SourceMap, error) {
				var module string
				if args.Module.Valid {
					module = string(args.Module.Value)
				}
				var url string
				if args.URL.Valid {
					url = string(args.URL.Value)
				}
				return &SourceMap{
					Module:   module,
					Filename: args.Filename,
					Line:     args.Line,
					Column:   args.Column,
					URL:      url,
				}, nil
			}),
		}.Install(dag)

		ctx = ContextWithQuery(ctx, root)
		ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
			ClientID:  "namespace-source-map-test-client",
			SessionID: "namespace-source-map-test-session",
		})

		result, err := mod.namespaceSourceMap(ctx, "sub", dagql.Null[dagql.ObjectResult[*SourceMap]]())
		require.NoError(t, err)
		require.True(t, result.Valid)
		require.NotNil(t, result.Value.Self())
		require.Equal(t, "mymod", result.Value.Self().Module)
	})
}
