package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// A cycle that returns to an existing entry through entries the merge
// doesn't change is still refused before anything changes. The Cloud keeps
// A -> C -> B with B expired; a new record of B depends on A. B is replaced,
// A is kept and C isn't in the bundle: B -> A -> C -> B.
func TestMergeGraphRefusesACycleThroughUnchangedEntries(t *testing.T) {
	t.Parallel()
	ctx, a, asrv := transferTestCache(t)
	pa := persistedListTestResult(t, ctx, a, asrv, "through-a", String("a"))
	pc := persistedListTestResult(t, ctx, a, asrv, "through-c", String("c"))
	pb := persistedListTestResult(t, ctx, a, asrv, "through-b", String("old-b"))
	require.NoError(t, a.AddExplicitDependency(ctx, pa, pc, "test"))
	require.NoError(t, a.AddExplicitDependency(ctx, pc, pb, "test"))
	mergeTestExpire(a, pb.cacheSharedResult())
	cctx, cloud := storedPartTestCache(t, WithBlobStore())
	_, err := cloud.MergeValues(cctx, "cache-a", exportTestBundle(t, ctx, a, pa))
	require.NoError(t, err)

	bctx, b, bsrv := transferTestCache(t)
	qa := persistedListTestResult(t, bctx, b, bsrv, "through-a", String("a"))
	qb := persistedListTestResult(t, bctx, b, bsrv, "through-b", String("new-b"))
	require.NoError(t, b.AddExplicitDependency(bctx, qb, qa, "test"))
	second := exportTestBundle(t, bctx, b, qb)

	before := mergeGraphTestState(t, cloud)
	reply, err := cloud.MergeValues(cctx, "cache-b", second)
	require.ErrorContains(t, err, "cycle")
	require.False(t, reply.Committed, "refused before the commit")
	require.Equal(t, before, mergeGraphTestState(t, cloud), "and the Cloud is unchanged")
}

// A cycle that closes through an offer a merge admits on an existing entry is
// refused before the commit, whether the offer's owner is an entry the merge
// creates or one the engine has. An error alone wouldn't show it: the
// commit's own attach refuses the closing offer too, but only after the
// commit has changed the cache.
func TestMergeGraphRefusesACycleThroughANewOffer(t *testing.T) {
	t.Parallel()
	t.Run("owner new", func(t *testing.T) {
		t.Parallel()
		// The engine keeps M -> K. The bundle creates N -> M and admits an
		// offer on K owned by N: K -> N -> M -> K.
		ctx, a, srv := transferTestCache(t)
		k := persistedListTestResult(t, ctx, a, srv, "new-owner-k", &transferTestValue{Text: "pending"})
		m := persistedListTestResult(t, ctx, a, srv, "new-owner-m", String("m"))
		n := persistedListTestResult(t, ctx, a, srv, "new-owner-n", String("n"))
		require.NoError(t, a.AddExplicitDependency(ctx, n, m, "test"))
		mergeTestOfferOn(t, ctx, a, k, "/sent", n)
		bundle := exportTestBundle(t, ctx, a, k)

		bctx, b, bsrv := transferTestCache(t)
		keptK := persistedListTestResult(t, bctx, b, bsrv, "new-owner-k", &transferTestValue{Text: "pending"})
		keptM := persistedListTestResult(t, bctx, b, bsrv, "new-owner-m", String("m"))
		require.NoError(t, b.AddExplicitDependency(bctx, keptM, keptK, "test"))

		before := mergeGraphTestState(t, b)
		reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
		require.ErrorContains(t, err, "cycle")
		require.False(t, reply.Committed, "refused before the commit")
		require.Equal(t, before, mergeGraphTestState(t, b), "and the engine is unchanged")
	})
	t.Run("owner existing", func(t *testing.T) {
		t.Parallel()
		// The engine keeps Y -> M -> X. The bundle admits an offer on X
		// owned by Y: X -> Y -> M -> X.
		ctx, a, srv := transferTestCache(t)
		x := persistedListTestResult(t, ctx, a, srv, "old-owner-x", &transferTestValue{Text: "pending"})
		y := persistedListTestResult(t, ctx, a, srv, "old-owner-y", String("y"))
		mergeTestOfferOn(t, ctx, a, x, "/sent", y)
		bundle := exportTestBundle(t, ctx, a, x)

		bctx, b, bsrv := transferTestCache(t)
		keptX := persistedListTestResult(t, bctx, b, bsrv, "old-owner-x", &transferTestValue{Text: "pending"})
		keptM := persistedListTestResult(t, bctx, b, bsrv, "old-owner-m", String("m"))
		keptY := persistedListTestResult(t, bctx, b, bsrv, "old-owner-y", String("y"))
		require.NoError(t, b.AddExplicitDependency(bctx, keptM, keptX, "test"))
		require.NoError(t, b.AddExplicitDependency(bctx, keptY, keptM, "test"))

		before := mergeGraphTestState(t, b)
		reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
		require.ErrorContains(t, err, "cycle")
		require.False(t, reply.Committed, "refused before the commit")
		require.Equal(t, before, mergeGraphTestState(t, b), "and the engine is unchanged")
	})
}

// A merge whose changed entries are all new searches only them: the stored
// closure below them can't lead back to an entry nothing stored points at.
// The Cloud stores a chain of 50 entries, each held by three engines; a
// fourth engine's bundle creates two entries on top of the chain's head.
func TestMergeGraphOfNewEntriesVisitsOnlyThem(t *testing.T) {
	t.Parallel()
	const length = 50
	chain := func(t *testing.T) (context.Context, *Cache, *Server, AnyResult) {
		ctx, c, srv := transferTestCache(t)
		var head AnyResult
		for i := range length {
			next := persistedListTestResult(t, ctx, c, srv, fmt.Sprintf("chain-%d", i), String(fmt.Sprint(i)))
			if head != nil {
				require.NoError(t, c.AddExplicitDependency(ctx, next, head, "test"))
			}
			head = next
		}
		return ctx, c, srv, head
	}
	cctx, cloud := storedPartTestCache(t, WithBlobStore())
	for _, engine := range []CacheID{"cache-a", "cache-b", "cache-c"} {
		ctx, c, _, head := chain(t)
		_, err := cloud.MergeValues(cctx, engine, exportTestBundle(t, ctx, c, head))
		require.NoError(t, err)
	}

	dctx, d, dsrv, head := chain(t)
	n1 := persistedListTestResult(t, dctx, d, dsrv, "on-top-1", String("1"))
	n2 := persistedListTestResult(t, dctx, d, dsrv, "on-top-2", String("2"))
	require.NoError(t, d.AddExplicitDependency(dctx, n1, head, "test"))
	require.NoError(t, d.AddExplicitDependency(dctx, n2, n1, "test"))
	bundle := exportTestBundle(t, dctx, d, n2)
	require.Len(t, bundle.Values, length+2)

	var (
		mu      sync.Mutex
		visited []sharedResultID
	)
	cloud.testMergeGraphVisited = func(id sharedResultID) {
		mu.Lock()
		defer mu.Unlock()
		visited = append(visited, id)
	}
	reply, err := cloud.MergeValues(cctx, "cache-d", bundle)
	require.NoError(t, err)
	var created []sharedResultID
	for _, value := range reply.Values {
		if value.Stored {
			created = append(created, sharedResultID(value.Number))
		}
	}
	require.Len(t, created, 2)
	mu.Lock()
	defer mu.Unlock()
	slices.Sort(visited)
	slices.Sort(created)
	require.Equal(t, created, visited, "the search visits the two new entries and none of the chain")
}

// mergeGraphTestState is what a refused merge must leave as it was: each
// entry's dependencies and offers, by entry.
func mergeGraphTestState(t *testing.T, c *Cache) string {
	t.Helper()
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	type entry struct {
		Deps   []sharedResultID
		Offers []string
		Value  json.RawMessage
	}
	state := map[sharedResultID]entry{}
	for id, res := range c.resultsByID {
		var e entry
		for dep := range res.deps {
			e.Deps = append(e.Deps, dep)
		}
		slices.Sort(e.Deps)
		for key, offer := range res.partOffersLocked() {
			e.Offers = append(e.Offers, fmt.Sprintf("%s:%v", key, offer.record.Owner.DependencyIDs))
		}
		slices.Sort(e.Offers)
		if env := res.persistedEnvelope; env != nil {
			data, err := json.Marshal(env)
			require.NoError(t, err)
			e.Value = data
		}
		state[id] = e
	}
	data, err := json.Marshal(state)
	require.NoError(t, err)
	return string(data)
}
