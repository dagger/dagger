package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestDirectoryResolveBrokenSymlinksSchema(t *testing.T) {
	ctx := context.Background()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "follow-symlinks-client", SessionID: "follow-symlinks-session"})
	srv := &currentTypeDefsTestServer{}
	base, err := NewCoreSchemaBase(ctx, srv)
	require.NoError(t, err)
	for _, version := range []string{"v0.21.6", "v1.0.0"} {
		view := call.View(engine.APIViewVersion(version))
		dag, err := base.Fork(ctx, core.NewRoot(srv), view)
		require.NoError(t, err)
		schema := dag.SchemaForView(view)
		for _, typ := range []string{"Directory", "Container"} {
			arg := schema.Types[typ].Fields.ForName("directory").Arguments.ForName("resolveBrokenSymlinks")
			if version == "v0.21.6" {
				require.Nil(t, arg, typ)
			} else {
				require.NotNil(t, arg, typ)
				require.Equal(t, "Boolean", arg.Type.Name())
				require.Equal(t, "false", arg.DefaultValue.Raw)
				// Keep old argument positions in generated clients.
				args := schema.Types[typ].Fields.ForName("directory").Arguments
				require.Equal(t, "resolveBrokenSymlinks", args[len(args)-1].Name)
			}
			require.Nil(t, schema.Types[typ].Fields.ForName("withDirectory").Arguments.ForName("resolveBrokenSymlinks"))
			require.Nil(t, schema.Types[typ].Fields.ForName("directory").Arguments.ForName("followSymlinks"))
		}
	}
}
