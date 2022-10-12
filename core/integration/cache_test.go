package core

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.dagger.io/dagger/core"
	"go.dagger.io/dagger/internal/testutil"
)

func TestCacheVolume(t *testing.T) {
	t.Parallel()

	type createWithKeyRes struct {
		Cache struct {
			WithKey struct {
				ID core.CacheID
			}
		}
	}

	type createRes struct {
		Cache struct {
			ID core.CacheID
		}
	}

	var idOrig, idSame, idDiff, idGiven core.CacheID

	t.Run("creating from scratch", func(t *testing.T) {
		var res createRes
		err := testutil.Query(
			`{
				cache {
					id
				}
			}`, &res, nil)
		require.NoError(t, err)

		idOrig = res.Cache.ID
		require.NotEmpty(t, res.Cache.ID)
	})

	t.Run("creating from a key", func(t *testing.T) {
		var res createWithKeyRes
		err := testutil.Query(
			`{
				cache {
					withKey(key: "ab") {
						id
					}
				}
			}`, &res, nil)
		require.NoError(t, err)

		idOrig = res.Cache.WithKey.ID
		require.NotEmpty(t, res.Cache.WithKey.ID)
	})

	t.Run("creating from a key", func(t *testing.T) {
		var res createWithKeyRes
		err := testutil.Query(
			`{
				cache {
					withKey(key: "ab") {
						id
					}
				}
			}`, &res, nil)
		require.NoError(t, err)

		idOrig = res.Cache.WithKey.ID
		require.NotEmpty(t, res.Cache.WithKey.ID)
	})

	t.Run("creating from same key again", func(t *testing.T) {
		var res createWithKeyRes
		err := testutil.Query(
			`{
				cache {
					withKey(key: "ab") {
						id
					}
				}
			}`, &res, nil)
		require.NoError(t, err)

		idSame = res.Cache.WithKey.ID
		require.NotEmpty(t, idSame)

		require.Equal(t, idOrig, idSame)
	})

	t.Run("creating from a different key", func(t *testing.T) {
		var res createWithKeyRes
		err := testutil.Query(
			`{
				cache {
					withKey(key: "ac") {
						id
					}
				}
			}`, &res, nil)
		require.NoError(t, err)

		idDiff = res.Cache.WithKey.ID
		require.NotEmpty(t, idDiff)

		require.NotEqual(t, idOrig, idDiff)
	})

	t.Run("creating from valid ID", func(t *testing.T) {
		var res createRes
		err := testutil.Query(
			`query Test($id: CacheID!) {
				cache(id: $id) {
					id
				}
			}`, &res, &testutil.QueryOptions{Variables: map[string]any{
				"id": idOrig,
			}})
		require.NoError(t, err)

		idGiven = res.Cache.ID
		require.Equal(t, idOrig, idGiven)
	})

	t.Run("creating from bogus ID", func(t *testing.T) {
		var res createRes
		err := testutil.Query(
			`query Test($id: CacheID!) {
				cache(id: $id) {
					id
				}
			}`, &res, &testutil.QueryOptions{Variables: map[string]any{
				"id": "bogus",
			}})
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid cache ID")
	})
}
