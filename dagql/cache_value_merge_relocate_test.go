package dagql

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// mergeTestMovingStream returns two bundles of one source cache: a child's,
// and a root's that holds the child too. Merged in that order, the root's
// child lands on the entry the first merge made, so the root's record has to
// be renumbered: its references move.
func mergeTestMovingStream(t *testing.T) (child, root ValueBundle) {
	t.Helper()
	ctx, a, srv := transferTestCache(t)
	c := persistedListTestResult(t, ctx, a, srv, "moved-child", String("child"))
	r := persistedListTestResult(t, ctx, a, srv, "moved-root", DynamicResultArrayOutput{Elem: String(""), Values: []AnyResult{c}})
	child = exportTestBundle(t, ctx, a, c)
	root = exportTestBundle(t, ctx, a, r)
	require.Len(t, root.Values, 2)
	return child, root
}

// mergeTestState is a cache's entries as merge leaves them: each entry's
// number, record and dependencies, in number order.
func mergeTestState(t *testing.T, c *Cache) []string {
	t.Helper()
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	var out []string
	for id, res := range c.resultsByID {
		var deps []uint64
		for dep := range res.deps {
			deps = append(deps, uint64(dep))
		}
		slices.Sort(deps)
		envelope, err := json.Marshal(res.persistedEnvelope)
		require.NoError(t, err)
		call, err := json.Marshal(res.loadResultCall())
		require.NoError(t, err)
		out = append(out, fmt.Sprintf("%d deps=%v call=%s envelope=%s", id, deps, call, envelope))
	}
	slices.Sort(out)
	return out
}

// mergeTestStream merges the bundles in turn into a new cache, set up by
// setup, and returns its state.
func mergeTestStream(t *testing.T, setup func(*Cache), bundles ...ValueBundle) []string {
	t.Helper()
	ctx, c, _ := transferTestCache(t)
	if setup != nil {
		setup(c)
	}
	for _, bundle := range bundles {
		reply, err := c.MergeValues(ctx, cloudCacheID, bundle)
		require.NoError(t, err)
		require.True(t, reply.Committed)
	}
	return mergeTestState(t, c)
}

// A merge renumbers its records before its commit takes the graph lock, so
// the commit, which holds the lock every check's calls wait on, doesn't
// decode and encode them again.
func TestMergeValuesRelocatesBeforeTheGraphLock(t *testing.T) {
	t.Parallel()
	child, root := mergeTestMovingStream(t)
	var under []uint64
	mergeTestStream(t, func(c *Cache) {
		c.testRelocatedUnderLock = func(ordinal uint64) { under = append(under, ordinal) }
	}, child, root)
	require.Empty(t, under, "no record is relocated under the graph lock")

	// Without the relocation before the lock, the root's record is
	// relocated under it: the stream does move a record.
	under = nil
	mergeTestStream(t, func(c *Cache) {
		c.testNoPrerelocation = true
		c.testRelocatedUnderLock = func(ordinal uint64) { under = append(under, ordinal) }
	}, child, root)
	require.Len(t, under, 1)
}

// Relocating before the lock leaves the cache as relocating under it does:
// the same entries, records and dependencies.
func TestMergeValuesRelocationBeforeTheLockLeavesTheSameCache(t *testing.T) {
	t.Parallel()
	child, root := mergeTestMovingStream(t)
	want := mergeTestStream(t, func(c *Cache) { c.testNoPrerelocation = true }, child, root)
	require.Equal(t, want, mergeTestStream(t, nil, child, root))
}

// A record relocated before the lock with a number the commit doesn't decide
// is relocated again under the lock, so the cache ends as if nothing had been
// relocated before it.
func TestMergeValuesRelocatesAgainWhenANumberChanged(t *testing.T) {
	t.Parallel()
	child, root := mergeTestMovingStream(t)
	want := mergeTestStream(t, func(c *Cache) { c.testNoPrerelocation = true }, child, root)
	var under []uint64
	got := mergeTestStream(t, func(c *Cache) {
		c.testPrerelocated = func(used map[uint64]map[uint64]sharedResultID) {
			for _, numbers := range used {
				for provisional := range numbers {
					numbers[provisional] += 1000
				}
			}
		}
		c.testRelocatedUnderLock = func(ordinal uint64) { under = append(under, ordinal) }
	}, child, root)
	require.Equal(t, want, got)
	require.Len(t, under, 1, "the record whose numbers no longer hold is relocated under the lock")
}
