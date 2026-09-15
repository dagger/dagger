package core

import (
	"context"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (DirectorySuite) TestWithNewDirectoryNoop(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	parent := c.Directory().
		WithNewDirectory("scope/existing", dagger.DirectoryWithNewDirectoryOpts{Permissions: 0o750}).
		WithNewFile("scope/existing/keep.txt", "keep").
		Directory("scope")

	// Even though MkdirAll does nothing, committing a snapshot per call used
	// to exceed overlay mount limits before this chain could be read.
	result := parent
	for range 600 {
		result = result.WithNewDirectory("existing", dagger.DirectoryWithNewDirectoryOpts{Permissions: 0o700})
	}
	contents, err := result.File("existing/keep.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "keep", contents)

	stat, err := result.Stat(ctx, "existing")
	require.NoError(t, err)
	permissions, err := stat.Permissions(ctx)
	require.NoError(t, err)
	require.Equal(t, 0o750, permissions)

	// Reusing the snapshot must leave both branches usable for later writes.
	for _, branch := range []*dagger.Directory{parent, result} {
		contents, err := branch.WithNewFile("existing/next.txt", "next").File("existing/next.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "next", contents)
	}
}

func (DirectorySuite) TestWithNewDirectoryPaths(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	parent := c.Directory().
		WithNewFile("scope/existing/keep.txt", "keep").
		WithSymlink("existing", "scope/link").
		WithSymlink("missing", "scope/dangling").
		Directory("scope")

	for _, target := range []string{".", "/", "existing", "/existing", "link"} {
		t.Run(target, func(ctx context.Context, t *testctx.T) {
			result := parent.WithNewDirectory(target)
			empty, err := result.Changes(parent).IsEmpty(ctx)
			require.NoError(t, err)
			require.True(t, empty)
		})
	}

	t.Run("creates through symlinks", func(ctx context.Context, t *testctx.T) {
		for target, created := range map[string]string{"link/new": "existing/new", "dangling": "missing"} {
			result := parent.WithNewDirectory(target, dagger.DirectoryWithNewDirectoryOpts{Permissions: 0o700})
			stat, err := result.Stat(ctx, created)
			require.NoError(t, err)
			permissions, err := stat.Permissions(ctx)
			require.NoError(t, err)
			require.Equal(t, 0o700, permissions)
		}
	})

	t.Run("file conflicts still fail", func(ctx context.Context, t *testctx.T) {
		for _, target := range []string{"existing/keep.txt", "existing/keep.txt/child"} {
			_, err := parent.WithNewDirectory(target).Sync(ctx)
			require.Error(t, err)
		}
	})

	t.Run("scratch root", func(ctx context.Context, t *testctx.T) {
		entries, err := c.Directory().WithNewDirectory(".").Entries(ctx)
		require.NoError(t, err)
		require.Empty(t, entries)
	})
}

func (DirectorySuite) TestWithNewDirectoryNoopCache(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	parent := c.Directory().WithNewFile("existing/keep.txt", identity.NewID())
	run := func(dir *dagger.Directory) string {
		out, err := c.Container().From(alpineImage).
			WithMountedDirectory("/input", dir).
			WithExec([]string{"sh", "-c", "head -c 32 /dev/urandom | base64"}).
			Stdout(ctx)
		require.NoError(t, err)
		return out
	}
	want := run(parent)

	// Evaluation discovers the no-op. Downstream calls should then reuse the
	// parent's cached results instead of treating it as a different input.
	result := parent.WithNewDirectory("existing")
	_, err := result.Sync(ctx)
	require.NoError(t, err)
	require.Equal(t, want, run(result))
}
