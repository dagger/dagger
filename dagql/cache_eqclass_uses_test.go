package dagql

import (
	"context"
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// eqClassTestHolding is the i'th entry of a cache shaped as the Cloud's: its
// term takes the entries before it and at half its number as inputs; one in
// three has a content digest, which one in thirty shares with the entry
// before it, joining their classes; and one in ten was requested under
// another digest.
func eqClassTestHolding(i int) RemoteHolding {
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

// A collection when no class has ended since the last one changes no state
// engines share: over entries whose terms take each other as inputs, some
// joined by content and some requested under another digest, a collection
// right after the one that freed what an entry's attach ended leaves the
// e-graph, the entries and the free slots as they were.
func TestACollectionThatEndsNoClassChangesNothing(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	const entries = 300
	for i := range entries {
		_, err := cloud.AttachRemoteHolding(ctx, HolderKey{"cache-a", uint64(i + 1)}, eqClassTestHolding(i))
		require.NoError(t, err)
	}
	_, _, err := cloud.CollectRemoteHoldings(ctx, nil, nil)
	require.NoError(t, err)
	_, err = cloud.AttachRemoteHolding(ctx, HolderKey{"cache-a", entries + 1}, eqClassTestHolding(entries))
	require.NoError(t, err)
	_, _, err = cloud.CollectRemoteHoldings(ctx, nil, nil)
	require.NoError(t, err)

	before := sharedStateOf(cloud)
	_, _, err = cloud.CollectRemoteHoldings(ctx, nil, nil)
	require.NoError(t, err)
	require.Equal(t, before, sharedStateOf(cloud), "the collection left the e-graph as it was")
}

// BenchmarkCollectionThatEndsNothing measures a Cloud collection that ends
// no class, on caches of 43,000 entries and of 430,000, about a Cloud
// cache's serving several pull requests at once. Each entry is held by three
// engine caches of 165, so 1.29 million holdings at the larger size. Each
// operation adds an entry, whose new classes the collection looks at, and
// times a collection of nothing, which holds the cache's lock for that look
// alone. It reports the collections' mean and longest time, and fails when
// the mean is a millisecond or more: freeing costs what ended, and a walk of
// the smaller cache takes tens of milliseconds.
func BenchmarkCollectionThatEndsNothing(b *testing.B) {
	for _, entries := range []int{43_000, 430_000} {
		b.Run("entries="+strconv.Itoa(entries), func(b *testing.B) {
			ctx := context.Background()
			cloud, err := NewCache(ctx, "", nil, nil, WithBlobStore())
			require.NoError(b, err)
			b.Cleanup(func() { require.NoError(b, cloud.CloseDiscardingPersistence()) })
			attach := func(i int) {
				for k := range 3 {
					cache := CacheID(fmt.Sprint("cache-", (i+55*k)%165))
					_, err := cloud.AttachRemoteHolding(ctx, HolderKey{cache, uint64(i + 1)}, eqClassTestHolding(i))
					require.NoError(b, err)
				}
			}
			for i := range entries {
				attach(i)
			}
			_, _, err = cloud.CollectRemoteHoldings(ctx, nil, nil)
			require.NoError(b, err)
			cloud.egraphMu.RLock()
			slots, terms, holdings := cloud.eqClassSlotsLocked(), len(cloud.egraphTerms), 0
			for _, state := range cloud.remoteCaches {
				holdings += state.holdings
			}
			cloud.egraphMu.RUnlock()
			b.Logf("%d entries, %d terms, %d holdings, %d class slots", entries, terms, holdings, slots)

			var total, longest time.Duration
			n := 0
			for b.Loop() {
				attach(entries + n)
				n++
				start := time.Now()
				_, _, err := cloud.CollectRemoteHoldings(ctx, nil, nil)
				took := time.Since(start)
				require.NoError(b, err)
				total += took
				if took > longest {
					longest = took
				}
			}
			mean := total / time.Duration(n)
			b.Logf("%d collections: mean %v, longest %v", n, mean, longest)
			b.ReportMetric(float64(mean.Nanoseconds())/1e6, "collect-ms")
			b.ReportMetric(float64(longest.Nanoseconds())/1e6, "longest-collect-ms")
			require.Less(b, mean, time.Millisecond, "a collection that ends nothing walked the cache: %v a collection", mean)
		})
	}
}

// Freeing each class as it ends leaves the e-graph a compaction at every
// collection would leave, the way the cache freed classes before: after
// every step of random rows of three engines, with sessions ending, engines
// released and entries collected, a cache that frees the classes as they end
// and one that compacts them by a walk of every term and entry as each
// collection finishes hold the same digests in the same classes, the same
// extra digests, the same terms over the same inputs and outputs and the
// same entries' output classes, and as many class slots, and give the same
// hits for every recipe, content and term. The kept uses equal a walk's in
// both. The rows' calls share recipes, requests, contents and term selves,
// so unions are frequent, a union joins classes both used, a term may take
// one class in two positions, an entry may name two classes a union joins, a
// union makes terms congruent, and a dead class can come back before a
// collection.
func TestFreeingClassesAsTheyEndMatchesACompaction(t *testing.T) {
	t.Parallel()
	for seed := range uint64(30) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			t.Parallel()
			checkFreeingClassesAsTheyEndMatchesACompaction(t, seed)
		})
	}
}

// checkFreeingClassesAsTheyEndMatchesACompaction runs one seed's steps.
func checkFreeingClassesAsTheyEndMatchesACompaction(t *testing.T, seed uint64) {
	rng := rand.New(rand.NewPCG(seed, seed))
	type arm struct {
		ctx   context.Context
		cloud *Cache
		a     *cloudApplier
	}
	newArm := func(compact bool) *arm {
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		if compact {
			cloud.testFreeEndedEqClasses = func() { testCompactEqClassesLocked(cloud) }
		}
		return &arm{ctx: ctx, cloud: cloud, a: newCloudApplier(t, cloud)}
	}
	compacted, freed := newArm(true), newArm(false)
	arms := []*arm{compacted, freed}

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
			if rng.IntN(8) == 0 {
				// An input with no digest, which names no class.
				term.Inputs = append(term.Inputs, "")
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
	// classes reads each arm's classes by the digests they hold, which the
	// two name alike whatever numbers they gave them.
	classes := func(c *Cache) (eqClassContents, int) {
		c.egraphMu.Lock()
		defer c.egraphMu.Unlock()
		return testEqClassContentsLocked(c), c.eqClassSlotsLocked()
	}
	compare := func(step int) {
		t.Helper()
		for _, arm := range arms {
			require.NoError(t, arm.cloud.verifyEqClassUses(), "step %d", step)
		}
		wantClasses, wantSlots := classes(compacted.cloud)
		gotClasses, gotSlots := classes(freed.cloud)
		require.Equal(t, wantClasses, gotClasses, "step %d: the classes differ", step)
		require.Equal(t, wantSlots, gotSlots, "step %d: the class slots differ", step)
		require.Equal(t, hits(compacted.cloud), hits(freed.cloud), "step %d: the hits differ", step)
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
		case 5, 6, 7, 8, 9:
			// A collection with nothing to collect, as each read's end runs.
			for _, arm := range arms {
				arm.a.collect()
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
	freed.cloud.egraphMu.RLock()
	unions := freed.cloud.egraphUnions
	freed.cloud.egraphMu.RUnlock()
	t.Logf("seed %d: %d unions", seed, unions)
	require.Greater(t, unions, uint64(10), "the rows join classes")
}

// testCompactEqClassesLocked frees every class that no term and no entry
// uses, found by a walk, and every class a union joined into another, by
// rebuilding every table keyed by class from every live digest, term and
// entry, as the cache's compaction did before each class was freed as it
// ended. Live classes keep their numbers; the free slots past the highest
// live class are trimmed. Requires egraphMu for writing.
func testCompactEqClassesLocked(c *Cache) {
	if len(c.egraphParents) <= 1 {
		return
	}
	walked := c.walkEqClassUsesLocked()
	oldRoots := slices.Sorted(maps.Keys(walked))
	var highest eqClassID
	if len(oldRoots) > 0 {
		highest = oldRoots[len(oldRoots)-1]
	}
	newParents := make([]eqClassID, highest+1)
	newRanks := make([]uint8, highest+1)
	newUses := make([]uint32, highest+1)
	live := make(map[eqClassID]bool, len(oldRoots))
	for _, root := range oldRoots {
		live[root] = true
		newParents[root] = root
		newRanks[root] = c.egraphRanks[root]
	}
	var newFree []eqClassID
	for id := highest; id >= 1; id-- {
		if newParents[id] == 0 {
			newFree = append(newFree, id)
		}
	}
	newEqClassToDigests := make(map[eqClassID]*idSet[string], len(oldRoots))
	newEqClassExtraDigests := make(map[eqClassID][]call.ExtraDigest, len(oldRoots))
	newEgraphDigestToClass := make(map[string]eqClassID)
	for _, root := range oldRoots {
		if digests := c.eqClassToDigests[root]; digests.Size() > 0 {
			for dig := range digests.All() {
				newEgraphDigestToClass[dig] = root
			}
			newEqClassToDigests[root] = digests.Clone()
		}
		if extras := c.eqClassExtraDigests[root]; len(extras) > 0 {
			newEqClassExtraDigests[root] = slices.Clone(extras)
		}
	}
	var newInputEqClassToTerms, newOutputEqClassToTerms idSetIndex[eqClassID, egraphTermID]
	var newEgraphTermsByTermDigest idSetIndex[string, egraphTermID]
	add := func(index *idSetIndex[eqClassID, egraphTermID], id eqClassID, termID egraphTermID) {
		index.Insert(id, termID)
	}
	for termID, term := range c.egraphTerms {
		if term == nil {
			continue
		}
		inputs := make([]eqClassID, 0, len(term.inputEqIDs))
		for _, in := range term.inputEqIDs {
			if in == 0 {
				// An input with no digest keeps its position, as a repair
				// keeps it.
				inputs = append(inputs, 0)
				continue
			}
			root := c.findEqClassLocked(in)
			if !live[root] {
				continue
			}
			inputs = append(inputs, root)
			newUses[root]++
			add(&newInputEqClassToTerms, root, termID)
		}
		term.inputEqIDs = inputs
		output := c.findEqClassLocked(term.outputEqID)
		if !live[output] {
			continue
		}
		term.outputEqID = output
		newUses[output]++
		add(&newOutputEqClassToTerms, output, termID)
		term.termDigest = calcEgraphTermDigest(term.selfDigest, term.inputEqIDs)
		newEgraphTermsByTermDigest.Insert(term.termDigest, termID)
	}
	var newResultOutputEqClasses idSetIndex[sharedResultID, eqClassID]
	var newOutputEqClassResults idSetIndex[eqClassID, sharedResultID]
	for resID := range c.resultOutputEqClasses.Keys() {
		outputs := c.resultOutputEqClasses.Members(resID)
		roots := newIDSet[eqClassID]()
		for id := range outputs.All() {
			if root := c.findEqClassLocked(id); live[root] {
				roots.Insert(root)
			}
		}
		if roots.Size() == 0 {
			continue
		}
		for root := range roots.All() {
			newResultOutputEqClasses.Insert(resID, root)
			newUses[root]++
			newOutputEqClassResults.Insert(root, resID)
		}
	}
	if c.changes != nil {
		for id := range c.changes.left {
			if !live[id] {
				delete(c.changes.left, id)
			}
		}
		for id := range c.changes.leftNamed {
			if !live[id] {
				delete(c.changes.leftNamed, id)
			}
		}
	}
	c.egraphParents = newParents
	c.egraphRanks = newRanks
	c.egraphUses = newUses
	c.liveEqClasses = len(oldRoots)
	c.eqClassToDigests = newEqClassToDigests
	c.eqClassExtraDigests = newEqClassExtraDigests
	c.egraphDigestToClass = newEgraphDigestToClass
	c.inputEqClassToTerms = newInputEqClassToTerms
	c.outputEqClassToTerms = newOutputEqClassToTerms
	c.resultOutputEqClasses = newResultOutputEqClasses
	c.outputEqClassResults = newOutputEqClassResults
	c.egraphTermsByTermDigest = newEgraphTermsByTermDigest
	c.freeEqClassIDs = newFree
	c.forgetAllCallTermsLocked()
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
			id, handleString(res.description), hasValue, envelope, res.expiresAtUnix, res.run, res.runRan,
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
	for _, dig := range slices.Sorted(c.egraphResultsByDigest.Digests()) {
		fmt.Fprintf(&b, "posting %s: %v\n", dig, slices.Sorted(c.egraphResultsByDigest.Items(dig)))
	}
	for _, id := range slices.Sorted(maps.Keys(c.egraphTerms)) {
		term := c.egraphTerms[id]
		fmt.Fprintf(&b, "term %d: %s %v -> %d\n", id, term.selfDigest, term.inputEqIDs, term.outputEqID)
	}
	for _, id := range slices.Sorted(c.resultOutputEqClasses.Keys()) {
		fmt.Fprintf(&b, "outputs %d: %v\n", id, slices.Collect(c.resultOutputEqClasses.Items(id)))
	}
	for _, class := range slices.Sorted(c.outputEqClassResults.Keys()) {
		fmt.Fprintf(&b, "results %d: %v\n", class, slices.Collect(c.outputEqClassResults.Items(class)))
	}
	fmt.Fprintf(&b, "engine caches %v\n", slices.Sorted(maps.Keys(c.remoteCaches)))
	return b.String()
}
