package dagql

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Each write of an engine cache's holdings names, in its change, the kinds
// of state engines share that it changed, and only those: an engine's
// first holding creates its state, a new recipe its entry, an identity the
// e-graph lacks is taught; a report the cache already holds, a hold, a
// retention, a session's end and a release change only the engine's own; a
// content digest other than the last one taught grows the entry's list
// every time; a collection that empties an entry and an engine cache
// removes both, and compacts the classes it left dead, which a collection
// after it with nothing changed doesn't.
func TestRemoteChangesNameTheSharedStateTheyChange(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	a, b := CacheID("cache-a"), CacheID("cache-b")
	attach := func(key HolderKey, desc RemoteHolding) SharedWrites {
		t.Helper()
		change, err := cloud.AttachRemoteHolding(ctx, key, desc)
		require.NoError(t, err)
		return change.Shared
	}
	const none = SharedWrites(0)

	require.Equal(t, SharedNewEngineCache|SharedNewEntry|SharedIdentity, attach(HolderKey{a, 1}, holdingOf("shared-1")), "A's first holding")
	require.Equal(t, none, attach(HolderKey{a, 1}, holdingOf("shared-1")), "the same report again")
	require.Equal(t, SharedNewEngineCache, attach(HolderKey{b, 1}, holdingOf("shared-1")), "B's holding of a recipe A taught")
	require.Equal(t, SharedNewEntry|SharedIdentity, attach(HolderKey{b, 2}, holdingOf("shared-2", 1)), "a new recipe of B's")

	change, err := cloud.AddRemoteHold(ctx, HolderKey{b, 2}, "s1")
	require.NoError(t, err)
	require.Equal(t, none, change.Shared, "a hold")
	change, err = cloud.ObserveRemoteRetention(ctx, HolderKey{b, 1}, *retainedAt(1, 10, 0))
	require.NoError(t, err)
	require.Equal(t, none, change.Shared, "a retention")
	require.Equal(t, none, cloud.ReleaseRemoteSession(ctx, b, "s1").Shared, "a session's end")

	first := holdingOf("shared-1")
	first.ContentDigest = testDigest("shared-content-1")
	second := holdingOf("shared-1")
	second.ContentDigest = testDigest("shared-content-2")
	require.Equal(t, SharedTaughtContent|SharedIdentity, attach(HolderKey{a, 1}, first), "a content digest")
	require.Equal(t, none, attach(HolderKey{b, 1}, first), "another engine's report of the same content digest")
	require.Equal(t, SharedTaughtContent|SharedIdentity, attach(HolderKey{b, 1}, second), "another content digest of the recipe")
	require.Equal(t, SharedTaughtContent, attach(HolderKey{a, 1}, first), "the first content digest again, which the class holds")

	released := cloud.ReleaseRemoteCache(ctx, b)
	require.Equal(t, none, released.Shared, "a release")
	_, change, err = cloud.CollectRemoteHoldings(ctx, released.Candidates, nil)
	require.NoError(t, err)
	require.Equal(t, SharedEntryRemoved, change.Shared&SharedEntryRemoved, "shared-2's entry, owned by B's holding alone, goes: %v", change.Shared)
	require.Equal(t, SharedEngineCacheGone, change.Shared&SharedEngineCacheGone, "B's state goes with its last holding: %v", change.Shared)
	require.Equal(t, SharedClassesCompacted, change.Shared&SharedClassesCompacted, "the first collection compacts the classes, most of whose slots it left dead: %v", change.Shared)
	_, change, err = cloud.CollectRemoteHoldings(ctx, []HolderKey{{a, 9}}, nil)
	require.NoError(t, err)
	require.Equal(t, none, change.Shared, "a collection of a holding the cache doesn't have, with no class changed since the check")
	_, err = cloud.AddRemoteHold(ctx, HolderKey{a, 1}, "s2")
	require.NoError(t, err)
	_, change, err = cloud.CollectRemoteHoldings(ctx, []HolderKey{{a, 1}}, nil)
	require.NoError(t, err)
	require.Equal(t, none, change.Shared, "a collection that collects nothing")

	// A call that returned another recipe's entry is kept on it, with the
	// call's expiry; the same report again keeps nothing new. Its engine,
	// and the run's below, are their own: their numbers are another
	// cache's.
	test := newCallRecordTest(t, ctx, cloud)
	key := HolderKey{Cache: "cache-k", Number: uint64(test.inner.cacheSharedResult().id)}
	span := test.span(t, true, time.Now().Add(time.Hour).Unix())
	kept := attach(key, span)
	require.Equal(t, SharedKeptCall|SharedExpiry, kept&(SharedKeptCall|SharedExpiry), "a call kept: %v", kept)
	require.Equal(t, none, attach(key, span), "the same call again")

	// A run noted as having run, once.
	number, run := runTestExport(t, ctx, cloud, "cache-r", "shared-run")
	runKey := HolderKey{Cache: run.Cache, Number: run.Number}
	require.Equal(t, SharedRunRan, runTestEvaluated(t, ctx, cloud, runKey, run.Replacements, true).Shared&SharedRunRan, "the run ran")
	require.True(t, runTestRan(t, cloud, number).Ran)
	require.Equal(t, none, runTestEvaluated(t, ctx, cloud, runKey, run.Replacements, true).Shared, "the run ran again")
}

// An engine's own cache has no engine caches' holdings to share, and its
// writes name nothing.
func TestAnEngineCacheNamesNoSharedWrites(t *testing.T) {
	t.Parallel()
	ctx, c := storedPartTestCache(t)
	change, err := c.AttachRemoteHolding(ctx, HolderKey{"cache-a", 1}, holdingOf("engine-1"))
	require.NoError(t, err)
	require.Equal(t, SharedWrites(0), change.Shared)
}

// A write that names no kind of shared state changes none: the entries,
// their own facts, the recipes' index, the e-graph, its union-find's raw
// parents included, and which engine caches have state are as they were.
// The run is the remote writes' whole script: attaches joined by content
// and by term, dependencies named before their holdings, holds and their
// sessions' ends, retention, merged replies, value states, a release and
// collections. A write that would run beside other engines' writes, as one
// naming nothing would, must change nothing they read.
func TestRemoteWritesThatNameNoSharedStateChangeNone(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	check := &sharedStateCheck{t: t, c: cloud}
	remoteWritesScript(t, ctx, check)
	t.Logf("writes naming no shared state: %d; naming some: %d", check.own, check.shared)
	require.GreaterOrEqual(t, check.own, 20, "the script's writes that change only their engine's own")
	require.GreaterOrEqual(t, check.shared, 5, "the script's writes that change shared state")
}

// sharedStateCheck is a cache whose writes are checked against what they
// name (RemoteChange.Shared): one that names nothing leaves sharedStateOf
// as it was. It counts the writes of each.
type sharedStateCheck struct {
	t           *testing.T
	c           *Cache
	own, shared int
}

func (s *sharedStateCheck) checked(name string, before string, change RemoteChange) {
	s.t.Helper()
	if change.Shared != 0 {
		s.shared++
		return
	}
	s.own++
	require.Equal(s.t, before, sharedStateOf(s.c), "%s named no shared state, and changed some", name)
}

func (s *sharedStateCheck) AttachRemoteHolding(ctx context.Context, key HolderKey, desc RemoteHolding) (RemoteChange, error) {
	before := sharedStateOf(s.c)
	change, err := s.c.AttachRemoteHolding(ctx, key, desc)
	s.checked(fmt.Sprintf("attach %v", key), before, change)
	return change, err
}

func (s *sharedStateCheck) UpdateRemoteHolding(ctx context.Context, key HolderKey, u RemoteHoldingUpdate) (RemoteChange, bool, error) {
	before := sharedStateOf(s.c)
	change, found, err := s.c.UpdateRemoteHolding(ctx, key, u)
	s.checked(fmt.Sprintf("update %v", key), before, change)
	return change, found, err
}

func (s *sharedStateCheck) AddRemoteHold(ctx context.Context, key HolderKey, session string) (RemoteChange, error) {
	before := sharedStateOf(s.c)
	change, err := s.c.AddRemoteHold(ctx, key, session)
	s.checked(fmt.Sprintf("hold %v", key), before, change)
	return change, err
}

func (s *sharedStateCheck) ObserveRemoteRetention(ctx context.Context, key HolderKey, obs RetentionObservation) (RemoteChange, error) {
	before := sharedStateOf(s.c)
	change, err := s.c.ObserveRemoteRetention(ctx, key, obs)
	s.checked(fmt.Sprintf("retain %v", key), before, change)
	return change, err
}

func (s *sharedStateCheck) ApplyHeldValueState(ctx context.Context, key HolderKey, obs HeldValueState) (RemoteChange, bool, error) {
	before := sharedStateOf(s.c)
	change, found, err := s.c.ApplyHeldValueState(ctx, key, obs)
	s.checked(fmt.Sprintf("value state %v", key), before, change)
	return change, found, err
}

func (s *sharedStateCheck) ApplyMergedReply(ctx context.Context, cache CacheID, sent ValueBundle, reply MergeReply) (RemoteChange, error) {
	before := sharedStateOf(s.c)
	change, err := s.c.ApplyMergedReply(ctx, cache, sent, reply)
	s.checked(fmt.Sprintf("merged reply %v", cache), before, change)
	return change, err
}

func (s *sharedStateCheck) ReleaseRemoteSession(ctx context.Context, cache CacheID, session string) RemoteChange {
	before := sharedStateOf(s.c)
	change := s.c.ReleaseRemoteSession(ctx, cache, session)
	s.checked(fmt.Sprintf("end %v %s", cache, session), before, change)
	return change
}

func (s *sharedStateCheck) ReleaseRemoteCache(ctx context.Context, cache CacheID) RemoteChange {
	before := sharedStateOf(s.c)
	change := s.c.ReleaseRemoteCache(ctx, cache)
	s.checked(fmt.Sprintf("release %v", cache), before, change)
	return change
}

func (s *sharedStateCheck) CollectRemoteHoldings(ctx context.Context, candidates []HolderKey, owned func(HolderKey) bool) ([]HolderKey, RemoteChange, error) {
	before := sharedStateOf(s.c)
	collected, change, err := s.c.CollectRemoteHoldings(ctx, candidates, owned)
	s.checked(fmt.Sprintf("collect %v", candidates), before, change)
	return collected, change, err
}

func (s *sharedStateCheck) RemoteHoldingInfo(key HolderKey) (RemoteHeldEntry, bool) {
	return s.c.RemoteHoldingInfo(key)
}

func (s *sharedStateCheck) HolderClosure(key HolderKey) []HolderKey {
	return s.c.HolderClosure(key)
}

func (s *sharedStateCheck) HolderGraph(cache CacheID, roots []uint64) map[uint64][]uint64 {
	return s.c.HolderGraph(cache, roots)
}

func (s *sharedStateCheck) EntryClassEntries(number uint64) (EntryClass, bool) {
	return s.c.EntryClassEntries(number)
}

var _ remoteWriteOps = (*sharedStateCheck)(nil)

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
		fmt.Fprintf(&b, "entry %d %s: value %v %v, expires %d, run %v %v, taught %v given %d, calls %v, recipes %v, stored %d, stamp %d %v, deps %v\n",
			id, res.description, hasValue, envelope, res.expiresAtUnix, res.run, res.runRan,
			res.taught, res.taughtGiven, slices.Sorted(maps.Keys(res.calls)), res.recipeKeys,
			len(res.storedParts), res.changedAt, res.stampCause, slices.Sorted(maps.Keys(res.deps)))
	}
	for _, recipe := range slices.Sorted(maps.Keys(c.entriesByRecipe)) {
		fmt.Fprintf(&b, "recipe %s: %d\n", recipe, c.entriesByRecipe[recipe])
	}
	fmt.Fprintf(&b, "parents %v\nranks %v\nfree %v\nwrites %d\n", c.egraphParents, c.egraphRanks, c.freeEqClassIDs, c.writes)
	for _, dig := range slices.Sorted(maps.Keys(c.egraphDigestToClass)) {
		fmt.Fprintf(&b, "digest %s: class %d\n", dig, c.egraphDigestToClass[dig])
	}
	for _, class := range slices.Sorted(maps.Keys(c.eqClassToDigests)) {
		fmt.Fprintf(&b, "class %d: digests %v, extras %d\n", class, slices.Sorted(maps.Keys(c.eqClassToDigests[class])), len(c.eqClassExtraDigests[class]))
	}
	for _, dig := range slices.Sorted(maps.Keys(c.egraphResultsByDigest)) {
		fmt.Fprintf(&b, "posting %s: %v\n", dig, slices.Sorted(c.egraphResultsByDigest[dig].Items()))
	}
	for _, id := range slices.Sorted(maps.Keys(c.egraphTerms)) {
		term := c.egraphTerms[id]
		fmt.Fprintf(&b, "term %d: %s %v -> %d\n", id, term.selfDigest, term.inputEqIDs, term.outputEqID)
	}
	for _, id := range slices.Sorted(maps.Keys(c.resultOutputEqClasses)) {
		fmt.Fprintf(&b, "outputs %d: %v\n", id, slices.Sorted(maps.Keys(c.resultOutputEqClasses[id])))
	}
	for _, class := range slices.Sorted(maps.Keys(c.outputEqClassResults)) {
		fmt.Fprintf(&b, "results %d: %v\n", class, slices.Sorted(maps.Keys(c.outputEqClassResults[class])))
	}
	fmt.Fprintf(&b, "engine caches %v\n", slices.Sorted(maps.Keys(c.remoteCaches)))
	return b.String()
}
