package schema

import (
	"context"
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
	require.Equal(t, "mod(gogithub.com/dagger/dagger/.dagger/modules/go)", namespace("git", &core.ModuleSource{
		Kind: core.ModuleSourceKindGit,
		Git:  &core.GitModuleSource{Symbolic: "github.com/dagger/dagger/.dagger/modules/go"},
	}))
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
