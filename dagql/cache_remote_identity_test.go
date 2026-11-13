package dagql

import (
	"fmt"
	"math/rand/v2"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// A call row whose identity the cache already holds teaches it nothing, so
// it costs what that identity holds, not what the cache built on it: teaching
// base again, when 2000 other calls take base as their input, allocates no
// more than when 10 do. The cost is counted in allocated bytes; the count is
// process-wide, so the test doesn't run in parallel. A teach that walked
// base's uses would allocate at least a term number for each, 16 KB for 2000;
// a run under the race detector misses its sync.Pool at random and allocates
// a few hundred bytes more or less, so that much is allowed.
func TestKnownRemoteIdentityCostIsIndependentOfItsUses(t *testing.T) {
	ctx := t.Context()
	bytes := func(uses int) uint64 {
		cloud := newCloudCache(t)
		a := newCloudApplier(t, cloud)
		base := holdingOf("base")
		base.ContentDigest = testDigest("base-content")
		a.call(callRow{key: HolderKey{"cache-a", 1}, session: "a", holding: base})
		for i := range uses {
			use := fmt.Sprint("use-", i)
			a.call(callRow{key: HolderKey{"cache-a", uint64(i + 2)}, session: "a", holding: RemoteHolding{
				Recipe: testDigest(use),
				Field:  "use",
				Term:   &RemoteTerm{Self: testDigest(use + "-self"), Inputs: []digest.Digest{base.Recipe}},
			}})
		}
		cloud.egraphMu.RLock()
		entry, _ := cloud.holdingLocked(HolderKey{"cache-a", 1})
		cloud.egraphMu.RUnlock()
		require.NotNil(t, entry)
		teach := func() {
			cloud.egraphMu.Lock()
			cloud.teachRemoteIdentityLocked(ctx, entry, base.Field, base.Recipe, base.Request, base.ContentDigest, base.Term)
			cloud.egraphMu.Unlock()
		}
		teach()
		return allocatedBytesPerRun(50, teach)
	}
	few, many := bytes(10), bytes(2000)
	t.Logf("a known row's teach allocates %d bytes with 10 uses of its class, %d with 2000", few, many)
	require.Less(t, many, few+1024, "a known row's teach grew with the calls that take its result as an input")
}

// allocatedBytesPerRun is testing.AllocsPerRun in bytes.
func allocatedBytesPerRun(runs int, f func()) uint64 {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	f()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range runs {
		f()
	}
	runtime.ReadMemStats(&after)
	return (after.TotalAlloc - before.TotalAlloc) / uint64(runs)
}

// Whenever the check finds a row's identity already taught, teaching it in
// full writes nothing: every structure of the e-graph is as it was. Random
// rows of a few engines, whose calls share recipes, requests, contents and
// term inputs, are applied with their sessions ending, entries collected and
// classes compacted between them; after each step every live holding's row
// is checked, and taught in full when the check passes.
func TestKnownRemoteIdentityWritesNothing(t *testing.T) {
	t.Parallel()
	for seed := range uint64(20) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			t.Parallel()
			checkKnownRemoteIdentityWritesNothing(t, seed)
		})
	}
}

func checkKnownRemoteIdentityWritesNothing(t *testing.T, seed uint64) {
	rng := rand.New(rand.NewPCG(seed, seed))
	// A blob store, as the service's cache has, so that classes compact.
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	a := newCloudApplier(t, cloud)
	pick := func(prefix string, n int) digest.Digest {
		return testDigest(fmt.Sprint(prefix, rng.IntN(n)))
	}
	// A call is one recipe; each engine reports it under its own number,
	// with the same request, content and term.
	type remoteCall struct{ holding RemoteHolding }
	calls := make([]remoteCall, 24)
	for i := range calls {
		recipe := testDigest(fmt.Sprint("recipe-", i))
		h := RemoteHolding{Recipe: recipe, Request: recipe, Field: "f", TypeName: "Directory"}
		if rng.IntN(3) == 0 {
			h.Request = pick("recipe-", len(calls))
		}
		if rng.IntN(2) == 0 {
			h.ContentDigest = pick("content-", 8)
		}
		if rng.IntN(5) != 0 {
			term := &RemoteTerm{Self: pick("self-", 4)}
			for range rng.IntN(3) {
				term.Inputs = append(term.Inputs, pick("recipe-", len(calls)))
			}
			h.Term = term
		}
		calls[i] = remoteCall{holding: h}
	}
	engines := []CacheID{"cache-a", "cache-b", "cache-c"}
	session := map[CacheID]int{}
	sessionOf := func(e CacheID) string { return fmt.Sprint(e, "-", session[e]) }
	descs := map[HolderKey]RemoteHolding{}

	known, checked := 0, 0
	checkAll := func() {
		cloud.egraphMu.Lock()
		defer cloud.egraphMu.Unlock()
		keys := make([]HolderKey, 0, len(descs))
		for key := range descs {
			keys = append(keys, key)
		}
		slices.SortFunc(keys, compareHolderKeys)
		for _, key := range keys {
			entry, h := cloud.holdingLocked(key)
			if h == nil {
				continue
			}
			desc := descs[key]
			checked++
			if !cloud.remoteIdentityTeachesNothingLocked(entry, desc.Recipe, desc.Request, desc.ContentDigest, desc.Term) {
				continue
			}
			known++
			before := egraphFactsForTest(cloud)
			cloud.writeRemoteIdentityLocked(ctx, entry, desc.Field, desc.Recipe, desc.Request, desc.ContentDigest, desc.Term)
			require.Equal(t, before, egraphFactsForTest(cloud), "teaching %v's known identity wrote", key)
		}
	}
	for step := range 400 {
		switch rng.IntN(20) {
		case 0:
			e := engines[rng.IntN(len(engines))]
			a.endSession(e, sessionOf(e))
			session[e]++
			a.collect()
		case 1:
			_, _, err := cloud.CompactEqClasses(ctx)
			require.NoError(t, err)
		default:
			e := engines[rng.IntN(len(engines))]
			c := rng.IntN(len(calls))
			key := HolderKey{e, uint64(c + 1)}
			descs[key] = calls[c].holding
			a.call(callRow{key: key, session: sessionOf(e), holding: calls[c].holding})
			if step%7 == 0 {
				a.collect()
			}
		}
		checkAll()
	}
	t.Logf("seed %d: %d of %d checked rows known", seed, known, checked)
	require.Greater(t, known, checked/4, "most rows of calls taught before are known")
}

// egraphFactsForTest prints every structure of the e-graph, with each
// class's slot given by its root: finding a root compresses paths, which no
// reader sees; and the marks the cache's writes have made. Requires egraphMu.
func egraphFactsForTest(c *Cache) string {
	var b strings.Builder
	line := func(name string, v any) { fmt.Fprintf(&b, "%s %v\n", name, v) }
	roots := make([]eqClassID, len(c.egraphParents))
	for i := range roots {
		roots[i] = c.eqClassRootLocked(eqClassID(i))
	}
	line("roots", roots)
	line("ranks", c.egraphRanks)
	line("free", c.freeEqClassIDs)
	line("digestToClass", c.egraphDigestToClass)
	line("classDigests", c.eqClassToDigests)
	// A class's empty set of extra digests is no set: every reader iterates
	// it or asks it for one digest, and merges and compaction carry only a
	// set that holds one. addResultExtraDigestsLocked makes an empty one for
	// a frame without extra digests.
	extras := make(map[eqClassID]map[call.ExtraDigest]struct{}, len(c.eqClassExtraDigests))
	for id, set := range c.eqClassExtraDigests {
		if len(set) > 0 {
			extras[id] = set
		}
	}
	line("classExtras", extras)
	line("inputTerms", c.inputEqClassToTerms)
	line("outputTerms", c.outputEqClassToTerms)
	line("resultClasses", c.resultOutputEqClasses)
	line("classResults", c.outputEqClassResults)
	line("termResults", c.termResults)
	line("resultTerms", c.resultTerms)
	line("provenance", c.termInputProvenance)
	terms := make(map[egraphTermID]egraphTerm, len(c.egraphTerms))
	for id, term := range c.egraphTerms {
		terms[id] = *term
	}
	line("terms", terms)
	byDigest := make(map[string][]egraphTermID, len(c.egraphTermsByTermDigest))
	for dig, set := range c.egraphTermsByTermDigest {
		byDigest[dig] = slices.Collect(set.Items())
	}
	line("termsByDigest", byDigest)
	postings := make(map[string][]sharedResultID, len(c.egraphResultsByDigest))
	for dig, posting := range c.egraphResultsByDigest {
		postings[dig] = slices.Sorted(posting.Items())
	}
	line("postings", postings)
	line("indexedDigests", c.resultIndexedDigests)
	line("broadlyIndexed", c.broadlyIndexedResults)
	line("nextTerm", c.nextEgraphTermID)
	// What the cache's writes have marked for its reader and its save since
	// the last write handed them back.
	if c.changes != nil {
		line("changes", fmt.Sprintf("%+v", *c.changes))
	}
	return b.String()
}
