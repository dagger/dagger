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

// A result's content digest is kept once on its frame, whether it is set
// before publication or set again after it. A later content digest replaces
// it as the content digest, and the frame keeps the earlier one as a
// replaced content digest: both travel in an export, and survive the frame's
// encoding and a restart. The result's classes keep both.
func TestExtraDigestMetadata(t *testing.T) {
	for _, mode := range []string{"before publication", "again after publication"} {
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
				return value.WithContentDigest(ctx, first)
			})
			require.NoError(t, err)
			rowID := res.cacheSharedResult().id
			if mode == "again after publication" {
				res, err = res.(Result[Typed]).WithContentDigest(ctx, first)
				require.NoError(t, err)
			}
			require.Equal(t, rowID, res.cacheSharedResult().id)
			frame, err := res.ResultCall()
			require.NoError(t, err)
			wantExtra := call.ExtraDigest{Digest: first, Label: call.ExtraDigestLabelContent}
			count := 0
			for _, extra := range frame.ExtraDigests {
				if extra == wantExtra {
					count++
				}
			}
			require.Equal(t, 1, count, "the content digest is on the frame once")
			// Content replacement preserves declarations about earlier digests.
			require.NoError(t, cache.TeachContentDigest(ctx, res, second))
			frame, err = res.ResultCall()
			require.NoError(t, err)
			require.Equal(t, second, frame.ContentDigest())
			replacedExtra := call.ExtraDigest{Digest: first, Label: call.ExtraDigestLabelReplacedContent}
			require.Contains(t, frame.ExtraDigests, replacedExtra, "the frame keeps the replaced digest")
			// Both travel with the result.
			bundle := exportTestBundle(t, ctx, cache, res)
			require.Len(t, bundle.Values, 1)
			require.Subset(t, bundle.Values[0].Record.Call.ExtraDigests, []call.ExtraDigest{
				{Digest: second, Label: call.ExtraDigestLabelContent},
				replacedExtra,
			})
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
