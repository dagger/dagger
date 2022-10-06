//go:generate cloak client-gen -o ./api/api.gen.go
package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.dagger.io/dagger/codegen/internal/test/api"
	"go.dagger.io/dagger/engine"
	"go.dagger.io/dagger/internal/testutil"
)

func init() {
	if err := testutil.SetupBuildkitd(); err != nil {
		panic(err)
	}
}

func TestDirectory(t *testing.T) {
	t.Parallel()
	require.NoError(t, engine.Start(context.Background(), nil, func(ctx engine.Context) error {
		core := api.New(ctx.Client)

		dir := core.Directory()

		contents, err := dir.
			WithNewFile("/hello.txt", api.WithDirectoryWithNewFileContents("world")).
			File("/hello.txt").
			Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, "world", contents)
		return nil
	}))
}

func TestGit(t *testing.T) {
	t.Parallel()
	require.NoError(t, engine.Start(context.Background(), nil, func(ctx engine.Context) error {
		core := api.New(ctx.Client)
		tree := core.Git("github.com/dagger/dagger").
			Branch("cloak").
			Tree()

		files, err := tree.Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, files, "README.md")

		readmeFile := tree.File("README.md")

		readme, err := readmeFile.Contents(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, readme)
		require.Contains(t, readme, "Dagger")

		readmeID, err := readmeFile.ID(ctx)
		require.NoError(t, err)

		otherReadme, err := core.File(readmeID).Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, readme, otherReadme)

		return nil
	}))
}

func TestContainer(t *testing.T) {
	t.Parallel()
	require.NoError(t, engine.Start(context.Background(), nil, func(ctx engine.Context) error {
		core := api.New(ctx.Client)
		alpine := core.
			Container().
			From("alpine:3.16.2")

		contents, err := alpine.
			Rootfs().
			File("/etc/alpine-release").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "3.16.2\n", contents)

		stdout, err := alpine.Exec([]string{"cat", "/etc/alpine-release"}).Stdout().Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "3.16.2\n", stdout)

		return nil
	}))
}
