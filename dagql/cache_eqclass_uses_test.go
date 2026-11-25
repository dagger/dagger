package dagql

import (
	"context"
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// eqClassCheckHolding is the i'th entry of a cache shaped as the Cloud's:
// its term takes the entries before it and at half its number as inputs;
// one in three has a content digest, which one in thirty shares with the
// entry before it, joining their classes; and one in ten was requested
// under another digest. Its classes leave fewer dead slots than live
// classes, so a compaction check frees nothing.
func eqClassCheckHolding(i int) RemoteHolding {
	h := holdingOf("check-entry-" + strconv.Itoa(i))
	if i > 0 {
		h.Term.Inputs = []digest.Digest{testDigest("check-entry-" + strconv.Itoa(i-1)), testDigest("check-entry-" + strconv.Itoa(i/2))}
	}
	switch {
	case i%30 == 0 && i > 0:
		h.ContentDigest = testDigest("check-content-" + strconv.Itoa(i-1))
	case i%3 == 0:
		h.ContentDigest = testDigest("check-content-" + strconv.Itoa(i))
	}
	if i%10 == 0 {
		h.Request = testDigest("check-request-" + strconv.Itoa(i))
	}
	return h
}

// A collection's check of the classes' compaction that frees nothing
// changes no state engines share: over entries whose terms take each other
// as inputs, some joined by content and some requested under another
// digest, with dead slots fewer than the live classes, a check made after
// the slots grew leaves the e-graph, the entries and the free slots as they
// were.
func TestEqClassCheckThatFreesNothingChangesNothing(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	advance := testEqClassClock(cloud)
	const entries = 300
	for i := range entries {
		_, err := cloud.AttachRemoteHolding(ctx, HolderKey{"cache-a", uint64(i + 1)}, eqClassCheckHolding(i))
		require.NoError(t, err)
	}
	_, _, err := cloud.CollectRemoteHoldings(ctx, nil, nil)
	require.NoError(t, err)
	_, err = cloud.AttachRemoteHolding(ctx, HolderKey{"cache-a", entries + 1}, eqClassCheckHolding(entries))
	require.NoError(t, err)
	advance(eqClassCheckInterval)

	cloud.egraphMu.Lock()
	slots, live := cloud.eqClassSlotsLocked(), testLiveEqClassesLocked(cloud)
	cloud.egraphMu.Unlock()
	require.Greater(t, slots, live, "the cache has dead slots")
	require.Less(t, slots, 2*live, "fewer than the live classes: the check frees nothing")

	checks, before := testEqClassChecks(cloud), sharedStateOf(cloud)
	_, _, err = cloud.CollectRemoteHoldings(ctx, nil, nil)
	require.NoError(t, err)
	require.Equal(t, checks+1, testEqClassChecks(cloud), "the collection checks the classes' compaction")
	require.Equal(t, before, sharedStateOf(cloud), "the check left the e-graph as it was")
}

// BenchmarkEqClassCheckThatFreesNothing measures the Cloud's check of the
// classes' compaction on caches of 43,000 entries and of 430,000, about a
// Cloud cache's serving several pull requests at once. Each entry is held
// by three engine caches of 165, so 1.29 million holdings at the larger
// size. The
// entries are eqClassCheckHolding's, whose classes leave fewer dead slots
// than live classes. Each operation adds an entry, which grows the slots,
// moves the checks' clock past their interval and times a collection of
// nothing, which holds the cache's lock for the check alone. It reports the
// checks' mean and longest time, and the compactions, which should be none.
// It fails when the checks' mean is a millisecond or more: a check that
// frees nothing has no reason to walk the cache, and a walk of the smaller
// cache takes tens of milliseconds.
func BenchmarkEqClassCheckThatFreesNothing(b *testing.B) {
	for _, entries := range []int{43_000, 430_000} {
		b.Run("entries="+strconv.Itoa(entries), func(b *testing.B) {
			ctx := context.Background()
			cloud, err := NewCache(ctx, "", nil, nil, WithBlobStore())
			require.NoError(b, err)
			b.Cleanup(func() { require.NoError(b, cloud.CloseDiscardingPersistence()) })
			attach := func(i int) {
				for k := range 3 {
					cache := CacheID(fmt.Sprint("cache-", (i+55*k)%165))
					_, err := cloud.AttachRemoteHolding(ctx, HolderKey{cache, uint64(i + 1)}, eqClassCheckHolding(i))
					require.NoError(b, err)
				}
			}
			for i := range entries {
				attach(i)
			}
			advance := testEqClassClock(cloud)
			_, _, err = cloud.CollectRemoteHoldings(ctx, nil, nil)
			require.NoError(b, err)
			cloud.egraphMu.RLock()
			checks, slots := cloud.eqClassChecks, cloud.eqClassSlotsLocked()
			terms, holdings := len(cloud.egraphTerms), 0
			for _, state := range cloud.remoteCaches {
				holdings += state.holdings
			}
			cloud.egraphMu.RUnlock()
			b.Logf("%d entries, %d terms, %d holdings, %d class slots", entries, terms, holdings, slots)

			var total, longest time.Duration
			compactions, n := 0, 0
			for b.Loop() {
				attach(entries + n)
				n++
				advance(eqClassCheckInterval)
				cloud.egraphMu.RLock()
				before := cloud.eqClassSlotsLocked()
				cloud.egraphMu.RUnlock()
				start := time.Now()
				_, _, err := cloud.CollectRemoteHoldings(ctx, nil, nil)
				took := time.Since(start)
				require.NoError(b, err)
				total += took
				if took > longest {
					longest = took
				}
				cloud.egraphMu.RLock()
				if cloud.eqClassSlotsLocked() < before {
					compactions++
				}
				cloud.egraphMu.RUnlock()
			}
			cloud.egraphMu.RLock()
			checks = cloud.eqClassChecks - checks
			cloud.egraphMu.RUnlock()
			require.Equal(b, n, checks, "each collection checks")
			mean := total / time.Duration(n)
			b.Logf("%d checks: mean %v, longest %v; %d compactions", n, mean, longest, compactions)
			b.ReportMetric(float64(mean.Nanoseconds())/1e6, "check-ms")
			b.ReportMetric(float64(longest.Nanoseconds())/1e6, "longest-check-ms")
			b.ReportMetric(float64(compactions), "compactions")
			require.Zero(b, compactions, "the checks free nothing")
			require.Less(b, mean, time.Millisecond, "a check that frees nothing walked the cache: %v a check", mean)
		})
	}
}

// Compaction decided from the uses the cache keeps leaves the e-graph as
// compaction decided from a walk of every term and entry did: after every
// step of random rows of three engines, with sessions ending, engines
// released, entries collected, the checks' clock moved past their interval
// and classes compacted by force between them, the two caches hold every
// digest in a class of the same number, the same free slots, the same terms
// with the same inputs and outputs, and the same entries' output classes;
// they have checked as often, and give the same hits for every recipe,
// content and term. The kept uses equal a walk's in both, and over all the
// seeds the checks compact as well as decline. The rows' calls share recipes, requests,
// contents and term selves, so unions are frequent, a union joins classes
// both used, a term may take one class in two positions, an entry may name
// two classes a union joins, and a union makes terms congruent.
func TestKeptClassUsesDecideAsAWalk(t *testing.T) {
	t.Parallel()
	var compacted, declined atomic.Int64
	t.Cleanup(func() {
		t.Logf("of the collections' checks, %d compacted and %d declined", compacted.Load(), declined.Load())
		require.Positive(t, compacted.Load(), "a check compacts")
		require.Positive(t, declined.Load(), "a check declines")
	})
	for seed := range uint64(30) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			t.Parallel()
			c, d := checkKeptClassUsesDecideAsAWalk(t, seed)
			compacted.Add(int64(c))
			declined.Add(int64(d))
		})
	}
}

// checkKeptClassUsesDecideAsAWalk runs one seed's steps, and returns how
// many of the collections' checks compacted and declined.
func checkKeptClassUsesDecideAsAWalk(t *testing.T, seed uint64) (compacted, declined int) {
	rng := rand.New(rand.NewPCG(seed, seed))
	type arm struct {
		ctx     context.Context
		cloud   *Cache
		a       *cloudApplier
		advance func(time.Duration)
	}
	newArm := func(walk bool) *arm {
		// A blob store, as the service's cache has, so that classes compact.
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		cloud.testWalkLiveClasses = walk
		return &arm{ctx: ctx, cloud: cloud, a: newCloudApplier(t, cloud), advance: testEqClassClock(cloud)}
	}
	walked, kept := newArm(true), newArm(false)
	arms := []*arm{walked, kept}

	const recipes = 30
	pick := func(prefix string, n int) digest.Digest { return testDigest(fmt.Sprint(prefix, rng.IntN(n))) }
	calls := make([]RemoteHolding, recipes)
	for i := range calls {
		recipe := testDigest(fmt.Sprint("recipe-", i))
		h := RemoteHolding{Recipe: recipe, Request: recipe, Field: "f", TypeName: "Directory"}
		if rng.IntN(4) == 0 {
			h.Request = pick("recipe-", recipes)
		}
		if rng.IntN(2) == 0 {
			h.ContentDigest = pick("content-", 6)
		}
		if rng.IntN(6) != 0 {
			term := &RemoteTerm{Self: pick("self-", 3)}
			for range rng.IntN(4) {
				term.Inputs = append(term.Inputs, pick("recipe-", recipes))
			}
			if len(term.Inputs) > 0 && rng.IntN(4) == 0 {
				term.Inputs = append(term.Inputs, term.Inputs[0])
			}
			h.Term = term
		}
		calls[i] = h
	}
	engines := []CacheID{"cache-a", "cache-b", "cache-c"}
	session := map[CacheID]int{}
	sessionOf := func(e CacheID) string { return fmt.Sprint(e, "-", session[e]) }

	hits := func(c *Cache) []string {
		var out []string
		sorted := func(n []uint64) []uint64 { return slices.Sorted(slices.Values(n)) }
		for i, h := range calls {
			out = append(out, fmt.Sprint("recipe ", i, sorted(c.EquivalentEntries(h.Recipe.String()))))
			if h.ContentDigest != "" {
				out = append(out, fmt.Sprint("content ", i, sorted(c.EquivalentEntries(h.ContentDigest.String()))))
			}
			if h.Term != nil {
				out = append(out, fmt.Sprint("term ", i, sorted(c.TermEntries(h.Term.Self, h.Term.Inputs))))
			}
		}
		return out
	}
	// classes reads each arm's classes by number, without compressing a
	// path: the walk compresses them, and the kept uses don't.
	classes := func(c *Cache) []string {
		c.egraphMu.RLock()
		defer c.egraphMu.RUnlock()
		var out []string
		for _, dig := range slices.Sorted(maps.Keys(c.egraphDigestToClass)) {
			out = append(out, fmt.Sprint("digest ", dig, " ", c.eqClassRootLocked(c.egraphDigestToClass[dig])))
		}
		roots := func(ids []eqClassID) []eqClassID {
			out := make([]eqClassID, len(ids))
			for i, id := range ids {
				out[i] = c.eqClassRootLocked(id)
			}
			return out
		}
		for _, id := range slices.Sorted(maps.Keys(c.egraphTerms)) {
			term := c.egraphTerms[id]
			out = append(out, fmt.Sprint("term ", id, " ", term.selfDigest, " ", roots(term.inputEqIDs), " ", c.eqClassRootLocked(term.outputEqID), " ", term.termDigest))
		}
		for _, id := range slices.Sorted(maps.Keys(c.resultOutputEqClasses)) {
			out = append(out, fmt.Sprint("outputs ", id, " ", slices.Sorted(slices.Values(roots(slices.Collect(c.resultOutputEqClasses[id].All()))))))
		}
		out = append(out, fmt.Sprint("slots ", c.eqClassSlotsLocked(), " free ", c.freeEqClassIDs, " checks ", c.eqClassChecks))
		return out
	}
	slotsOf := func(c *Cache) int {
		c.egraphMu.RLock()
		defer c.egraphMu.RUnlock()
		return c.eqClassSlotsLocked()
	}
	compare := func(step int) {
		t.Helper()
		for _, arm := range arms {
			require.NoError(t, arm.cloud.verifyEqClassUses(), "step %d", step)
		}
		require.Equal(t, classes(walked.cloud), classes(kept.cloud), "step %d: the classes differ", step)
		require.Equal(t, hits(walked.cloud), hits(kept.cloud), "step %d: the hits differ", step)
	}

	for step := range 400 {
		switch rng.IntN(30) {
		case 0, 1:
			e := engines[rng.IntN(len(engines))]
			for _, arm := range arms {
				arm.a.endSession(e, sessionOf(e))
				arm.a.collect()
			}
			session[e]++
		case 2, 3, 4:
			// An engine gone: every holding of its cache is a candidate.
			e := engines[rng.IntN(len(engines))]
			for _, arm := range arms {
				arm.a.ended[sessionOf(e)] = true
				arm.a.candidates = append(arm.a.candidates, arm.cloud.ReleaseRemoteCache(arm.ctx, e).Candidates...)
				arm.a.collect()
			}
			session[e]++
		case 5:
			for _, arm := range arms {
				_, _, err := arm.cloud.CompactEqClasses(arm.ctx)
				require.NoError(t, err)
			}
		case 6, 7, 8, 9:
			// The checks' interval passes: the next collection checks, if
			// something changed since the last check.
			checks, slots := testEqClassChecks(kept.cloud), slotsOf(kept.cloud)
			for _, arm := range arms {
				arm.advance(eqClassCheckInterval)
				arm.a.collect()
			}
			switch {
			case testEqClassChecks(kept.cloud) == checks:
			case slotsOf(kept.cloud) < slots:
				compacted++
			default:
				declined++
			}
		default:
			e := engines[rng.IntN(len(engines))]
			c := rng.IntN(len(calls))
			key := HolderKey{e, uint64(c + 1)}
			for _, arm := range arms {
				arm.a.call(callRow{key: key, session: sessionOf(e), holding: calls[c]})
				if step%9 == 0 {
					arm.a.collect()
				}
			}
		}
		compare(step)
	}
	kept.cloud.egraphMu.RLock()
	unions := kept.cloud.egraphUnions
	kept.cloud.egraphMu.RUnlock()
	t.Logf("seed %d: %d unions; of the collections' checks, %d compacted and %d declined", seed, unions, compacted, declined)
	require.Greater(t, unions, uint64(10), "the rows join classes")
	return compacted, declined
}

// sharedStateOf is what engines share of a blob-backed cache's state, read
// under its lock: its entries with their own facts, the recipes' index, the
// e-graph, its union-find's raw parents and ranks included, so that a path
// compressed shows, the stamps stored bundles read, and which engine caches
// have state. It leaves out what is an engine cache's own, its holdings and
// index, and what an entry keeps under locks of its own or in atomics: its
// holder list, ownership count and last use.
func sharedStateOf(c *Cache) string {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	var b strings.Builder
	for _, id := range slices.Sorted(maps.Keys(c.resultsByID)) {
		res := c.resultsByID[id]
		res.payloadMu.RLock()
		hasValue, envelope := res.hasValue, res.persistedEnvelope != nil
		res.payloadMu.RUnlock()
		var (
			taught []digest.Digest
			given  int
		)
		if t := res.taughtContent; t != nil {
			taught, given = t.digests, t.given
		}
		fmt.Fprintf(&b, "entry %d %s: value %v %v, expires %d, run %v %v, taught %v given %d, calls %v, recipes %v, stored %d, stamp %d %v, deps %v\n",
			id, res.description, hasValue, envelope, res.expiresAtUnix, res.run, res.runRan,
			taught, given, slices.Sorted(maps.Keys(res.keptCallsLocked())), res.recipeKeys,
			len(res.storedParts), res.changedAt, res.stampCause, res.deps)
	}
	for _, recipe := range slices.Sorted(maps.Keys(c.entriesByRecipe)) {
		fmt.Fprintf(&b, "recipe %s: %d\n", recipe, c.entriesByRecipe[recipe])
	}
	fmt.Fprintf(&b, "parents %v\nranks %v\nfree %v\nwrites %d\n", c.egraphParents, c.egraphRanks, c.freeEqClassIDs, c.writes)
	for _, dig := range slices.Sorted(maps.Keys(c.egraphDigestToClass)) {
		fmt.Fprintf(&b, "digest %s: class %d\n", dig, c.egraphDigestToClass[dig])
	}
	for _, class := range slices.Sorted(maps.Keys(c.eqClassToDigests)) {
		fmt.Fprintf(&b, "class %d: digests %v, extras %d\n", class, slices.Collect(c.eqClassToDigests[class].Items()), len(c.eqClassExtraDigests[class]))
	}
	for _, dig := range slices.Sorted(maps.Keys(c.egraphResultsByDigest)) {
		fmt.Fprintf(&b, "posting %s: %v\n", dig, slices.Sorted(c.egraphResultsByDigest[dig].Items()))
	}
	for _, id := range slices.Sorted(maps.Keys(c.egraphTerms)) {
		term := c.egraphTerms[id]
		fmt.Fprintf(&b, "term %d: %s %v -> %d\n", id, term.selfDigest, term.inputEqIDs, term.outputEqID)
	}
	for _, id := range slices.Sorted(maps.Keys(c.resultOutputEqClasses)) {
		fmt.Fprintf(&b, "outputs %d: %v\n", id, slices.Collect(c.resultOutputEqClasses[id].Items()))
	}
	for _, class := range slices.Sorted(maps.Keys(c.outputEqClassResults)) {
		fmt.Fprintf(&b, "results %d: %v\n", class, slices.Collect(c.outputEqClassResults[class].Items()))
	}
	fmt.Fprintf(&b, "engine caches %v\n", slices.Sorted(maps.Keys(c.remoteCaches)))
	return b.String()
}
