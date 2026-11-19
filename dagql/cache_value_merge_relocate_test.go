package dagql

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
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

// mergeTestMovingPayloadStream is mergeTestMovingStream with a root whose
// object payload names its child, so that the reference that moves is in
// the root's payload.
func mergeTestMovingPayloadStream(t *testing.T) (child, root ValueBundle) {
	t.Helper()
	ctx, a, srv := transferTestCache(t)
	c := persistedListTestResult(t, ctx, a, srv, "moved-payload-child", &transferTestValue{Text: "child"})
	r := persistedListTestResult(t, ctx, a, srv, "moved-payload-root", &transferTestValue{Text: "moved-payload-root", Child: rowNumber(c)})
	transferTestDependency(a, ctx, r, c)
	child = exportTestBundle(t, ctx, a, c)
	root = exportTestBundle(t, ctx, a, r)
	require.Len(t, root.Values, 2)
	return child, root
}

// mergeTestMovingStreams are the streams whose second bundle moves a
// record's references: in a list's items, and in an object's payload.
func mergeTestMovingStreams(t *testing.T) map[string][2]ValueBundle {
	t.Helper()
	listChild, listRoot := mergeTestMovingStream(t)
	payloadChild, payloadRoot := mergeTestMovingPayloadStream(t)
	return map[string][2]ValueBundle{"list": {listChild, listRoot}, "payload": {payloadChild, payloadRoot}}
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
	for name, stream := range mergeTestMovingStreams(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			want := mergeTestStream(t, func(c *Cache) { c.testNoPrerelocation = true }, stream[0], stream[1])
			require.Equal(t, want, mergeTestStream(t, nil, stream[0], stream[1]))
		})
	}
}

// A record relocated before the lock with a number the commit doesn't decide
// is relocated again under the lock, so the cache ends as if nothing had been
// relocated before it.
func TestMergeValuesRelocatesAgainWhenANumberChanged(t *testing.T) {
	t.Parallel()
	for name, stream := range mergeTestMovingStreams(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			want := mergeTestStream(t, func(c *Cache) { c.testNoPrerelocation = true }, stream[0], stream[1])
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
			}, stream[0], stream[1])
			require.Equal(t, want, got)
			require.Len(t, under, 1, "the record whose numbers no longer hold is relocated under the lock")
		})
	}
}

// An engine relocates a record whose references move to its final numbers
// as it prepares it, so the record's payload is decoded once in the merge,
// and not again to relocate it before the commit, nor under the lock.
func TestMergeValuesDecodesAMovedPayloadOnce(t *testing.T) {
	// Not parallel: the codec's step hook is the package's.
	child, root := mergeTestMovingPayloadStream(t)
	ctx, c, _ := transferTestCache(t)
	_, err := c.MergeValues(ctx, cloudCacheID, child)
	require.NoError(t, err)
	var mu sync.Mutex
	visits := 0
	steps := func(step string, v PersistedPayloadVisit) {
		if step == "visit" && bytes.Contains(v.Payload, []byte("moved-payload-root")) {
			mu.Lock()
			defer mu.Unlock()
			visits++
		}
	}
	transferTestCodecSteps.Store(&steps)
	defer transferTestCodecSteps.Store(nil)
	var moved, under []uint64
	c.testPrerelocated = func(used map[uint64]map[uint64]sharedResultID) {
		for ordinal := range used {
			moved = append(moved, ordinal)
		}
	}
	c.testRelocatedUnderLock = func(ordinal uint64) { under = append(under, ordinal) }
	reply, err := c.MergeValues(ctx, cloudCacheID, root)
	require.NoError(t, err)
	require.True(t, reply.Committed)
	require.Len(t, moved, 1, "the root's reference moves")
	require.Empty(t, under, "nothing is relocated under the lock")
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, visits, "the root's payload is visited once")
}

// A merge renumbers its records as it is prepared, outside its caller's
// lock, which on the Cloud is the remote cache service's work lock, and the
// commit uses them: here the root's record is relocated before the commit
// begins, and nothing is relocated under the graph lock.
func TestPreparedMergeRelocatesBeforeItsCommit(t *testing.T) {
	t.Parallel()
	child, root := mergeTestMovingStream(t)
	ctx, c, _ := transferTestCache(t)
	_, err := c.MergeValues(ctx, cloudCacheID, child)
	require.NoError(t, err)
	committing := false
	var relocatedWhileCommitting []bool
	var under []uint64
	c.testPrerelocated = func(used map[uint64]map[uint64]sharedResultID) {
		if len(used) > 0 {
			relocatedWhileCommitting = append(relocatedWhileCommitting, committing)
		}
	}
	c.testRelocatedUnderLock = func(ordinal uint64) { under = append(under, ordinal) }
	p, err := c.PrepareValueMerge(ctx, cloudCacheID, root)
	require.NoError(t, err)
	committing = true
	reply, err := c.CommitValueMerge(ctx, p)
	require.NoError(t, err)
	require.True(t, reply.Committed)
	require.Equal(t, []bool{false}, relocatedWhileCommitting, "relocated once, in the prepare")
	require.Empty(t, under, "no record is relocated under the graph lock")
}
