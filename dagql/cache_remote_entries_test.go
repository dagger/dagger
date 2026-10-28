package dagql

import (
	"context"
	"fmt"
	"math/rand/v2"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func newCloudCache(t *testing.T) *Cache {
	t.Helper()
	cloud, err := NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cloud.CloseDiscardingPersistence()) })
	return cloud
}

func testDigest(name string) digest.Digest {
	return digest.FromString(name)
}

// callRow is one call span of an engine cache, as the service reads it.
type callRow struct {
	key      HolderKey
	session  string
	holding  RemoteHolding
	retained *RetentionObservation
}

// cloudApplier applies rows to a Cloud cache the way the service does: every
// operation records what it changes, and one collection pass follows each
// applied batch, over the candidates the batch produced.
type cloudApplier struct {
	t     *testing.T
	cloud *Cache
	ended map[string]bool

	candidates []HolderKey
}

func newCloudApplier(t *testing.T, cloud *Cache) *cloudApplier {
	return &cloudApplier{t: t, cloud: cloud, ended: map[string]bool{}}
}

// call applies a call row: attach, a hold only while its session is open,
// then the retention it reports.
func (a *cloudApplier) call(row callRow) {
	a.t.Helper()
	ctx := a.t.Context()
	change, err := a.cloud.AttachRemoteHolding(ctx, row.key, row.holding)
	require.NoError(a.t, err)
	a.candidates = append(a.candidates, change.Candidates...)
	if row.session != "" && !a.ended[row.session] {
		require.NoError(a.t, a.cloud.AddRemoteHold(ctx, row.key, row.session))
	}
	if row.retained != nil {
		candidates, err := a.cloud.ObserveRemoteRetention(ctx, row.key, *row.retained)
		require.NoError(a.t, err)
		a.candidates = append(a.candidates, candidates...)
	}
}

func (a *cloudApplier) observe(key HolderKey, obs RetentionObservation) {
	a.t.Helper()
	candidates, err := a.cloud.ObserveRemoteRetention(a.t.Context(), key, obs)
	require.NoError(a.t, err)
	a.candidates = append(a.candidates, candidates...)
}

func (a *cloudApplier) endSession(cache CacheID, session string) {
	a.ended[session] = true
	a.candidates = append(a.candidates, a.cloud.ReleaseRemoteSession(a.t.Context(), cache, session)...)
}

// collect ends the batch: one collection pass over its candidates.
func (a *cloudApplier) collect() []HolderKey {
	a.t.Helper()
	collected, err := a.cloud.CollectRemoteHoldings(a.t.Context(), a.candidates)
	require.NoError(a.t, err)
	a.candidates = nil
	return collected
}

func holdingOf(field string, deps ...uint64) RemoteHolding {
	return RemoteHolding{
		Recipe:   testDigest(field),
		Request:  testDigest(field),
		Field:    field,
		Term:     &RemoteTerm{Self: testDigest(field + "-self")},
		TypeName: "Directory",
		Deps:     deps,
	}
}

func retainedAt(generation uint64, engineTime int64, expiresAtUnix int64) *RetentionObservation {
	return &RetentionObservation{Retained: true, ExpiresAtUnix: expiresAtUnix, Generation: generation, EngineTimeUnixNano: engineTime}
}

func droppedAt(generation uint64, engineTime int64) RetentionObservation {
	return RetentionObservation{Generation: generation, EngineTimeUnixNano: engineTime}
}

func requireHolding(t *testing.T, cloud *Cache, key HolderKey) RemoteHoldingInfo {
	t.Helper()
	info, ok := cloud.RemoteEntryInfo(key)
	require.True(t, ok, "holding %v exists", key)
	for _, h := range info.Holdings {
		if h.Key == key {
			return h
		}
	}
	require.FailNow(t, "the entry lists its holding", "%v", key)
	return RemoteHoldingInfo{}
}

func requireNoHolding(t *testing.T, cloud *Cache, key HolderKey) {
	t.Helper()
	_, ok := cloud.RemoteEntryInfo(key)
	require.False(t, ok, "holding %v is gone", key)
}

// cloudIndexes returns the holding keys and recipe keys the cache resolves.
func cloudIndexes(cloud *Cache) ([]HolderKey, []digest.Digest) {
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	var keys []HolderKey
	for key := range cloud.holderEntries {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, compareHolderKeys)
	var recipes []digest.Digest
	for recipe := range cloud.entriesByRecipe {
		recipes = append(recipes, recipe)
	}
	slices.Sort(recipes)
	return keys, recipes
}

// A holding attaches to the entry of its stored recipe. A request digest that
// differs is taught as an equivalence; it keys the entry only when no entry
// has it yet.
func TestRemoteHoldingAttachesByStoredRecipe(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	engineA := CacheID("cache-a")
	engineB := CacheID("cache-b")

	// Engine A's Container.from: the request digest differs from the stored
	// recipe of the result.
	from := RemoteHolding{
		Recipe:        testDigest("from-stored"),
		Request:       testDigest("from-request"),
		Field:         "from",
		Term:          &RemoteTerm{Self: testDigest("from-self"), Inputs: []digest.Digest{testDigest("address")}},
		ContentDigest: testDigest("from-content"),
		TypeName:      "Container",
	}
	a.call(callRow{key: HolderKey{engineA, 7}, session: "s1", holding: from})
	// Engine B's result of the stored recipe itself attaches to the same
	// entry.
	a.call(callRow{key: HolderKey{engineB, 3}, session: "s2", holding: RemoteHolding{Recipe: testDigest("from-stored"), Request: testDigest("from-stored"), Field: "from", TypeName: "Container"}})
	require.Empty(t, a.collect())

	info, ok := cloud.RemoteEntryInfo(HolderKey{engineA, 7})
	require.True(t, ok)
	require.ElementsMatch(t, []digest.Digest{testDigest("from-request"), testDigest("from-stored")}, info.Recipes, "the request digest keys the entry: no entry had it")
	require.Len(t, info.Holdings, 2)
	require.Equal(t, HolderKey{engineA, 7}, info.Holdings[0].Key)
	require.Equal(t, HolderKey{engineB, 3}, info.Holdings[1].Key)
	require.Equal(t, testDigest("from-content"), info.Holdings[0].ContentDigest)
	for _, dig := range []digest.Digest{testDigest("from-stored"), testDigest("from-request"), testDigest("from-content")} {
		require.Contains(t, info.ClassDigests, dig.String(), "one class")
		require.Equal(t, []HolderKey{{engineA, 7}, {engineB, 3}}, cloud.EquivalentHolders(dig.String()))
	}
	require.Equal(t, []RemoteTerm{{Self: testDigest("from-self"), Inputs: []digest.Digest{testDigest("address")}}}, info.Terms)

	// A request digest another entry already has stays an equivalence only.
	a.call(callRow{key: HolderKey{engineA, 8}, session: "s1", holding: RemoteHolding{Recipe: testDigest("other-stored"), Request: testDigest("from-request"), Field: "from", TypeName: "Container"}})
	require.Empty(t, a.collect())
	other, ok := cloud.RemoteEntryInfo(HolderKey{engineA, 8})
	require.True(t, ok)
	require.Equal(t, []digest.Digest{testDigest("other-stored")}, other.Recipes)
	require.Len(t, other.Holdings, 1, "a separate entry: its stored recipe differs")
	require.Equal(t, []HolderKey{{engineA, 7}, {engineA, 8}, {engineB, 3}}, cloud.EquivalentHolders(testDigest("other-stored").String()), "taught equivalent through the request digest")
}

// Two holdings of one cache on one entry keep their own holds, retention,
// parts and dependencies. When one goes, its key no longer resolves while the
// other and the recipe index still do; when the last goes, nothing is left in
// either index.
func TestRemoteHoldingsShareAnEntry(t *testing.T) {
	t.Parallel()
	for _, together := range []bool{false, true} {
		t.Run("together="+strconv.FormatBool(together), func(t *testing.T) {
			t.Parallel()
			cloud := newCloudCache(t)
			a := newCloudApplier(t, cloud)
			engine := CacheID("cache-a")
			first, second, dep := HolderKey{engine, 4510}, HolderKey{engine, 7578}, HolderKey{engine, 20}

			host := holdingOf("host")
			withParts := holdingOf("host", dep.Number)
			withParts.Parts = []PersistedPartAddress{{Part: "fs"}}
			a.call(callRow{key: dep, session: "s1", holding: holdingOf("dep")})
			a.call(callRow{key: first, session: "s1", holding: withParts, retained: retainedAt(1, 10, 500)})
			if !together {
				// The first incarnation leaves before the second is computed.
				a.endSession(engine, "s1")
				a.observe(first, droppedAt(1, 20))
				require.Equal(t, []HolderKey{dep, first}, sortedKeys2(a.collect()))
				_, recipes := cloudIndexes(cloud)
				require.Empty(t, recipes, "the entry left with its last holding")
			}
			a.call(callRow{key: second, session: "s2", holding: host})
			require.Empty(t, a.collect())

			info, ok := cloud.RemoteEntryInfo(second)
			require.True(t, ok)
			if !together {
				require.Len(t, info.Holdings, 1)
				return
			}
			require.Len(t, info.Holdings, 2, "one entry, two holdings of one cache")
			h1, h2 := requireHolding(t, cloud, first), requireHolding(t, cloud, second)
			require.Equal(t, []string{"s1"}, h1.Sessions)
			require.Equal(t, []string{"s2"}, h2.Sessions)
			require.True(t, h1.Retained)
			require.Equal(t, int64(500), h1.RetentionExpiresAtUnix)
			require.False(t, h2.Retained)
			require.Equal(t, []PersistedPartAddress{{Part: "fs"}}, h1.Parts)
			require.Empty(t, h2.Parts)
			require.Equal(t, []uint64{dep.Number}, h1.Deps)
			require.Empty(t, h2.Deps)

			a.endSession(engine, "s2")
			require.Equal(t, []HolderKey{second}, a.collect())
			requireNoHolding(t, cloud, second)
			keys, recipes := cloudIndexes(cloud)
			require.Equal(t, []HolderKey{dep, first}, keys)
			require.Contains(t, recipes, testDigest("host"), "the other holding keeps the recipe")
			require.Equal(t, []HolderKey{first}, cloud.EquivalentHolders(testDigest("host").String()))

			a.endSession(engine, "s1")
			a.observe(first, droppedAt(1, 30))
			require.Equal(t, []HolderKey{dep, first}, sortedKeys2(a.collect()))
			keys, recipes = cloudIndexes(cloud)
			require.Empty(t, keys)
			require.Empty(t, recipes)
		})
	}
}

func sortedKeys2(keys []HolderKey) []HolderKey {
	out := slices.Clone(keys)
	slices.SortFunc(out, compareHolderKeys)
	return out
}

// Holding dependencies cascade collection between holdings only. An entry
// goes only when no holding and no other owner keeps it, and its own deps are
// never touched by holding collection.
func TestRemoteHoldingDependenciesCascade(t *testing.T) {
	t.Parallel()
	for _, terms := range []bool{true, false} {
		t.Run("terms="+strconv.FormatBool(terms), func(t *testing.T) {
			t.Parallel()
			testRemoteHoldingDependenciesCascade(t, terms)
		})
	}
}

// testRemoteHoldingDependenciesCascade runs the cascade with holdings that
// carry terms, or with none, as spans of calls that derived no structural
// identity report them.
func testRemoteHoldingDependenciesCascade(t *testing.T, terms bool) {
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	engine := CacheID("cache-a")
	root, mid, leaf := HolderKey{engine, 3}, HolderKey{engine, 2}, HolderKey{engine, 1}
	holding := func(field string, deps ...uint64) RemoteHolding {
		h := holdingOf(field, deps...)
		if !terms {
			h.Term = nil
		}
		return h
	}
	a.call(callRow{key: leaf, session: "s", holding: holding("leaf")})
	a.call(callRow{key: mid, session: "s", holding: holding("mid", leaf.Number)})
	a.call(callRow{key: root, session: "s", holding: holding("root", mid.Number), retained: retainedAt(1, 1, 0)})
	require.Empty(t, a.collect())

	// Another owner of the leaf's entry: an entry-level unit, as a stored
	// value is.
	cloud.egraphMu.Lock()
	leafEntry, _ := cloud.holdingLocked(leaf)
	cloud.incrementIncomingOwnershipLocked(t.Context(), leafEntry)
	cloud.egraphMu.Unlock()

	a.endSession(engine, "s")
	require.Empty(t, a.collect(), "the retained root owns its closure")
	require.Equal(t, 1, requireHolding(t, cloud, mid).Dependents)
	require.Equal(t, []HolderKey{leaf, mid, root}, cloud.HolderClosure(root))
	cloud.egraphMu.RLock()
	for _, key := range []HolderKey{root, mid, leaf} {
		entry, _ := cloud.holdingLocked(key)
		require.Empty(t, entry.deps, "holding dependencies never become entry deps")
	}
	cloud.egraphMu.RUnlock()

	a.observe(root, droppedAt(1, 2))
	require.Equal(t, []HolderKey{leaf, mid, root}, sortedKeys2(a.collect()), "the drop cascades through holdings")
	keys, _ := cloudIndexes(cloud)
	require.Empty(t, keys)
	cloud.egraphMu.RLock()
	_, stays := cloud.resultsByID[leafEntry.id]
	_, recipeStays := cloud.entriesByRecipe[testDigest("leaf")]
	cloud.egraphMu.RUnlock()
	require.True(t, stays, "the leaf's entry has another owner")
	require.True(t, recipeStays, "and keeps its recipe")
	require.Equal(t, []HolderKey(nil), cloud.EquivalentHolders(testDigest("leaf").String()), "with no holding left")

	cloud.egraphMu.Lock()
	queue, err := cloud.decrementIncomingOwnershipLocked(t.Context(), leafEntry, nil)
	require.NoError(t, err)
	_, err = cloud.collectUnownedResultsLocked(t.Context(), queue)
	require.NoError(t, err)
	_, stays = cloud.resultsByID[leafEntry.id]
	cloud.egraphMu.Unlock()
	require.NoError(t, err)
	require.False(t, stays)
}

// An entry that another unit owns outlives its last holding, with or without
// terms, while other entries leave around it. It keeps its entry dependency,
// its recipe keys, its postings and its class membership. The collection that
// takes those holdings leaves more dead classes than live ones and compacts
// them: the entry keeps all of that through the renumbering, and a holding
// that stays keeps its number, recipes, postings, classes and terms. When the
// unit goes, the entry and its entry dependency go, and only then, with no
// entry left, does the e-graph reset.
func TestRemoteEntryOutlivesItsHoldings(t *testing.T) {
	t.Parallel()
	for _, terms := range []bool{true, false} {
		t.Run("terms="+strconv.FormatBool(terms), func(t *testing.T) {
			t.Parallel()
			cloud := newCloudCache(t)
			advance := testEqClassClock(cloud)
			a := newCloudApplier(t, cloud)
			key, scratch, dep := HolderKey{"cache-a", 1}, HolderKey{"cache-a", 2}, HolderKey{"cache-a", 3}
			kept := holdingOf("kept")
			kept.Request = testDigest("kept-alias")
			kept.ContentDigest = testDigest("kept-content")
			if !terms {
				kept.Term = nil
			}
			a.call(callRow{key: key, session: "s", holding: kept})
			for k, field := range map[HolderKey]string{scratch: "scratch", dep: "entry-dep"} {
				holding := holdingOf(field)
				if !terms {
					holding.Term = nil
				}
				a.call(callRow{key: k, session: "s", holding: holding})
			}
			// A holding of another engine cache that stays, whose term takes the
			// kept entry as its input.
			stays := HolderKey{"cache-b", 1}
			live := holdingOf("live")
			live.Request = testDigest("live-alias")
			live.ContentDigest = testDigest("live-content")
			if terms {
				live.Term.Inputs = []digest.Digest{kept.Recipe}
			} else {
				live.Term = nil
			}
			a.call(callRow{key: stays, session: "stays", holding: live})
			require.Empty(t, a.collect())

			// Another unit owns the kept entry, as a stored value will, and the
			// entry depends on another entry, as a stored value's references do.
			cloud.egraphMu.Lock()
			entry, _ := cloud.holdingLocked(key)
			depEntry, _ := cloud.holdingLocked(dep)
			cloud.incrementIncomingOwnershipLocked(t.Context(), entry)
			err := cloud.addExplicitDependencyLocked(t.Context(), entry, depEntry, "test")
			cloud.egraphMu.Unlock()
			require.NoError(t, err)

			// Churn from a third engine cache leaves with them: more dead
			// classes than live ones.
			var churn []HolderKey
			for i := range 40 {
				k := HolderKey{"cache-c", uint64(i + 1)}
				holding := holdingOf(fmt.Sprintf("churn-%d", i))
				holding.ContentDigest = testDigest(fmt.Sprintf("churn-%d-content", i))
				if !terms {
					holding.Term = nil
				}
				a.call(callRow{key: k, session: "churn", holding: holding})
				churn = append(churn, k)
			}
			a.endSession("cache-c", "churn")
			stayed, ok := cloud.RemoteEntryInfo(stays)
			require.True(t, ok)
			cloud.egraphMu.Lock()
			slotsBefore, checksBefore := cloud.eqClassSlotsLocked(), cloud.eqClassChecks
			cloud.egraphMu.Unlock()

			// Every holding leaves, and the scratch entry with its holding, in
			// one pass that compacts the classes, an interval after the last
			// check.
			advance(eqClassCheckInterval)
			a.endSession("cache-a", "s")
			require.Equal(t, append([]HolderKey{key, scratch, dep}, churn...), a.collect())
			cloud.egraphMu.Lock()
			slotsAfter, checksAfter, liveClasses := cloud.eqClassSlotsLocked(), cloud.eqClassChecks, testLiveEqClassesLocked(cloud)
			cloud.egraphMu.Unlock()
			require.Equal(t, checksBefore+1, checksAfter, "the collection checked the classes")
			require.Less(t, slotsAfter, slotsBefore, "and compacted them")
			require.Equal(t, liveClasses, slotsAfter, "to the live classes")
			after, ok := cloud.RemoteEntryInfo(stays)
			require.True(t, ok, "the holding that stays resolves by its number")
			require.Equal(t, stayed, after, "with its recipes, postings, classes and terms")
			cloud.egraphMu.RLock()
			stayEntry, _ := cloud.holdingLocked(stays)
			stayKeyed := cloud.entriesByRecipe[live.Recipe] == stayEntry.id && cloud.entriesByRecipe[live.Request] == stayEntry.id
			_, stayClassed := cloud.outputEqClassResults[cloud.eqClassRootLocked(cloud.egraphDigestToClass[live.ContentDigest.String()])][stayEntry.id]
			cloud.egraphMu.RUnlock()
			require.True(t, stayKeyed, "by its recipe and alias")
			require.True(t, stayClassed, "and by its content")
			require.Equal(t, []HolderKey{stays}, cloud.EquivalentHolders(live.ContentDigest.String()))

			cloud.egraphMu.RLock()
			sameEntry := cloud.resultsByID[entry.id] == entry
			sameDep := cloud.resultsByID[depEntry.id] == depEntry
			_, depEdge := entry.deps[depEntry.id]
			owners, depOwners := entry.incomingOwnershipCount.Load(), depEntry.incomingOwnershipCount.Load()
			postings := slices.Clone(cloud.resultIndexedDigests[entry.id])
			keyed := map[digest.Digest]bool{}
			classed := map[digest.Digest]bool{}
			for _, dig := range []digest.Digest{kept.Recipe, kept.Request, kept.ContentDigest} {
				keyed[dig] = cloud.entriesByRecipe[dig] == entry.id
				class := cloud.eqClassRootLocked(cloud.egraphDigestToClass[dig.String()])
				_, classed[dig] = cloud.outputEqClassResults[class][entry.id]
			}
			holdings := len(entry.holders)
			for k := range cloud.holderEntries {
				if k.Cache == key.Cache {
					holdings++
				}
			}
			if _, ok := cloud.remoteCaches[key.Cache]; ok {
				holdings++
			}
			cloud.egraphMu.RUnlock()
			require.True(t, sameEntry, "another unit keeps the entry")
			require.True(t, sameDep, "the entry keeps its dependency")
			require.True(t, depEdge)
			require.EqualValues(t, 1, owners)
			require.EqualValues(t, 1, depOwners)
			require.Zero(t, holdings)
			for _, dig := range []digest.Digest{kept.Recipe, kept.Request, kept.ContentDigest} {
				require.Contains(t, postings, dig.String())
				require.True(t, classed[dig], "the entry stays in the class of %s", dig)
			}
			require.True(t, keyed[kept.Recipe])
			require.True(t, keyed[kept.Request])
			require.Empty(t, cloud.EquivalentHolders(kept.Recipe.String()), "with no holding left")

			a.endSession("cache-b", "stays")
			require.Equal(t, []HolderKey{stays}, a.collect())
			cloud.egraphMu.Lock()
			queue, err := cloud.decrementIncomingOwnershipLocked(t.Context(), entry, nil)
			require.NoError(t, err)
			_, err = cloud.collectUnownedResultsLocked(t.Context(), queue)
			require.NoError(t, err)
			remaining := len(cloud.resultsByID) + len(cloud.entriesByRecipe) + len(cloud.egraphDigestToClass)
			cloud.egraphMu.Unlock()
			require.Zero(t, remaining, "with no entry left, the e-graph reset")
		})
	}
}

// Holds are a set per (session, holding): holding twice under one session
// releases at once, and each session releases only its own hold.
func TestRemoteHoldsAreASet(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	engine := CacheID("cache-a")
	key := HolderKey{engine, 1}
	a.call(callRow{key: key, session: "s1", holding: holdingOf("x")})
	a.call(callRow{key: key, session: "s1", holding: holdingOf("x")})
	a.call(callRow{key: key, session: "s2", holding: holdingOf("x")})
	require.Empty(t, a.collect())
	require.Equal(t, []string{"s1", "s2"}, requireHolding(t, cloud, key).Sessions)

	a.endSession(engine, "s1")
	require.Empty(t, a.collect())
	require.Equal(t, []string{"s2"}, requireHolding(t, cloud, key).Sessions)
	a.endSession(engine, "s2")
	require.Equal(t, []HolderKey{key}, a.collect())
	// Releasing again changes nothing.
	a.endSession(engine, "s2")
	require.Empty(t, a.collect())
}

type retentionEvent struct {
	set     bool
	expires int64
}

// engineRetention replays an engine's history of sets and drops of one
// retention edge, as dagql does it: a set on a missing edge creates it with
// its candidate expiry, a set on an existing edge keeps the earlier non-zero
// expiry, and a drop deletes the edge. Each set is observed by its span after
// the change; each drop at its own time.
func engineRetention(history []retentionEvent, times []obsTime) (bool, int64, []RetentionObservation) {
	var (
		edge    bool
		expires int64
		obs     []RetentionObservation
	)
	for i, ev := range history {
		at := times[i]
		switch {
		case ev.set:
			if edge {
				expires = mergeSharedResultExpiryUnix(expires, ev.expires)
			} else {
				edge, expires = true, ev.expires
			}
			obs = append(obs, RetentionObservation{Retained: true, ExpiresAtUnix: expires, Generation: at.generation, EngineTimeUnixNano: at.engineTime})
		case edge:
			edge, expires = false, 0
			obs = append(obs, RetentionObservation{Generation: at.generation, EngineTimeUnixNano: at.engineTime})
		}
	}
	return edge, expires, obs
}

func permutations[T any](items []T, yield func([]T)) {
	var rec func(int)
	rec = func(k int) {
		if k == len(items) {
			yield(items)
			return
		}
		for i := k; i < len(items); i++ {
			items[k], items[i] = items[i], items[k]
			rec(k + 1)
			items[k], items[i] = items[i], items[k]
		}
	}
	rec(0)
}

// Retention is one register per holding: in every arrival order of an
// engine's sets, drops and re-creations, the holding ends where the engine's
// edge ended. The histories are those of retention_register.py: the review's
// two, and random ones over expiries 0, 100, 200 and 300; every order up to
// six observations, a sample above that. A further set of histories crosses a
// generation whose clock runs behind the previous one's.
func TestRemoteRetentionEveryArrivalOrder(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	engine := CacheID("cache-a")
	sequential := func(n int) []obsTime {
		times := make([]obsTime, n)
		for i := range times {
			times[i] = obsTime{generation: 1, engineTime: int64(i + 1)}
		}
		return times
	}
	type history struct {
		events []retentionEvent
		times  []obsTime
	}
	histories := []history{
		{events: []retentionEvent{{set: true, expires: 100}, {}, {set: true, expires: 200}}},
		{events: []retentionEvent{{set: true, expires: 100}, {}, {set: true, expires: 200}, {}}},
		// A later generation's clock is behind: its observations still win.
		{
			events: []retentionEvent{{set: true, expires: 100}, {}, {set: true, expires: 300}},
			times:  []obsTime{{1, 1000}, {2, 5}, {2, 6}},
		},
		{
			events: []retentionEvent{{set: true, expires: 100}, {set: true, expires: 50}, {}},
			times:  []obsTime{{1, 1000}, {1, 2000}, {2, 1}},
		},
	}
	rng := rand.New(rand.NewPCG(14300, 0))
	for range 2000 {
		n := 1 + rng.IntN(8)
		events := make([]retentionEvent, n)
		for i := range events {
			if rng.Float64() < 0.6 {
				events[i] = retentionEvent{set: true, expires: []int64{0, 100, 200, 300}[rng.IntN(4)]}
			}
		}
		histories = append(histories, history{events: events})
	}

	cloud := newCloudCache(t)
	var (
		number uint64
		orders int
	)
	check := func(wantRetained bool, wantExpires int64, order []RetentionObservation) {
		number++
		key := HolderKey{engine, number}
		session := "s" + strconv.FormatUint(number, 10)
		_, err := cloud.AttachRemoteHolding(ctx, key, holdingOf("retention"))
		require.NoError(t, err)
		// The session hold keeps the holding while the observations apply.
		require.NoError(t, cloud.AddRemoteHold(ctx, key, session))
		for _, obs := range order {
			_, err := cloud.ObserveRemoteRetention(ctx, key, obs)
			require.NoError(t, err)
		}
		got := requireHolding(t, cloud, key)
		require.Equal(t, wantRetained, got.Retained, "order %v", order)
		require.Equal(t, wantExpires, got.RetentionExpiresAtUnix, "order %v", order)
		orders++
		// A drop of a later generation, then the session's end, collect it.
		candidates, err := cloud.ObserveRemoteRetention(ctx, key, droppedAt(100, 0))
		require.NoError(t, err)
		collected, err := cloud.CollectRemoteHoldings(ctx, append(candidates, cloud.ReleaseRemoteSession(ctx, engine, session)...))
		require.NoError(t, err)
		require.Equal(t, []HolderKey{key}, collected)
	}
	for _, h := range histories {
		times := h.times
		if times == nil {
			times = sequential(len(h.events))
		}
		retained, expires, obs := engineRetention(h.events, times)
		if len(obs) <= 6 {
			permutations(obs, func(order []RetentionObservation) { check(retained, expires, order) })
			continue
		}
		for range 500 {
			order := slices.Clone(obs)
			rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
			check(retained, expires, order)
		}
	}
	t.Logf("%d arrival orders", orders)
}

// Observations at equal times, which need a coarse clock, resolve the same in
// either order: a set beats a drop, and between two sets the earlier non-zero
// expiry wins.
func TestRemoteRetentionTies(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cloud := newCloudCache(t)
	set := func(expires int64) RetentionObservation { return *retainedAt(1, 5, expires) }
	for i, tc := range []struct {
		obs          []RetentionObservation
		wantRetained bool
		wantExpires  int64
	}{
		{obs: []RetentionObservation{set(100), droppedAt(1, 5)}, wantRetained: true, wantExpires: 100},
		{obs: []RetentionObservation{set(200), set(100)}, wantRetained: true, wantExpires: 100},
		{obs: []RetentionObservation{set(0), set(100)}, wantRetained: true, wantExpires: 100},
		{obs: []RetentionObservation{droppedAt(1, 5), droppedAt(1, 5)}},
	} {
		for _, order := range [][]RetentionObservation{tc.obs, {tc.obs[1], tc.obs[0]}} {
			key := HolderKey{"cache-a", uint64(i + 1)}
			_, err := cloud.AttachRemoteHolding(ctx, key, holdingOf("tie"))
			require.NoError(t, err)
			require.NoError(t, cloud.AddRemoteHold(ctx, key, "s"))
			for _, obs := range order {
				_, err := cloud.ObserveRemoteRetention(ctx, key, obs)
				require.NoError(t, err)
			}
			got := requireHolding(t, cloud, key)
			require.Equal(t, tc.wantRetained, got.Retained, "case %d order %v", i, order)
			require.Equal(t, tc.wantExpires, got.RetentionExpiresAtUnix, "case %d order %v", i, order)
			collected, err := cloud.CollectRemoteHoldings(ctx, append(cloud.ReleaseRemoteCache(ctx, "cache-a"), key))
			require.NoError(t, err)
			require.Equal(t, []HolderKey{key}, collected)
		}
	}
}

// A session's end releases its holds; the pass after it collects every
// holding left with no owner, and keeps what retention and dependencies own.
func TestRemoteSessionEndCollects(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	engine := CacheID("cache-a")
	scratch, root, dir := HolderKey{engine, 1}, HolderKey{engine, 2}, HolderKey{engine, 3}
	a.call(callRow{key: scratch, session: "s", holding: holdingOf("scratch")})
	a.call(callRow{key: dir, session: "s", holding: holdingOf("dir")})
	a.call(callRow{key: root, session: "s", holding: holdingOf("root", dir.Number), retained: retainedAt(1, 1, 0)})
	require.Empty(t, a.collect())

	a.endSession(engine, "s")
	require.Equal(t, []HolderKey{scratch}, a.collect())
	require.Empty(t, requireHolding(t, cloud, root).Sessions)
	require.Equal(t, 1, requireHolding(t, cloud, dir).Dependents, "the retained root owns the directory")
}

// A late call row of an ended session creates its holding with no hold; the
// pass after its batch collects it unless its retention or a dependent owns
// it.
func TestRemoteLateRowsOfEndedSession(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	engine := CacheID("cache-a")
	a.call(callRow{key: HolderKey{engine, 1}, session: "s", holding: holdingOf("first")})
	a.endSession(engine, "s")
	require.Equal(t, []HolderKey{{engine, 1}}, a.collect())

	unretained, retained, dep := HolderKey{engine, 2}, HolderKey{engine, 3}, HolderKey{engine, 4}
	a.call(callRow{key: unretained, session: "s", holding: holdingOf("late-unretained")})
	a.call(callRow{key: dep, session: "s", holding: holdingOf("late-dep")})
	a.call(callRow{key: retained, session: "s", holding: holdingOf("late-retained", dep.Number), retained: retainedAt(1, 5, 0)})
	require.Equal(t, []HolderKey{unretained}, a.collect())
	require.Empty(t, requireHolding(t, cloud, retained).Sessions, "an ended session gains no hold")
	require.True(t, requireHolding(t, cloud, retained).Retained)
	require.Equal(t, 1, requireHolding(t, cloud, dep).Dependents)
}

// A dependency named before its holding exists waits as an unknown number;
// creating the holding attaches it. Either order gives the same state.
func TestRemoteDependencyEitherOrder(t *testing.T) {
	t.Parallel()
	engine := CacheID("cache-a")
	parent, dep := HolderKey{engine, 10}, HolderKey{engine, 9}
	rows := []callRow{
		{key: parent, session: "s", holding: holdingOf("parent", dep.Number, 99)},
		{key: dep, session: "s", holding: holdingOf("dep")},
	}
	var states [][]RemoteHoldingInfo
	for _, order := range [][]callRow{rows, {rows[1], rows[0]}} {
		cloud := newCloudCache(t)
		a := newCloudApplier(t, cloud)
		a.call(order[0])
		if order[0].key == parent {
			require.Equal(t, []uint64{dep.Number, 99}, requireHolding(t, cloud, parent).UnknownDeps)
		}
		a.call(order[1])
		require.Empty(t, a.collect())
		p, d := requireHolding(t, cloud, parent), requireHolding(t, cloud, dep)
		require.Equal(t, []uint64{dep.Number}, p.Deps)
		require.Equal(t, []uint64{99}, p.UnknownDeps)
		require.Equal(t, 1, d.Dependents)
		states = append(states, []RemoteHoldingInfo{p, d})

		// The dependency lives as long as its parent does.
		a.endSession(engine, "s")
		a.observe(parent, droppedAt(1, 1))
		require.Equal(t, []HolderKey{dep, parent}, sortedKeys2(a.collect()))
		cloud.egraphMu.RLock()
		require.Empty(t, cloud.remoteCaches, "no reverse index outlives its holdings")
		cloud.egraphMu.RUnlock()
	}
	require.Equal(t, states[0], states[1])
}

// A lazy row about a holding that does not exist yet is dropped; the next
// call row that names the entry carries its parts and content digest.
func TestRemoteLazyUpdateBeforeHolding(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	key := HolderKey{"cache-a", 5}
	update := RemoteHoldingUpdate{ContentDigest: testDigest("content"), Parts: []PersistedPartAddress{{Part: "fs"}}, Deps: []uint64{4}}
	_, found, err := cloud.UpdateRemoteHolding(t.Context(), key, update)
	require.NoError(t, err)
	require.False(t, found)
	requireNoHolding(t, cloud, key)

	row := holdingOf("withExec")
	a.call(callRow{key: key, session: "s", holding: row})
	require.Empty(t, a.collect())
	require.Empty(t, requireHolding(t, cloud, key).Parts, "the dropped lazy row taught nothing")

	row.ContentDigest = testDigest("content")
	row.Parts = []PersistedPartAddress{{Part: "fs"}}
	a.call(callRow{key: key, session: "s", holding: row})
	h := requireHolding(t, cloud, key)
	require.Equal(t, []PersistedPartAddress{{Part: "fs"}}, h.Parts)
	require.Equal(t, testDigest("content"), h.ContentDigest)
	require.Equal(t, []HolderKey{key}, cloud.EquivalentHolders(testDigest("content").String()))

	// A lazy row after the holding adds to it.
	_, found, err = cloud.UpdateRemoteHolding(t.Context(), key, RemoteHoldingUpdate{Parts: []PersistedPartAddress{{OutputPath: PersistedRefPath{}.Field("rootfs"), Part: "snapshot"}}, Deps: []uint64{4}})
	require.NoError(t, err)
	require.True(t, found)
	h = requireHolding(t, cloud, key)
	require.Len(t, h.Parts, 2)
	require.Equal(t, []uint64{4}, h.UnknownDeps)
}

// A clean stop ends the process's sessions: a retained root and its
// unretained dependency stay under the same keys. The next generation's prune
// of the root then collects both.
func TestRemoteCleanStopThenNextGenerationPrune(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	engine := CacheID("cache-a")
	root, dep, scratch := HolderKey{engine, 2}, HolderKey{engine, 1}, HolderKey{engine, 3}
	a.call(callRow{key: dep, session: "s1", holding: holdingOf("dep")})
	a.call(callRow{key: root, session: "s1", holding: holdingOf("root", dep.Number), retained: retainedAt(1, 100, 0)})
	a.call(callRow{key: scratch, session: "s2", holding: holdingOf("scratch")})
	require.Empty(t, a.collect())

	a.candidates = append(a.candidates, cloud.EndRemoteProcess(t.Context(), engine, []string{"s1", "s2"})...)
	require.Equal(t, []HolderKey{scratch}, a.collect())
	keys, _ := cloudIndexes(cloud)
	require.Equal(t, []HolderKey{dep, root}, keys, "what the engine saved, under the same keys")

	// Generation 2's clock runs behind generation 1's: its drop still wins.
	a.observe(root, droppedAt(2, 50))
	require.Equal(t, []HolderKey{dep, root}, sortedKeys2(a.collect()))
	keys, recipes := cloudIndexes(cloud)
	require.Empty(t, keys)
	require.Empty(t, recipes)
}

// A stop that was not clean releases every holding of the cache, retained or
// not, and leaves other caches' holdings alone.
func TestRemoteReleaseCache(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	engineA, engineB := CacheID("cache-a"), CacheID("cache-b")
	a.call(callRow{key: HolderKey{engineA, 1}, session: "a", holding: holdingOf("dep")})
	a.call(callRow{key: HolderKey{engineA, 2}, session: "a", holding: holdingOf("root", 1), retained: retainedAt(1, 1, 0)})
	a.call(callRow{key: HolderKey{engineB, 1}, session: "b", holding: holdingOf("root"), retained: retainedAt(1, 1, 0)})
	require.Empty(t, a.collect())

	a.candidates = append(a.candidates, cloud.ReleaseRemoteCache(t.Context(), engineA)...)
	require.Equal(t, []HolderKey{{engineA, 1}, {engineA, 2}}, sortedKeys2(a.collect()))
	keys, recipes := cloudIndexes(cloud)
	require.Equal(t, []HolderKey{{engineB, 1}}, keys)
	require.Equal(t, []digest.Digest{testDigest("root")}, recipes)
}

// Applying the same batch of rows again changes nothing.
func TestRemoteReplayIsIdempotent(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	engine := CacheID("cache-a")
	withExpiry := holdingOf("root", 1, 77)
	withExpiry.ExpiresAtUnix = 900
	withExpiry.Parts = []PersistedPartAddress{{Part: "fs"}}
	batch := []callRow{
		{key: HolderKey{engine, 1}, session: "s", holding: holdingOf("dep")},
		{key: HolderKey{engine, 2}, session: "s", holding: withExpiry, retained: retainedAt(1, 3, 1000)},
		{key: HolderKey{engine, 3}, session: "t", holding: holdingOf("other")},
	}
	apply := func() {
		for _, row := range batch {
			a.call(row)
		}
		a.collect()
	}
	snapshot := func() []RemoteEntryInfo {
		var out []RemoteEntryInfo
		for _, row := range batch {
			info, ok := cloud.RemoteEntryInfo(row.key)
			require.True(t, ok)
			out = append(out, info)
		}
		return out
	}
	apply()
	first := snapshot()
	apply()
	require.Equal(t, first, snapshot())
}

// An entry known only through holdings is never a cache hit, and its number
// never loads a value, a call frame or a schema module.
func TestRemoteEntryHasNoValue(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cloud := newCloudCache(t)
	frame := cacheTestIntCall("leaf")
	recipe, err := frame.deriveRecipeDigest(cloud)
	require.NoError(t, err)
	_, err = cloud.AttachRemoteHolding(ctx, HolderKey{"cache-a", 1}, RemoteHolding{Recipe: recipe, Request: recipe, Field: "leaf", TypeName: "Int"})
	require.NoError(t, err)
	require.NoError(t, cloud.AddRemoteHold(ctx, HolderKey{"cache-a", 1}, "s"))
	cloud.egraphMu.RLock()
	id := uint64(cloud.holderEntries[HolderKey{"cache-a", 1}])
	cloud.egraphMu.RUnlock()
	require.NotZero(t, id)

	for _, session := range []string{"", "s"} {
		_, err := cloud.LoadResultByResultID(ctx, session, nil, id)
		require.ErrorIs(t, err, errEntryHasNoValue, "session %q", session)
		_, err = cloud.ResultCallByResultID(ctx, session, id)
		require.ErrorIs(t, err, errEntryHasNoValue, "session %q", session)
	}
	_, err = cloud.LoadResultByResultIDForSchema(ctx, "s", nil, id, nil)
	require.ErrorIs(t, err, errEntryHasNoValue)

	res, err := cloud.GetOrInitCall(ctx, "cloud", noopTypeResolver{}, &CallRequest{ResultCall: cacheTestIntCall("leaf")}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(cacheTestIntCall("leaf"), 7), nil
	})
	require.NoError(t, err)
	require.False(t, res.HitCache())
	require.Equal(t, 7, cacheTestUnwrapInt(t, res))
}

// Holdings of two caches on different recipes meet through their terms, as
// the engines' own results do: engine B's equality of x and y relates engine
// A's f(x) and f(y).
func TestRemoteHoldingsRelateThroughTerms(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	engineA, engineB := CacheID("cache-a"), CacheID("cache-b")
	f := func(input string) RemoteHolding {
		return RemoteHolding{
			Recipe:  testDigest("f-" + input),
			Request: testDigest("f-" + input),
			Field:   "f",
			Term:    &RemoteTerm{Self: testDigest("f-self"), Inputs: []digest.Digest{testDigest(input)}},
		}
	}
	a.call(callRow{key: HolderKey{engineA, 1}, session: "a", holding: f("x")})
	a.call(callRow{key: HolderKey{engineA, 2}, session: "a", holding: f("y")})
	require.Equal(t, []HolderKey{{engineA, 1}}, cloud.EquivalentHolders(testDigest("f-x").String()))

	a.call(callRow{key: HolderKey{engineB, 1}, session: "b", holding: RemoteHolding{Recipe: testDigest("x"), Request: testDigest("y"), Field: "x"}})
	require.Empty(t, a.collect())
	require.Equal(t, []HolderKey{{engineA, 1}, {engineA, 2}}, cloud.EquivalentHolders(testDigest("f-x").String()), "f(x) and f(y) are one class")
}

// Readers run concurrently with each other and with writers. Run with -race.
func TestRemoteHoldingsConcurrentReads(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cloud := newCloudCache(t)
	const n = 8
	engine := CacheID("cache-a")
	for i := range n {
		// Chain the classes so that finding a digest's root walks a path.
		desc := RemoteHolding{Recipe: testDigest("merge-" + strconv.Itoa(i)), Request: testDigest("merge-" + strconv.Itoa(i+1)), Field: "f"}
		_, err := cloud.AttachRemoteHolding(ctx, HolderKey{engine, uint64(i + 1)}, desc)
		require.NoError(t, err)
		require.NoError(t, cloud.AddRemoteHold(ctx, HolderKey{engine, uint64(i + 1)}, "s"))
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 50 {
				dig := testDigest("merge-" + strconv.Itoa((g+i)%n)).String()
				require.Len(t, cloud.EquivalentHolders(dig), n)
				info, ok := cloud.RemoteEntryInfo(HolderKey{engine, uint64((g+i)%n + 1)})
				require.True(t, ok)
				require.Len(t, info.ClassDigests, n+1)
				require.NotEmpty(t, cloud.HolderClosure(HolderKey{engine, uint64((g+i)%n + 1)}))
			}
		})
	}
	wg.Go(func() {
		for i := range 20 {
			key := HolderKey{"cache-w", uint64(i + 1)}
			_, err := cloud.AttachRemoteHolding(ctx, key, holdingOf("writer-"+strconv.Itoa(i)))
			require.NoError(t, err)
			candidates := cloud.ReleaseRemoteCache(ctx, "cache-w")
			_, err = cloud.CollectRemoteHoldings(ctx, append(candidates, key))
			require.NoError(t, err)
		}
	})
	wg.Wait()
}

// testLiveEqClassesLocked counts the classes that a term or an entry uses,
// the ones compaction keeps. Requires egraphMu for writing.
func testLiveEqClassesLocked(c *Cache) int {
	live := map[eqClassID]struct{}{}
	use := func(id eqClassID) {
		if root := c.findEqClassLocked(id); root != 0 {
			live[root] = struct{}{}
		}
	}
	for _, term := range c.egraphTerms {
		if term == nil {
			continue
		}
		for _, id := range term.inputEqIDs {
			use(id)
		}
		use(term.outputEqID)
	}
	for _, classes := range c.resultOutputEqClasses {
		for id := range classes {
			use(id)
		}
	}
	return len(live)
}

// testEqClassClock gives the cache a clock for its compaction checks that
// only the test moves, and returns the function that moves it.
func testEqClassClock(c *Cache) func(time.Duration) {
	now := time.Unix(1_000_000, 0)
	c.egraphMu.Lock()
	c.eqClassClock = func() time.Time { return now }
	c.egraphMu.Unlock()
	return func(d time.Duration) {
		c.egraphMu.Lock()
		now = now.Add(d)
		c.egraphMu.Unlock()
	}
}

// testEqClassChecks returns the cache's count of compaction checks.
func testEqClassChecks(c *Cache) int {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return c.eqClassChecks
}

// The Cloud's collection checks its classes for compaction at most once per
// interval, and only when something changed since the last check: the class
// slots differ, or a collection removed entries. Under churn, with holdings
// created and collected at every one-second read, the collections check once
// per interval, and each check leaves the slots under twice the live classes.
func TestCollectionChecksOncePerInterval(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	advance := testEqClassClock(cloud)
	a := newCloudApplier(t, cloud)
	for i := range 100 {
		live := holdingOf(fmt.Sprintf("live-%d", i))
		live.ContentDigest = testDigest(fmt.Sprintf("live-%d-content", i))
		a.call(callRow{key: HolderKey{"cache-live", uint64(i + 1)}, session: "live", holding: live})
	}
	require.Empty(t, a.collect())

	const rounds, perRound = 200, 5
	compactions := 0
	for r := range rounds {
		advance(time.Second)
		session := fmt.Sprintf("churn-%d", r)
		for i := range perRound {
			field := fmt.Sprintf("churn-%d-%d", r, i)
			churn := holdingOf(field)
			churn.ContentDigest = testDigest(field + "-content")
			a.call(callRow{key: HolderKey{"cache-churn", uint64(r*perRound + i + 1)}, session: session, holding: churn})
		}
		a.endSession("cache-churn", session)
		cloud.egraphMu.Lock()
		checkedAt, checkedSlots, removed := cloud.eqClassCheckedAt, cloud.eqClassCheckedSlots, cloud.eqClassRemoved
		checks, before, now, entries := cloud.eqClassChecks, cloud.eqClassSlotsLocked(), cloud.eqClassClock(), len(cloud.resultsByID)
		cloud.egraphMu.Unlock()
		require.Len(t, a.collect(), perRound)
		cloud.egraphMu.Lock()
		after, checksAfter, live, entriesAfter := cloud.eqClassSlotsLocked(), cloud.eqClassChecks, testLiveEqClassesLocked(cloud), len(cloud.resultsByID)
		cloud.egraphMu.Unlock()
		changed := before != checkedSlots || removed || entriesAfter < entries
		if changed && now.Sub(checkedAt) >= eqClassCheckInterval {
			require.Equal(t, checks+1, checksAfter, "round %d: the interval has passed, and something changed", r)
			require.Less(t, after, 2*live, "round %d: the check leaves the slots under twice the live classes", r)
		} else {
			require.Equal(t, checks, checksAfter, "round %d: within the interval, no check", r)
		}
		if after < before {
			compactions++
		}
	}
	checks := testEqClassChecks(cloud)
	require.Positive(t, compactions)
	require.LessOrEqual(t, checks, 1+rounds/10, "one check per interval, not one per collection")
}

// A collection that changes nothing never checks, however long the cache is
// idle; the first change after that checks at the next collection.
func TestCollectionNeverChecksWithoutChange(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	advance := testEqClassClock(cloud)
	a := newCloudApplier(t, cloud)
	a.call(callRow{key: HolderKey{"cache-a", 1}, session: "s", holding: holdingOf("idle")})
	require.Empty(t, a.collect())
	checks := testEqClassChecks(cloud)
	for range 100 {
		advance(time.Hour)
		require.Empty(t, a.collect())
	}
	require.Equal(t, checks, testEqClassChecks(cloud), "an idle cache never checks")

	a.call(callRow{key: HolderKey{"cache-a", 2}, session: "s", holding: holdingOf("change")})
	require.Empty(t, a.collect())
	require.Equal(t, checks+1, testEqClassChecks(cloud), "a change checks once its interval has passed")
}

// Entries of different recipes with equivalent content share one class, so a
// class's slot doesn't measure a check's cost: 100 live entries and their 100
// terms sit in one class. Twenty one-second sessions of two holdings each,
// created and collected, check once per interval.
func TestCollectionChecksManyEquivalentEntriesPerInterval(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	advance := testEqClassClock(cloud)
	a := newCloudApplier(t, cloud)
	for i := range 100 {
		h := holdingOf(fmt.Sprintf("equivalent-%d", i))
		h.ContentDigest = testDigest("same-content")
		a.call(callRow{key: HolderKey{"live", uint64(i + 1)}, session: "live", holding: h})
	}
	require.Empty(t, a.collect())
	cloud.egraphMu.Lock()
	live, entries, terms := testLiveEqClassesLocked(cloud), len(cloud.resultsByID), len(cloud.egraphTerms)
	cloud.egraphMu.Unlock()
	require.Equal(t, 1, live)
	require.Equal(t, 100, entries)
	require.Equal(t, 100, terms)
	checks := testEqClassChecks(cloud)
	const rounds = 20
	for round := range rounds {
		advance(time.Second)
		session := fmt.Sprintf("churn-%d", round)
		for j := range 2 {
			key := HolderKey{"churn", uint64(round*2 + j + 1)}
			a.call(callRow{key: key, session: session, holding: holdingOf(fmt.Sprintf("fresh-%d-%d", round, j))})
		}
		a.endSession("churn", session)
		require.Len(t, a.collect(), 2)
	}
	require.LessOrEqual(t, testEqClassChecks(cloud)-checks, rounds/10, "one check per interval: each scans the live entries and terms")
}

// Request digests that an engine's ordinary cache hits add to an entry's class
// grow the digests a compaction copies with no new entry or term. The engine's
// own calls produce them: a seed and 100 nested no-op calls hit the seed's
// entry, and the Cloud applies their evidence. Twenty one-second sessions of
// churn check once per interval, not once per session.
func TestCollectionChecksManyRequestDigestsPerInterval(t *testing.T) {
	t.Parallel()
	ctx, engineCache := cacheTestEvidenceEnv(t)
	defer cacheTestReleaseSession(t, engineCache, ctx)
	cloud := newCloudCache(t)
	advance := testEqClassClock(cloud)
	a := newCloudApplier(t, cloud)
	apply := func(req *CallRequest, res AnyResult) {
		frame, err := res.ResultCall()
		require.NoError(t, err)
		recipe, err := frame.deriveRecipeDigest(engineCache)
		require.NoError(t, err)
		request, err := req.ResultCall.deriveRecipeDigest(engineCache)
		require.NoError(t, err)
		ev := req.CacheEvidence
		h := RemoteHolding{Recipe: recipe, Request: request, Field: req.ResultCall.Field, Term: &RemoteTerm{Self: ev.SelfDigest, Inputs: ev.StructuralInputs}, TypeName: "Int"}
		a.call(callRow{key: HolderKey{"live", uint64(res.cacheSharedResult().id)}, session: "live", holding: h})
	}
	seed := cacheTestIntCall("alias-seed")
	req := cacheTestArmedRequest(seed)
	parent, err := engineCache.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, req, ValueFunc(cacheTestIntResult(seed, 42)))
	require.NoError(t, err)
	apply(req, parent)
	prev := seed
	for i := range 100 {
		frame := cacheTestIntCall("alias-noop")
		frame.Receiver = &ResultCallRef{Call: prev}
		req = cacheTestArmedRequest(frame)
		got, err := engineCache.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, req, ValueFunc(parent))
		require.NoError(t, err)
		require.Same(t, parent.cacheSharedResult(), got.cacheSharedResult())
		if i > 0 {
			require.True(t, got.HitCache())
		}
		apply(req, got)
		prev = frame
	}
	require.Empty(t, a.collect())
	cloud.egraphMu.Lock()
	entries, digests := len(cloud.resultsByID), len(cloud.egraphDigestToClass)
	cloud.egraphMu.Unlock()
	require.Equal(t, 1, entries)
	require.GreaterOrEqual(t, digests, 100)
	checks := testEqClassChecks(cloud)
	const rounds = 20
	for round := range rounds {
		advance(time.Second)
		session := fmt.Sprintf("real-churn-%d", round)
		for j := range 2 {
			field := fmt.Sprintf("real-churn-%d-%d", round, j)
			a.call(callRow{key: HolderKey{"churn", uint64(round*2 + j + 1)}, session: session, holding: holdingOf(field)})
		}
		a.endSession("churn", session)
		require.Len(t, a.collect(), 2)
	}
	require.LessOrEqual(t, testEqClassChecks(cloud)-checks, rounds/10, "one check per interval: each copies every request digest")
}

// A release with no new class, as an engine's unclean stop releases its whole
// cache, is a change: 200 of 201 entries go within an interval of the last
// check, and the first collection after the interval, with no candidates,
// checks and compacts the classes to the live ones. The entry that stays keeps
// its identity.
func TestCollectionChecksAReleaseAfterTheInterval(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	advance := testEqClassClock(cloud)
	a := newCloudApplier(t, cloud)
	keep := HolderKey{"stays", 1}
	a.call(callRow{key: keep, session: "stays", holding: holdingOf("stays")})
	for i := range 200 {
		a.call(callRow{key: HolderKey{"wiped", uint64(i + 1)}, session: "running", holding: holdingOf(fmt.Sprintf("old-%d", i))})
	}
	require.Empty(t, a.collect())
	before, ok := cloud.RemoteEntryInfo(keep)
	require.True(t, ok)
	checks := testEqClassChecks(cloud)

	advance(time.Second)
	collected, err := cloud.CollectRemoteHoldings(t.Context(), cloud.ReleaseRemoteCache(t.Context(), "wiped"))
	require.NoError(t, err)
	require.Len(t, collected, 200)
	require.Equal(t, checks, testEqClassChecks(cloud), "within the interval, the release waits")

	advance(eqClassCheckInterval)
	collected, err = cloud.CollectRemoteHoldings(t.Context(), nil)
	require.NoError(t, err)
	require.Empty(t, collected)
	cloud.egraphMu.Lock()
	slots, live := cloud.eqClassSlotsLocked(), testLiveEqClassesLocked(cloud)
	cloud.egraphMu.Unlock()
	require.Equal(t, checks+1, testEqClassChecks(cloud), "the first collection after the interval checks")
	require.Equal(t, live, slots, "and compacts to the live classes")
	after, ok := cloud.RemoteEntryInfo(keep)
	require.True(t, ok)
	require.Equal(t, before, after)
}

// Gone answers remove entries too, and never check by themselves: their
// removal counts toward the next collection's check after the interval, which
// compacts the classes to the live ones though it collects nothing itself.
func TestCollectionChecksGoneAnswersAfterTheInterval(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	advance := testEqClassClock(cloud)
	a := newCloudApplier(t, cloud)
	keep := HolderKey{"stays", 1}
	a.call(callRow{key: keep, session: "stays", holding: holdingOf("stays")})
	for i := range 200 {
		a.call(callRow{key: HolderKey{"gone", uint64(i + 1)}, session: "running", holding: holdingOf(fmt.Sprintf("gone-%d", i))})
	}
	require.Empty(t, a.collect())
	checks := testEqClassChecks(cloud)

	advance(eqClassCheckInterval)
	for i := range 200 {
		_, found, err := cloud.CollectRemoteHolding(t.Context(), HolderKey{"gone", uint64(i + 1)})
		require.NoError(t, err)
		require.True(t, found)
	}
	require.Equal(t, checks, testEqClassChecks(cloud), "gone answers don't check, even after the interval")

	collected, err := cloud.CollectRemoteHoldings(t.Context(), nil)
	require.NoError(t, err)
	require.Empty(t, collected)
	cloud.egraphMu.Lock()
	slots, live := cloud.eqClassSlotsLocked(), testLiveEqClassesLocked(cloud)
	cloud.egraphMu.Unlock()
	require.Equal(t, checks+1, testEqClassChecks(cloud), "the next collection checks")
	require.Equal(t, live, slots, "and compacts to the live classes")
	_, ok := cloud.RemoteEntryInfo(keep)
	require.True(t, ok)
}

// A term over many inputs whose holdings the Cloud never saw adds one class
// per input. Releasing that one entry is a change even though it removes only
// one entry and one term: the first collection after the interval checks and
// compacts the classes to the live ones.
func TestCollectionChecksAWideTermReleaseAfterTheInterval(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	advance := testEqClassClock(cloud)
	a := newCloudApplier(t, cloud)
	for i := range 30 {
		a.call(callRow{key: HolderKey{"anchors", uint64(i + 1)}, session: "anchors", holding: holdingOf(fmt.Sprintf("anchor-%d", i))})
	}
	wide := holdingOf("wide")
	for i := range 3000 {
		wide.Term.Inputs = append(wide.Term.Inputs, testDigest(fmt.Sprintf("unobserved-input-%d", i)))
	}
	a.call(callRow{key: HolderKey{"burst", 1}, session: "burst", holding: wide})
	require.Empty(t, a.collect())
	checks := testEqClassChecks(cloud)

	advance(time.Second)
	a.endSession("burst", "burst")
	require.Len(t, a.collect(), 1)
	require.Equal(t, checks, testEqClassChecks(cloud), "within the interval, the release waits")

	advance(eqClassCheckInterval)
	require.Empty(t, a.collect())
	cloud.egraphMu.Lock()
	slots, live := cloud.eqClassSlotsLocked(), testLiveEqClassesLocked(cloud)
	cloud.egraphMu.Unlock()
	require.Equal(t, checks+1, testEqClassChecks(cloud))
	require.Equal(t, 30, live)
	require.Equal(t, live, slots, "the wide term's input classes are freed")
}

// A row returns the recipe digest of each entry it affected: one it created,
// gave a new holding, or changed the holding's value state of (parts, count or
// expiry), and every entry it joined to another class, directly or by
// congruence. A row that does none of these returns no digest.
func TestHoldingRowsReturnTheAffectedRecipes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cloud := newCloudCache(t)
	key := func(cache CacheID, n uint64) HolderKey { return HolderKey{Cache: cache, Number: n} }
	attach := func(k HolderKey, h RemoteHolding) []digest.Digest {
		t.Helper()
		change, err := cloud.AttachRemoteHolding(ctx, k, h)
		require.NoError(t, err)
		return change.Recipes
	}
	update := func(k HolderKey, u RemoteHoldingUpdate) []digest.Digest {
		t.Helper()
		change, found, err := cloud.UpdateRemoteHolding(ctx, k, u)
		require.NoError(t, err)
		require.True(t, found)
		return change.Recipes
	}
	parent := func(field, input string) RemoteHolding {
		h := holdingOf(field)
		h.Term = &RemoteTerm{Self: testDigest("parent-self"), Inputs: []digest.Digest{testDigest(input)}}
		return h
	}

	require.Equal(t, []digest.Digest{testDigest("x")}, attach(key("a", 1), holdingOf("x")), "created")
	require.Equal(t, []digest.Digest{testDigest("x")}, attach(key("b", 1), holdingOf("x")), "a new holding")
	require.Empty(t, attach(key("a", 1), holdingOf("x")), "the same report again")
	withExpiry := holdingOf("x")
	withExpiry.ExpiresAtUnix = time.Now().Add(time.Hour).Unix()
	require.Equal(t, []digest.Digest{testDigest("x")}, attach(key("a", 1), withExpiry), "the expiry")
	require.Equal(t, []digest.Digest{testDigest("x")}, update(key("a", 1), RemoteHoldingUpdate{Parts: []PersistedPartAddress{{Part: "fs"}}}), "a part")
	require.Equal(t, []digest.Digest{testDigest("x")}, update(key("a", 1), RemoteHoldingUpdate{Replacements: 1}), "the count")
	require.Empty(t, update(key("a", 1), RemoteHoldingUpdate{Replacements: 1, Deps: []uint64{9}}), "dependencies only")

	attach(key("a", 2), holdingOf("y"))
	attach(key("a", 3), parent("parent-x", "x"))
	attach(key("a", 4), parent("parent-y", "y"))
	require.Empty(t, update(key("a", 1), RemoteHoldingUpdate{Replacements: 1, ContentDigest: testDigest("content")}), "a content digest no other entry has")
	joined := update(key("a", 2), RemoteHoldingUpdate{ContentDigest: testDigest("content")})
	require.ElementsMatch(t,
		[]digest.Digest{testDigest("x"), testDigest("y"), testDigest("parent-x"), testDigest("parent-y")},
		joined, "y joins x's class, and the parents join by congruence")
	// Each returned digest names its entry's class for EquivalentHolders.
	for _, dig := range joined {
		want := []HolderKey{key("a", 1), key("a", 2), key("b", 1)}
		if dig == testDigest("parent-x") || dig == testDigest("parent-y") {
			want = []HolderKey{key("a", 3), key("a", 4)}
		}
		require.Equal(t, want, cloud.EquivalentHolders(dig.String()), "%s", dig)
	}
}

// A content digest is class identity, which every observation teaches
// whatever its count: R's class joins X's through an old value's content
// digest, reported by a call span or a lazy span, before or after the
// replacement's count-1 observation. R's holding keeps the count-1 value
// state either way.
func TestContentDigestIsTaughtWhateverTheCount(t *testing.T) {
	t.Parallel()
	for _, lazy := range []bool{false, true} {
		for _, oldLast := range []bool{false, true} {
			name := map[bool]string{false: "call", true: "lazy"}[lazy] + "/" + map[bool]string{false: "old first", true: "old last"}[oldLast]
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				ctx := t.Context()
				cloud := newCloudCache(t)
				r, x := HolderKey{Cache: "a", Number: 1}, HolderKey{Cache: "a", Number: 2}
				other := holdingOf("x")
				other.ContentDigest = testDigest("old-content")
				_, err := cloud.AttachRemoteHolding(ctx, x, other)
				require.NoError(t, err)
				first := holdingOf("r")
				_, err = cloud.AttachRemoteHolding(ctx, r, first)
				require.NoError(t, err)
				replaced := func() {
					_, _, err := cloud.UpdateRemoteHolding(ctx, r, RemoteHoldingUpdate{Replacements: 1})
					require.NoError(t, err)
				}
				oldValue := func() {
					if lazy {
						_, _, err := cloud.UpdateRemoteHolding(ctx, r, RemoteHoldingUpdate{ContentDigest: other.ContentDigest})
						require.NoError(t, err)
						return
					}
					old := holdingOf("r")
					old.ContentDigest = other.ContentDigest
					_, err := cloud.AttachRemoteHolding(ctx, r, old)
					require.NoError(t, err)
				}
				if oldLast {
					replaced()
					oldValue()
				} else {
					oldValue()
					replaced()
				}
				require.Equal(t, []HolderKey{r, x}, cloud.EquivalentHolders(first.Recipe.String()), "R's class learned the old content digest")
				h := requireHolding(t, cloud, r)
				require.Equal(t, uint64(1), h.Replacements)
				require.Empty(t, h.ContentDigest, "the holding's value state follows the count")
			})
		}
	}
}

// testBlobCloud returns a blob-backed Cloud cache, the remote-cache service's
// kind, with the compaction checks' test clock and an applier. It holds live
// holdings of distinct content, and its first collection has checked.
func testBlobCloud(t *testing.T, live int) (*Cache, func(time.Duration), *cloudApplier) {
	t.Helper()
	_, cloud := storedPartTestCache(t, WithBlobStore())
	advance := testEqClassClock(cloud)
	a := newCloudApplier(t, cloud)
	for i := range live {
		h := holdingOf(fmt.Sprintf("live-%d", i))
		h.ContentDigest = testDigest(fmt.Sprintf("live-%d-content", i))
		a.call(callRow{key: HolderKey{"cache-live", uint64(i + 1)}, session: "live", holding: h})
	}
	require.Empty(t, a.collect())
	require.Equal(t, 1, testEqClassChecks(cloud))
	return cloud, advance, a
}

// testEqClassCheckState is what a compaction check changes: the class slots,
// the count of checks and when the last one ran.
type testEqClassCheckState struct {
	slots     int
	checks    int
	checkedAt time.Time
}

func testEqClassCheckStateOf(c *Cache) testEqClassCheckState {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return testEqClassCheckState{slots: c.eqClassSlotsLocked(), checks: c.eqClassChecks, checkedAt: c.eqClassCheckedAt}
}

// testDeadEqClasses adds classes known only by a digest, which no term and no
// entry uses.
func testDeadEqClasses(t *testing.T, c *Cache, n int) {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	for i := range n {
		c.ensureEqClassForDigestLocked(t.Context(), fmt.Sprintf("dead-%d", i))
	}
}

// The service compacts before it forgets any engine's holdings. Right after a
// collection, within the interval and with fewer dead classes than live ones,
// neither of which the collection's check would compact, CompactEqClasses
// frees every dead slot. What the live classes hold is unchanged, and the call
// counts as a check.
func TestCompactEqClassesForcesTheCheck(t *testing.T) {
	t.Parallel()
	cloud, advance, a := testBlobCloud(t, 20)
	advance(time.Second)
	for i := range 2 {
		churn := holdingOf(fmt.Sprintf("churn-%d", i))
		churn.ContentDigest = testDigest(fmt.Sprintf("churn-%d-content", i))
		a.call(callRow{key: HolderKey{"cache-churn", uint64(i + 1)}, session: "churn", holding: churn})
	}
	a.endSession("cache-churn", "churn")
	require.Len(t, a.collect(), 2)

	cloud.egraphMu.Lock()
	before, live, checks := cloud.eqClassSlotsLocked(), testLiveEqClassesLocked(cloud), cloud.eqClassChecks
	contents := testEqClassContentsLocked(cloud)
	cloud.egraphMu.Unlock()
	require.Equal(t, 1, checks, "the collection within the interval didn't check")
	require.Greater(t, before, live, "the collected holdings left dead classes")
	require.Less(t, before, 2*live, "fewer dead classes than live ones: the unforced compaction keeps them")

	oldSlots, newSlots, err := cloud.CompactEqClasses(t.Context())
	require.NoError(t, err)
	require.Equal(t, before, oldSlots)
	require.Equal(t, live, newSlots)

	cloud.egraphMu.Lock()
	after, checksAfter := cloud.eqClassSlotsLocked(), cloud.eqClassChecks
	afterContents, indexErr := testEqClassContentsLocked(cloud), cacheDerivedIndexesErrorLocked(cloud)
	cloud.egraphMu.Unlock()
	require.Equal(t, live, after)
	require.NoError(t, indexErr)
	require.Equal(t, contents, afterContents)
	require.Equal(t, checks+1, checksAfter)
}

// A forced compaction is a check: the collection checks again only once the
// interval since the forced call has passed, though the interval since the
// check before it has.
func TestCompactEqClassesRestartsTheInterval(t *testing.T) {
	t.Parallel()
	cloud, advance, a := testBlobCloud(t, 20)
	advance(9 * time.Second)
	_, _, err := cloud.CompactEqClasses(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, testEqClassChecks(cloud))

	// A change after the forced call, for the collection to check.
	a.call(callRow{key: HolderKey{"cache-new", 1}, session: "new", holding: holdingOf("after-force")})
	advance(2 * time.Second)
	require.Empty(t, a.collect())
	require.Equal(t, 2, testEqClassChecks(cloud), "11s after the first check, 2s after the forced one: no check")
	advance(8 * time.Second)
	require.Empty(t, a.collect())
	require.Equal(t, 3, testEqClassChecks(cloud), "10s after the forced check: the change is checked")
}

// With no class live, the forced compaction frees every slot, which the
// unforced one leaves, and keeps the entries: here one entry with no class,
// which keeps the e-graph from its reset. The next class is class 1.
func TestCompactEqClassesFreesEverySlotWhenNoClassIsLive(t *testing.T) {
	t.Parallel()
	_, cloud := storedPartTestCache(t, WithBlobStore())
	testDeadEqClasses(t, cloud, 8)
	cloud.egraphMu.Lock()
	entry := &sharedResult{id: cloud.nextSharedResultID}
	cloud.nextSharedResultID++
	cloud.putResultLocked(entry)
	before, live := cloud.eqClassSlotsLocked(), testLiveEqClassesLocked(cloud)
	cloud.egraphMu.Unlock()
	require.Equal(t, 8, before)
	require.Zero(t, live)

	oldSlots, newSlots, err := cloud.CompactEqClasses(t.Context())
	require.NoError(t, err)
	require.Equal(t, 8, oldSlots)
	require.Zero(t, newSlots)

	cloud.egraphMu.Lock()
	after, digests, kept := cloud.eqClassSlotsLocked(), len(cloud.egraphDigestToClass), cloud.resultsByID[entry.id] == entry
	next := cloud.ensureEqClassForDigestLocked(t.Context(), "after")
	indexErr := cacheDerivedIndexesErrorLocked(cloud)
	cloud.deleteResultLocked(entry)
	cloud.egraphMu.Unlock()
	require.Zero(t, after)
	require.Zero(t, digests)
	require.True(t, kept, "the entry stays")
	require.Equal(t, eqClassID(1), next)
	require.NoError(t, indexErr)
}

// CompactEqClasses refuses, changing nothing, on a cache without a blob store,
// with snapshot sharing enabled, and when its context is done before the
// compaction starts, including while it waits for the graph.
func TestCompactEqClassesRefusesAndChangesNothing(t *testing.T) {
	t.Parallel()
	refuses := func(t *testing.T, c *Cache, ctx context.Context, check func(error)) {
		t.Helper()
		before := testEqClassCheckStateOf(c)
		_, _, err := c.CompactEqClasses(ctx)
		check(err)
		require.Equal(t, before, testEqClassCheckStateOf(c))
	}
	t.Run("no blob store", func(t *testing.T) {
		t.Parallel()
		cloud := newCloudCache(t)
		testDeadEqClasses(t, cloud, 4)
		refuses(t, cloud, t.Context(), func(err error) { require.ErrorContains(t, err, "the cache has no blob store") })
	})
	t.Run("snapshot sharing", func(t *testing.T) {
		t.Parallel()
		_, cloud := storedPartTestCache(t, WithBlobStore())
		testDeadEqClasses(t, cloud, 4)
		require.NoError(t, cloud.EnableSnapshotSharing())
		refuses(t, cloud, t.Context(), func(err error) { require.ErrorContains(t, err, "snapshot sharing is enabled") })
	})
	t.Run("done context", func(t *testing.T) {
		t.Parallel()
		_, cloud := storedPartTestCache(t, WithBlobStore())
		testDeadEqClasses(t, cloud, 4)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		refuses(t, cloud, ctx, func(err error) { require.ErrorIs(t, err, context.Canceled) })
	})
	t.Run("done while waiting for the graph", func(t *testing.T) {
		t.Parallel()
		_, cloud := storedPartTestCache(t, WithBlobStore())
		testDeadEqClasses(t, cloud, 4)
		before := testEqClassCheckStateOf(cloud)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		cloud.egraphMu.Lock()
		locked := true
		defer func() {
			if locked {
				cloud.egraphMu.Unlock()
			}
		}()
		done := make(chan error, 1)
		go func() {
			_, _, err := cloud.CompactEqClasses(ctx)
			done <- err
		}()
		blocked := false
		for deadline := time.Now().Add(10 * time.Second); !blocked && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
			buf := make([]byte, 1<<20)
			n := runtime.Stack(buf, true)
			for _, stack := range strings.Split(string(buf[:n]), "\n\n") {
				if strings.Contains(stack, "(*Cache).CompactEqClasses(") && strings.Contains(stack, "sync.(*RWMutex).Lock(") {
					blocked = true
					break
				}
			}
		}
		require.True(t, blocked, "CompactEqClasses waits for the graph")
		cancel()
		cloud.egraphMu.Unlock()
		locked = false
		require.ErrorIs(t, <-done, context.Canceled)
		require.Equal(t, before, testEqClassCheckStateOf(cloud))
	})
}
