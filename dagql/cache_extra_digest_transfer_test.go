package dagql

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestRemoteCacheExtraDigestMetadata(t *testing.T) {
	for _, mode := range []string{"unmarked", "detached", "attached"} {
		t.Run(mode, func(t *testing.T) {
			ctx := cacheTestContext(t.Context())
			dbPath := filepath.Join(t.TempDir(), "cache.db")
			cache, err := NewCache(ctx, dbPath, nil, nil)
			require.NoError(t, err)
			ctx = ContextWithCache(ctx, cache)
			key := cacheTestIntCall("transfer-extra")
			originalRecipe, err := key.deriveRecipeDigest(cache)
			require.NoError(t, err)
			first := digest.FromString("original-identity")
			second := digest.FromString("later-content-identity")
			res, err := cache.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: key, IsPersistable: true}, func(ctx context.Context) (AnyResult, error) {
				value := cacheTestIntResult(key, 42).(Result[Int])
				if mode == "detached" {
					return value.WithContentDigest(ctx, first, call.ExtraDigestLabelRemoteCache, call.ExtraDigestLabelRemoteCache)
				}
				return value.WithContentDigest(ctx, first)
			})
			require.NoError(t, err)
			rowID := res.cacheSharedResult().id
			if mode == "attached" {
				res, err = res.(Result[Typed]).WithContentDigest(ctx, first, call.ExtraDigestLabelRemoteCache, call.ExtraDigestLabelRemoteCache)
				require.NoError(t, err)
			}
			require.Equal(t, rowID, res.cacheSharedResult().id)
			frame, err := res.ResultCall()
			require.NoError(t, err)
			wantExtra := call.ExtraDigest{Digest: first, Label: call.ExtraDigestLabelRemoteCache}
			markerCount := 0
			for _, extra := range frame.ExtraDigests {
				if extra == wantExtra {
					markerCount++
				}
			}
			if mode == "unmarked" {
				require.Zero(t, markerCount)
			} else {
				require.Equal(t, 1, markerCount)
			}
			// Content replacement preserves declarations about earlier digests.
			require.NoError(t, cache.TeachContentDigest(ctx, res, second))
			frame, err = res.ResultCall()
			require.NoError(t, err)
			require.Equal(t, second, frame.ContentDigest())
			encoded, err := json.Marshal(frame)
			require.NoError(t, err)
			var cloned ResultCall
			require.NoError(t, json.Unmarshal(encoded, &cloned))
			require.Equal(t, frame.ExtraDigests, cloned.ExtraDigests)
			gotRecipe, err := cloned.deriveRecipeDigest(cache)
			require.NoError(t, err)
			require.Equal(t, originalRecipe, gotRecipe)
			assertExtras := func(c *Cache, result AnyResult) {
				t.Helper()
				c.egraphMu.RLock()
				seen := map[call.ExtraDigest]struct{}{}
				for eqID := range c.outputEqClassesForResultLocked(result.cacheSharedResult().id) {
					for extra := range c.eqClassExtraDigests[c.findEqClassLocked(eqID)] {
						seen[extra] = struct{}{}
					}
				}
				c.egraphMu.RUnlock()
				require.Contains(t, seen, call.ExtraDigest{Digest: first, Label: call.ExtraDigestLabelContent})
				require.Contains(t, seen, call.ExtraDigest{Digest: second, Label: call.ExtraDigestLabelContent})
				if mode == "unmarked" {
					require.NotContains(t, seen, wantExtra)
				} else {
					require.Contains(t, seen, wantExtra)
				}
			}
			assertExtras(cache, res)
			cacheTestReleaseSession(t, cache, ctx)
			require.NoError(t, cache.Close(ctx))

			cache, err = NewCache(ctx, dbPath, nil, nil)
			require.NoError(t, err)
			ctx = ContextWithCache(ctx, cache)
			require.Equal(t, CachePersistenceResetNone, cache.PersistenceResetReason())
			res, err = cache.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: key, IsPersistable: true}, func(context.Context) (AnyResult, error) {
				return cacheTestIntResult(key, 99), nil
			})
			require.NoError(t, err)
			require.True(t, res.HitCache())
			require.Equal(t, 42, cacheTestUnwrapInt(t, res))
			assertExtras(cache, res)
			cacheTestReleaseSession(t, cache, ctx)
			require.NoError(t, cache.Close(ctx))
		})
	}
}
