package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
	"github.com/dagger/dagger/testctx"
)

type CacheSuite struct{}

func TestCache(t *testing.T) {
	testctx.Run(testCtx, t, CacheSuite{}, Middleware()...)
}

func (CacheSuite) TestVolume(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	volID1, err := c.CacheVolume("ab").ID(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, volID1)

	volID2, err := c.CacheVolume("ab").ID(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, volID2)

	volID3, err := c.CacheVolume("ac").ID(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, volID3)

	require.Equal(t, volID1, volID2)
	require.NotEqual(t, volID1, volID3)
}

func (CacheSuite) TestVolumeWithSubmount(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("file mount", func(ctx context.Context, t *testctx.T) {
		subfile := c.Directory().WithNewFile("foo", "bar").File("foo")
		ctr := c.Container().From(alpineImage).
			WithMountedCache("/cache", c.CacheVolume(identity.NewID())).
			WithMountedFile("/cache/subfile", subfile)

		out, err := ctr.WithExec([]string{"cat", "/cache/subfile"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar", strings.TrimSpace(out))

		contents, err := ctr.File("/cache/subfile").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar", strings.TrimSpace(contents))
	})

	t.Run("dir mount", func(ctx context.Context, t *testctx.T) {
		subdir := c.Directory().WithNewFile("foo", "bar").WithNewFile("baz", "qux")
		ctr := c.Container().From(alpineImage).
			WithMountedCache("/cache", c.CacheVolume(identity.NewID())).
			WithMountedDirectory("/cache/subdir", subdir)

		for fileName, expectedContents := range map[string]string{
			"foo": "bar",
			"baz": "qux",
		} {
			subpath := filepath.Join("/cache/subdir", fileName)
			out, err := ctr.WithExec([]string{"cat", subpath}).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, expectedContents, strings.TrimSpace(out))

			contents, err := ctr.File(subpath).Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, expectedContents, strings.TrimSpace(contents))

			dir := ctr.Directory("/cache/subdir")
			contents, err = dir.File(fileName).Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, expectedContents, strings.TrimSpace(contents))
		}
	})
}

func (CacheSuite) TestLocalImportCacheReuse(ctx context.Context, t *testctx.T) {
	hostDirPath := t.TempDir()
	err := os.WriteFile(filepath.Join(hostDirPath, "foo"), []byte("bar"), 0o644)
	require.NoError(t, err)

	runExec := func(c *dagger.Client) string {
		out, err := c.Container().From(alpineImage).
			WithDirectory("/fromhost", c.Host().Directory(hostDirPath)).
			WithExec([]string{"stat", "/fromhost/foo"}).
			WithExec([]string{"sh", "-c", "head -c 128 /dev/random | sha256sum"}).
			Stdout(ctx)
		require.NoError(t, err)
		return out
	}

	c1 := connect(ctx, t)
	out1 := runExec(c1)

	c2 := connect(ctx, t)
	out2 := runExec(c2)

	require.Equal(t, out1, out2)
}
