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
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
)

func TestCacheVolume(t *testing.T) {
	t.Parallel()

	type creatVolumeRes struct {
		CacheVolume struct {
			ID core.CacheVolumeID
		}
	}

	var idOrig, idSame, idDiff core.CacheVolumeID

	t.Run("creating from a key", func(t *testing.T) {
		var res creatVolumeRes
		err := testutil.Query(
			`{
				cacheVolume(key: "ab") {
					id
				}
			}`, &res, nil)
		require.NoError(t, err)

		idOrig = res.CacheVolume.ID
		require.NotEmpty(t, res.CacheVolume.ID)
	})

	t.Run("creating from same key again", func(t *testing.T) {
		var res creatVolumeRes
		err := testutil.Query(
			`{
				cacheVolume(key: "ab") {
					id
				}
			}`, &res, nil)
		require.NoError(t, err)

		idSame = res.CacheVolume.ID
		require.NotEmpty(t, idSame)

		require.Equal(t, idOrig, idSame)
	})

	t.Run("creating from a different key", func(t *testing.T) {
		var res creatVolumeRes
		err := testutil.Query(
			`{
				cacheVolume(key: "ac") {
					id
				}
			}`, &res, nil)
		require.NoError(t, err)

		idDiff = res.CacheVolume.ID
		require.NotEmpty(t, idDiff)

		require.NotEqual(t, idOrig, idDiff)
	})
}

func TestCacheVolumeWithSubmount(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	t.Run("file mount", func(t *testing.T) {
		t.Parallel()
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

	t.Run("dir mount", func(t *testing.T) {
		t.Parallel()
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

func TestLocalImportCacheReuse(t *testing.T) {
	t.Parallel()

	hostDirPath := t.TempDir()
	err := os.WriteFile(filepath.Join(hostDirPath, "foo"), []byte("bar"), 0o644)
	require.NoError(t, err)

	runExec := func(ctx context.Context, t *testing.T, c *dagger.Client) string {
		out, err := c.Container().From(alpineImage).
			WithDirectory("/fromhost", c.Host().Directory(hostDirPath)).
			WithExec([]string{"stat", "/fromhost/foo"}).
			WithExec([]string{"sh", "-c", "head -c 128 /dev/random | sha256sum"}).
			Stdout(ctx)
		require.NoError(t, err)
		return out
	}

	c1, ctx1 := connect(t)
	out1 := runExec(ctx1, t, c1)

	c2, ctx2 := connect(t)
	out2 := runExec(ctx2, t, c2)

	require.Equal(t, out1, out2)
}
