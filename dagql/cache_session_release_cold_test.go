package dagql

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// testReleaseSessionEdges drops one live session's ownership of the given
// results with the per-result step ReleaseSession already runs
// (removeSessionResultLocked, then collectUnownedResultsLocked), and forgets
// the session's per-result records of them (its edge, install spans and lazy
// spans), which ReleaseSession drops only with the whole session. It is the
// spike for freeing whole cold results from a live session.
func (c *Cache) testReleaseSessionEdges(ctx context.Context, sessionID string, ids []sharedResultID) error {
	c.egraphMu.Lock()
	c.sessionMu.Lock()
	tracked := c.sessionResultIDsBySession[sessionID]
	owned := make([]sharedResultID, 0, len(ids))
	for _, id := range ids {
		if _, ok := tracked[id]; ok {
			delete(tracked, id)
			owned = append(owned, id)
		}
		delete(c.sessionResultInstallSpans[sessionID], id)
		delete(c.sessionLazySpansBySession[sessionID], id)
	}
	c.sessionMu.Unlock()
	var (
		queue []*sharedResult
		rerr  error
	)
	for _, id := range owned {
		var err error
		queue, err = c.removeSessionResultLocked(ctx, sessionID, id, len(owned), queue)
		if err != nil {
			rerr = err
		}
	}
	releases, err := c.collectUnownedResultsLocked(ctx, queue)
	c.egraphMu.Unlock()
	if err != nil {
		return err
	}
	if err := runOnReleaseFuncs(ctx, releases); err != nil {
		return err
	}
	return rerr
}

// TestCacheSessionReleaseColdResultEdges shows what releasing a live session's
// edges does with the existing collection path: a result that only the
// session owned is collected with the closure it alone kept, a dependency that
// a kept result still needs stays, and, after the class compaction the
// metadata prune already runs, the heap comes back. It also shows the hazard:
// a client still holding the collected result's handle gets an error, not a
// recompute, while the same call by recipe recomputes under a new number.
func TestCacheSessionReleaseColdResultEdges(t *testing.T) {
	const calls = 10000
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	const session = "test-session"
	value := strings.Repeat("x", 1024)

	var executed int
	call := func(i int) AnyResult {
		frame := cacheGrowthCall(i)
		res, err := c.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
			executed++
			return NewResultForCall(NewString(string([]byte(value))), frame)
		})
		require.NoError(t, err)
		return res
	}

	base := cacheGrowthHeap()
	results := make([]AnyResult, calls)
	for i := range calls {
		results[i] = call(i)
	}
	// Result 1 depends on result 0, and result 1 stays hot: result 0 must
	// survive the release of its own session edge.
	require.NoError(t, c.AddExplicitDependency(ctx, results[1], results[0], "test"))
	ids := make([]sharedResultID, calls)
	for i, res := range results {
		ids[i] = res.cacheSharedResult().id
	}
	results = nil
	full := cacheGrowthHeap()

	cold := append([]sharedResultID{ids[0]}, ids[2:]...)
	require.NoError(t, c.testReleaseSessionEdges(ctx, session, cold))
	released := cacheGrowthHeap()
	// The metadata prune compacts the e-graph's classes after it collects;
	// do the same.
	c.egraphMu.Lock()
	_, oldSlots, newSlots := c.compactEqClassesLocked(true)
	c.egraphMu.Unlock()
	compacted := cacheGrowthHeap()

	c.egraphMu.RLock()
	left := len(c.resultsByID)
	_, depKept := c.resultsByID[ids[0]]
	c.egraphMu.RUnlock()
	t.Logf("%d results: +%.1f MB held; after releasing %d cold session edges: %d results left, +%.1f MB; after class compaction (%d -> %d slots): +%.1f MB (%d B/result freed)",
		calls, float64(full-base)/1e6, len(cold), left, float64(int64(released)-int64(base))/1e6,
		oldSlots, newSlots, float64(int64(compacted)-int64(base))/1e6,
		(int64(full)-int64(compacted))/int64(len(cold)-1))
	require.Equal(t, 2, left, "the hot result and its dependency stay")
	require.True(t, depKept, "a dependency of a kept result stays")
	require.Less(t, int64(compacted)-int64(base), int64(full-base)/5, "most of the heap comes back")

	// The session still serves what it kept, by handle.
	kept, err := c.LoadResultByResultID(ctx, session, nil, uint64(ids[1]))
	require.NoError(t, err)
	require.Equal(t, value, string(kept.Unwrap().(String)))

	// A handle to a collected result is gone: no recompute through it.
	_, err = c.LoadResultByResultID(ctx, session, nil, uint64(ids[5]))
	require.ErrorContains(t, err, fmt.Sprintf("resolve result %d: missing shared result", ids[5]))

	// The same call by recipe recomputes, under a new number.
	before := executed
	again := call(5)
	require.Equal(t, before+1, executed)
	require.NotEqual(t, ids[5], again.cacheSharedResult().id)

	require.NoError(t, c.ReleaseSession(ctx, session))
	require.NoError(t, c.WaitSessionRelease(ctx, session))
	require.Empty(t, c.resultsByID)
}
