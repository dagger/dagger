package dagql

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// alive reports whether the entry numbered number has a value in c.
func alive(c *Cache, number uint64) bool {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	res := c.resultsByID[sharedResultID(number)]
	return res != nil && !res.noValueLocked()
}

// A restore merges a part of a set file holding every entry the part lands
// on, from its commit until the restore releases it, so that a part's
// records a later part names as held rows are there for it. A root the part
// leaves unretained lives only through that hold, as a value nothing retains
// does on an engine: alive until the release, gone after it. A root merged
// as usual keeps its retention edge after the release.
func TestRestoreValuesHoldsWhatAPartLandsUntilReleased(t *testing.T) {
	t.Parallel()
	for _, unretained := range []bool{false, true} {
		t.Run(fmt.Sprint("unretained=", unretained), func(t *testing.T) {
			t.Parallel()
			actx, a, asrv := transferTestCache(t)
			r := persistedListTestResult(t, actx, a, asrv, "held-root", &transferTestValue{Text: "root"})
			p := persistedListTestResult(t, actx, a, asrv, "held-dep", &transferTestValue{Text: "dep"})
			transferTestDependency(a, actx, r, p)
			bundle := exportTestBundle(t, actx, a, r)
			var leave []TransferOrdinal
			if unretained {
				leave = []TransferOrdinal{bundle.Roots[0].Ordinal}
			}

			bctx, b, _ := transferTestCache(t)
			reply, err := b.RestoreValues(bctx, bundle, leave)
			require.NoError(t, err)
			require.NotNil(t, reply.Release, "a restore's merge holds what it lands")
			require.Len(t, reply.Roots, 1)
			require.Equal(t, !unretained, reply.Roots[0].Retained)
			number := reply.Roots[0].Number
			require.True(t, alive(b, number), "the root is held after the commit")

			require.NoError(t, reply.Release(bctx))
			require.Equal(t, !unretained, alive(b, number), "after the release, only a retained root stays")
			require.NoError(t, reply.Release(bctx), "a second release does nothing")
		})
	}
}

// A merge that isn't a restore's holds nothing for its caller.
func TestMergeValuesHoldsNothing(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	r := persistedListTestResult(t, actx, a, asrv, "plain-root", &transferTestValue{Text: "root"})
	bundle := exportTestBundle(t, actx, a, r)
	bctx, b, _ := transferTestCache(t)
	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	require.Nil(t, reply.Release)
}
