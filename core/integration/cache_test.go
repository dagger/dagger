package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestCacheVolume(t *testing.T) {
	t.Parallel()

	type creatVolumeRes struct {
		CacheVolume struct {
			ID core.CacheID
		}
	}

	var idOrig, idSame, idDiff core.CacheID

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

func TestLocalImportCacheReuse(t *testing.T) {
	t.Parallel()

	hostDirPath := t.TempDir()
	err := os.WriteFile(filepath.Join(hostDirPath, "foo"), []byte("bar"), 0o644)
	require.NoError(t, err)

	runExec := func(ctx context.Context, t *testing.T, c *dagger.Client) string {
		out, err := c.Container().From("alpine:3.16.2").
			WithDirectory("/fromhost", c.Host().Directory(hostDirPath)).
			WithExec([]string{"sh", "-c", "head -c 128 /dev/random | sha256sum"}).
			Stdout(ctx)
		require.NoError(t, err)
		return out
	}

	c1, ctx1 := connect(t)
	defer c1.Close()
	ctx1, cancel1 := context.WithCancel(ctx1)
	defer cancel1()
	out1 := runExec(ctx1, t, c1)

	c2, ctx2 := connect(t)
	defer c2.Close()
	ctx2, cancel2 := context.WithCancel(ctx2)
	defer cancel2()
	out2 := runExec(ctx2, t, c2)

	require.Equal(t, out1, out2)
}
