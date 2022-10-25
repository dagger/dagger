package core

import (
	"testing"

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
