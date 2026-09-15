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

func TestEngineDefaultPlatformCacheIdentity(t *testing.T) {
	ctx := context.Background()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cache.Close(context.Background()))
	})
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "default-platform-client",
		SessionID: "default-platform-session",
	})

	amd64 := core.Platform{OS: "linux", Architecture: "amd64"}
	arm64 := core.Platform{OS: "linux", Architecture: "arm64"}
	srv := &currentTypeDefsTestServer{platform: amd64}
	root := core.NewRoot(srv)
	ctx = core.ContextWithQuery(ctx, root)
	coreSchemaBase, err := NewCoreSchemaBase(ctx, srv)
	require.NoError(t, err)
	dag, err := coreSchemaBase.Fork(ctx, root, "")
	require.NoError(t, err)
	srv.dag = dag

	selectDefaultPlatform := func() core.Platform {
		t.Helper()
		var platform core.Platform
		require.NoError(t, dag.Select(ctx, dag.Root(), &platform, dagql.Selector{Field: "defaultPlatform"}))
		return platform
	}

	require.Equal(t, amd64, selectDefaultPlatform())
	srv.platform = arm64
	require.Equal(t, arm64, selectDefaultPlatform())

	queryType, ok := dag.ObjectType("Query")
	require.True(t, ok)
	directoryField, ok := queryType.FieldSpec("directory", call.View(""))
	require.True(t, ok)
	require.Equal(t, []string{"engineDefaultPlatform"}, implicitInputNames(directoryField))

	directoryType, ok := dag.ObjectType("Directory")
	require.True(t, ok)
	dockerBuildField, ok := directoryType.FieldSpec("dockerBuild", call.View(""))
	require.True(t, ok)
	require.NotNil(t, dockerBuildField.GetDynamicInput)
	require.Equal(t, []string{"engineDefaultPlatform"}, implicitInputNames(dockerBuildField))
}

func implicitInputNames(spec dagql.FieldSpec) []string {
	names := make([]string, 0, len(spec.ImplicitInputs))
	for _, input := range spec.ImplicitInputs {
		names = append(names, input.Name)
	}
	return names
}
