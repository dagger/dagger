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
	require.NoError(t, engine.Start(context.Background(), nil, func(ctx engine.Context) error {
		api := api.New()
		// FIXME: use scratch instead
		dir := api.Git("github.com/dagger/dagger").
			Branch("cloak").
			Tree()

		contents, err := dir.
			WithNewFile("/hello.txt", "world").
			File("/hello.txt").
			Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, "world", contents)

		return nil
	}))
}

func TestGit(t *testing.T) {
	require.NoError(t, engine.Start(context.Background(), nil, func(ctx engine.Context) error {
		api := api.New()
		tree := api.Git("github.com/dagger/dagger").
			Branch("cloak").
			Tree()

		files, err := tree.Contents(ctx, "/")
		require.NoError(t, err)
		require.Contains(t, files, "README.md")

		readmeFile := tree.File("README.md")

		readme, err := readmeFile.Contents(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, readme)
		require.Contains(t, readme, "Dagger")

		readmeID, err := readmeFile.ID(ctx)
		require.NoError(t, err)

		otherReadme, err := api.File(readmeID).Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, readme, otherReadme)

		return nil
	}))
}

func TestContainer(t *testing.T) {
	require.NoError(t, engine.Start(context.Background(), nil, func(ctx engine.Context) error {
		core := api.New()
		contents, err := core.
			Container("").
			From("alpine").
			Rootfs().
			File("/etc/alpine-release").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello world\n", contents)

		return nil
	}))
}
