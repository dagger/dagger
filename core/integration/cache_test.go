package core

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.dagger.io/dagger/core"
	"go.dagger.io/dagger/internal/testutil"
)

func TestCacheVolume(t *testing.T) {
	t.Parallel()

	type createFromTokensRes struct {
		CacheFromTokens struct {
			ID core.CacheID
		}
	}

	type createRes struct {
		Cache struct {
			ID core.CacheID
		}
	}

	var idOrig, idSame, idDiff, idGiven core.CacheID

	t.Run("creating from tokens", func(t *testing.T) {
		var res createFromTokensRes
		err := testutil.Query(
			`{
				cacheFromTokens(tokens: ["a", "b"]) {
					id
				}
			}`, &res, nil)
		require.NoError(t, err)

		idOrig = res.CacheFromTokens.ID
		require.NotEmpty(t, res.CacheFromTokens.ID)
	})

	t.Run("creating from same tokens again", func(t *testing.T) {
		var res createFromTokensRes
		err := testutil.Query(
			`{
				cacheFromTokens(tokens: ["a", "b"]) {
					id
				}
			}`, &res, nil)
		require.NoError(t, err)

		idSame = res.CacheFromTokens.ID
		require.NotEmpty(t, idSame)

		require.Equal(t, idOrig, idSame)
	})

	t.Run("creating from different tokens", func(t *testing.T) {
		var res createFromTokensRes
		err := testutil.Query(
			`{
				cacheFromTokens(tokens: ["a", "c"]) {
					id
				}
			}`, &res, nil)
		require.NoError(t, err)

		idDiff = res.CacheFromTokens.ID
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
