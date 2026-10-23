package schema

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

func TestNamespaceFromModule(t *testing.T) {
	ctx := t.Context()

	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)

	srv := &currentTypeDefsTestServer{}
	root := core.NewRoot(srv)
	dag, err := dagql.NewServer(ctx, root)
	require.NoError(t, err)
	srv.dag = dag
	ctx = core.ContextWithQuery(ctx, root)
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.ModuleSource]{Typed: &core.ModuleSource{}}))
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.Directory]{Typed: &core.Directory{}}))
	dagql.Fields[*core.Directory]{
		dagql.NodeFunc("digest", func(context.Context, dagql.ObjectResult[*core.Directory], struct{}) (dagql.String, error) {
			return dagql.String("test-directory-digest"), nil
		}),
	}.Install(dag)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "namespace", SessionID: "namespace"})

	namespace := func(name string, src *core.ModuleSource) string {
		t.Helper()
		dir := &core.Directory{}
		dirRes, err := dagql.NewObjectResultForCall(dir, dag, implementationScopedTestSyntheticCall(name+"-dir", dir))
		require.NoError(t, err)
		src.ModuleOriginalName, src.ContextDirectory = "go", dirRes
		srcRes, err := dagql.NewObjectResultForCall(src, dag, implementationScopedTestSyntheticCall(name+"-source", src))
		require.NoError(t, err)
		ns, err := namespaceFromModule(ctx, &core.Module{Source: dagql.NonNull(srcRes)})
		require.NoError(t, err)
		return ns
	}

	gitRoot, localRoot := "github.com/dagger/dagger", ""

	ns, err := namespaceFromModule(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, "mainClient", ns)

	require.Equal(t, "mod(go.dagger/modules/go)", namespace("local", &core.ModuleSource{
		Kind:              core.ModuleSourceKindLocal,
		SourceRootSubpath: ".dagger/modules/go",
	}))
	for i, cloneRef := range []string{
		"github.com/dagger/dagger",
		"https://github.com/dagger/dagger",
		"https://github.com/dagger/dagger.git",
		"git@github.com:dagger/dagger.git",
		"ssh://git@github.com/dagger/dagger",
		"https://synthuser:synthsecret@github.com/dagger/dagger",
	} {
		require.Equal(t, "mod(gogithub.com/dagger/dagger/.dagger/modules/go)", namespace(fmt.Sprintf("git-%d", i), &core.ModuleSource{
			Kind:              core.ModuleSourceKindGit,
			SourceRootSubpath: ".dagger/modules/go",
			Git:               &core.GitModuleSource{CloneRef: cloneRef},
		}), cloneRef)
		root := cloneRef
		require.Equal(t, "mod(gogithub.com/dagger/dagger/.dagger/modules/go)", namespace(fmt.Sprintf("dir-from-git-%d", i), &core.ModuleSource{
			Kind:              core.ModuleSourceKindDir,
			SourceRootSubpath: ".dagger/modules/go",
			DirSrc:            &core.DirModuleSource{OriginRoot: &root},
		}), cloneRef)
	}
	require.Equal(t, "mod(gogithub.com/dagger/dagger/.dagger/modules/go)", namespace("dir-from-git", &core.ModuleSource{
		Kind:              core.ModuleSourceKindDir,
		SourceRootSubpath: ".dagger/modules/go",
		DirSrc:            &core.DirModuleSource{OriginRoot: &gitRoot},
	}))
	require.Equal(t, "mod(go.dagger/modules/go)", namespace("dir-from-local", &core.ModuleSource{
		Kind:              core.ModuleSourceKindDir,
		SourceRootSubpath: ".dagger/modules/go",
		DirSrc:            &core.DirModuleSource{OriginRoot: &localRoot},
	}))

	dirSrc := &core.ModuleSource{
		Kind:              core.ModuleSourceKindDir,
		SourceRootSubpath: ".dagger/modules/go",
		DirSrc:            &core.DirModuleSource{},
	}
	ns = namespace("dir", dirSrc)
	sourceDigest, err := dirSrc.SourceImplementationDigest(ctx)
	require.NoError(t, err)
	require.Equal(t, "mod(go"+sourceDigest.String()+")", ns)
}

func TestCacheVolumeNamespace(t *testing.T) {
	ctx := t.Context()

	srv := &currentTypeDefsTestServer{}
	root := core.NewRoot(srv)
	dag, err := dagql.NewServer(ctx, root)
	require.NoError(t, err)
	srv.dag = dag
	ctx = core.ContextWithQuery(ctx, root)
	parent, ok := dag.Root().(dagql.ObjectResult[*core.Query])
	require.True(t, ok)

	namespace := func(clientMetadata *engine.ClientMetadata, args cacheArgs) string {
		t.Helper()
		req := &dagql.CallRequest{ResultCall: &dagql.ResultCall{}}
		err := (&cacheSchema{}).cacheVolumeCacheKey(engine.ContextWithClientMetadata(ctx, clientMetadata), parent, args, req)
		require.NoError(t, err)
		arg := req.Arg("namespace")
		if arg == nil {
			return ""
		}
		return arg.Value.StringValue
	}

	require.Equal(t, "mainClient", namespace(&engine.ClientMetadata{}, cacheArgs{Key: "go-sdk-v2-mod"}))
	require.Equal(t, "sdkEntrypoint", namespace(&engine.ClientMetadata{SDKEntrypoint: true}, cacheArgs{Key: "go-sdk-v2-mod"}))
	require.Equal(t, "sdkEntrypoint", namespace(&engine.ClientMetadata{SDKEntrypoint: true}, cacheArgs{Key: "gomod"}),
		"an entrypoint does not reach the legacy runtimes' volumes")
	require.Empty(t, namespace(&engine.ClientMetadata{SDKEntrypoint: true}, cacheArgs{Key: "gomod", Namespace: "custom"}),
		"an explicit namespace is kept as passed")
}
