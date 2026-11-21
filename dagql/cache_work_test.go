package dagql

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// A call's lookup that waits for the graph lock is counted by the kind of
// work of the writer that let it in, apart while a set of the Cloud's
// records lands; a lookup that takes the lock at once counts nothing.
func TestLookupLockWaitsNameTheirHolder(t *testing.T) {
	ctx, c := lockWaitTestCache(t)
	lookupBehind := func(work cacheWork, field string) {
		t.Helper()
		c.lockGraphFor(ctx, work)
		done := lockWaitTestCall(ctx, c, cacheTestIntCall(field), nil)
		waitBlockedIn(t, "lookupReadLockGraph(")
		time.Sleep(time.Millisecond)
		c.egraphMu.Unlock()
		require.NoError(t, <-done)
	}

	lookupBehind(workSharing, "lookupBehindSharing")
	landed := c.Landing()
	lookupBehind(workRestore, "lookupBehindRestore")
	landed()
	landed() // A second end of the same landing changes nothing.
	lookupBehind(workExport, "lookupBehindExport")
	require.NoError(t, <-lockWaitTestCall(ctx, c, cacheTestIntCall("lookupAtOnce"), nil))

	waits := c.LookupLockWaits()
	require.Equal(t, LookupLockWaits{
		Other: map[string]LockWaitStats{
			"sharing": waits.Other["sharing"],
			"export":  waits.Other["export"],
		},
		Landing: map[string]LockWaitStats{
			"restore": waits.Landing["restore"],
		},
	}, waits, "only the three waits, each by its writer's kind and window")
	for name, stats := range map[string]LockWaitStats{"sharing": waits.Other["sharing"], "export": waits.Other["export"], "restore": waits.Landing["restore"]} {
		require.Equal(t, int64(1), stats.Waits, name)
		require.GreaterOrEqual(t, stats.Total, time.Millisecond, name)
		require.Equal(t, stats.Total, stats.Longest, name)
	}
}

// A lookup that waits while a restore's part commits is let in by the
// restore.
func TestLookupBehindARestoreCommitNamesTheRestore(t *testing.T) {
	t.Parallel()
	set := firstUseTestCloud(t)
	ctx, engine, srv := transferTestCache(t)
	inCommit, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	engine.testMergeCommitLocked = func() {
		once.Do(func() {
			close(inCommit)
			<-release
		})
	}
	restored := make(chan error, 1)
	go func() {
		reply, err := engine.RestoreValues(ctx, set.bundle, nil, "test")
		if err == nil && reply.Release != nil {
			err = reply.Release(ctx)
		}
		restored <- err
	}()
	<-inCommit
	looked := make(chan error, 1)
	go func() {
		frame := &ResultCall{Kind: ResultCallKindField, Field: "behind-restore", Type: NewResultCallType((&transferTestValue{}).Type())}
		_, err := engine.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(&transferTestValue{Text: "ran"}, frame)
		})
		looked <- err
	}()
	waitBlockedIn(t, "lookupReadLockGraph(")
	close(release)
	require.NoError(t, <-restored)
	require.NoError(t, <-looked)
	waits := engine.LookupLockWaits()
	require.Equal(t, int64(1), waits.Other["restore"].Waits, "%+v", waits)
}

// A merge's preparation reports how long it waited for the graph lock, by
// the kind of writer that let it in, beside its time.
func TestMergePreparationReportsItsLockWaits(t *testing.T) {
	t.Parallel()
	set := firstUseTestCloud(t)
	ctx, engine, _ := transferTestCache(t)
	engine.lockGraphFor(ctx, workSharing)
	type restored struct {
		reply MergeReply
		err   error
	}
	done := make(chan restored, 1)
	go func() {
		reply, err := engine.RestoreValues(ctx, set.bundle, nil, "test")
		done <- restored{reply, err}
	}()
	waitBlockedIn(t, "prepareCheckedValueMerge(")
	time.Sleep(time.Millisecond)
	engine.egraphMu.Unlock()
	got := <-done
	require.NoError(t, got.err)
	reply := got.reply
	require.True(t, reply.Committed)
	require.GreaterOrEqual(t, reply.PrepareLockWaits["sharing"], time.Millisecond, "the preparation waited behind the sharing pass")
	require.Len(t, reply.PrepareLockWaits, 1)
	require.Equal(t, reply.PrepareLockWaits["sharing"], reply.PrepareLockWait)
	require.Less(t, reply.PrepareLockWait, reply.Prepare, "its waits are part of its preparation")
	if reply.Release != nil {
		require.NoError(t, reply.Release(ctx))
	}
}

// A first use of a merged record is counted by the path it came by: a
// call's decode of its hit by lookup, an export's capture of what it sends
// by export.
func TestFirstUsesCountByPath(t *testing.T) {
	t.Parallel()
	set := firstUseTestCloud(t)
	ctx, engine, srv := transferTestCache(t)
	reply, err := engine.MergeValues(ctx, cloudCacheID, set.bundle)
	require.NoError(t, err)
	require.Empty(t, engine.FirstUse().Paths, "a merge prepares nothing")

	hit, _ := firstUseTestLookup(t, ctx, engine, srv, "fu-good")
	require.True(t, hit)
	engine.egraphMu.RLock()
	bad := engine.resultsByID[sharedResultID(firstUseTestEntry(t, set.bundle, reply, set.bad))]
	engine.egraphMu.RUnlock()
	require.True(t, bad.holdsUnpreparedPayload())
	exportTestBundle(t, ctx, engine, Result[Typed]{shared: bad})

	stats := engine.FirstUse()
	lookup, export := stats.Paths["lookup"], stats.Paths["export"]
	require.Equal(t, int64(1), lookup.Prepared, "the lookup prepared its hit")
	require.Positive(t, lookup.Total)
	require.Equal(t, lookup.Total, lookup.Longest)
	require.Positive(t, export.Prepared, "the export prepared what it captured")
	require.Equal(t, stats.Prepared+stats.Refused, lookup.Prepared+export.Prepared, "every first use came by one of the two")
	require.Len(t, stats.Paths, 2)
}

// A pass over a class of a merged record and a value computed here with no
// part to give is counted as a class with no donor's. The merged record is
// prepared by a sharing pass's probe once, this one's or, where a record
// alone in its class is queued too, its own class's before: every first use
// by sharing is a pass's.
func TestSharingCountsANoDonorClassWithItsFirstUses(t *testing.T) {
	set := firstUseTestCloud(t)
	ctx, c, srv, _ := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	reply, err := c.MergeValues(ctx, cloudCacheID, set.bundle)
	require.NoError(t, err)
	c.egraphMu.RLock()
	merged := c.resultsByID[sharedResultID(firstUseTestEntry(t, set.bundle, reply, set.good))]
	c.egraphMu.RUnlock()
	computed := persistedListTestResult(t, ctx, c, srv, "computed-beside", &transferTestValue{Text: "ready"})
	shareTestUnite(t, ctx, c, "share-no-donor", Result[Typed]{shared: merged}, computed)
	shareTestAwaitQuiet(t, c, barrier)

	stats := c.Sharing()
	noDonor := stats.Kinds["no_donor"]
	require.Positive(t, noDonor.Passes, "%+v", stats.Kinds)
	require.GreaterOrEqual(t, noDonor.Members, 2*noDonor.Passes)
	require.Zero(t, noDonor.Slotted)
	require.False(t, merged.holdsUnpreparedPayload(), "a pass's probe prepared the merged record")
	var prepared int64
	var took time.Duration
	for _, kind := range stats.Kinds {
		prepared += kind.Prepared
		took += kind.PrepareTotal
	}
	require.Positive(t, prepared)
	sharing := c.FirstUse().Paths["sharing"]
	require.Equal(t, sharing.Prepared, prepared, "the passes made every first use by sharing")
	require.Equal(t, sharing.Total, took, "and spent all their time")
}

// A pass over a class with a member holding a part ready to give is counted
// as a donor class's, with the slot it made.
func TestSharingCountsADonorClassWithItsSlot(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	donorValue := &transferTestValue{Text: "snapshot", links: []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "donor-snapshot"}}}
	donor := persistedListTestResult(t, ctx, c, srv, "donor", donorValue)
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	shareTestEncodedReceiver(t, ctx, c, receiver)
	partTestEquivalent(t, c, receiver, donor)
	shareTestUnite(t, ctx, c, "share-counts", donor, receiver)

	require.Equal(t, 1, barrier.awaitPass(t), "one planned slot")
	shareTestAwaitQuiet(t, c, barrier)
	// The install queues the class again, for a pass with nothing left to
	// give.
	donorPasses := c.Sharing().Kinds["donor"]
	require.Positive(t, donorPasses.Passes)
	require.Equal(t, 2*donorPasses.Passes, donorPasses.Members, "each pass probed both members")
	require.Equal(t, int64(1), donorPasses.Slotted, "one pass made a slot")
	require.Equal(t, int64(1), donorPasses.Slots)
	require.Zero(t, donorPasses.Prepared, "nothing merged was prepared")
}

// shareTestAwaitQuiet waits until no sharing pass is queued and none ends for
// a while.
func shareTestAwaitQuiet(t *testing.T, c *Cache, barrier *sharePassBarrier) {
	t.Helper()
	for quiet := false; !quiet; {
		if pending, _ := shareTestQueueDepth(c); pending > 0 {
			barrier.awaitPass(t)
			continue
		}
		select {
		case <-barrier.passes:
		case <-time.After(300 * time.Millisecond):
			quiet = true
		}
	}
}

// Every exclusive take of the graph lock on a call's path, and in the
// service's holds and the engine's own passes, names its kind of work, so a
// lookup that waits behind it can tell what held it: none is left "other".
func TestCacheWritersNameTheirKind(t *testing.T) {
	ctx := cacheTestContext(context.Background())
	c, err := NewCache(ctx, filepath.Join(t.TempDir(), "cache.db"), nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	takes := lockTakes(t, c)

	call := func(field string) AnyResult {
		t.Helper()
		frame := cacheTestIntCall(field)
		res, err := c.GetOrInitCall(ctx, "kinds", noopTypeResolver{}, &CallRequest{ResultCall: frame, IsPersistable: true}, ValueFunc(cacheTestIntResult(frame, 1)))
		require.NoError(t, err)
		return res
	}
	parent := call("kinds-parent")
	dep := call("kinds-dep")
	call("kinds-parent")
	require.NoError(t, c.TeachContentDigest(ctx, parent, digest.FromString("kinds-content")))
	require.NoError(t, c.addExplicitDependency(ctx, parent, dep, "test"))
	require.NoError(t, c.MakeResultUnpruneable(ctx, dep))
	_, release, err := c.HoldEntry(ctx, uint64(parent.cacheSharedResult().id))
	require.NoError(t, err)
	require.NoError(t, release(ctx))
	require.NoError(t, c.ReleaseSession(ctx, "kinds"))
	require.NoError(t, c.persistCurrentState(ctx))
	_, err = c.Prune(ctx, []CachePrunePolicy{{All: true}})
	require.NoError(t, err)

	byKind := map[string][]string{}
	for name, marks := range takes() {
		for _, mark := range marks {
			kind := mark.kind.String()
			if !slices.Contains(byKind[kind], name) {
				byKind[kind] = append(byKind[kind], name)
			}
		}
	}
	require.Empty(t, byKind["other"], "takes that named no kind (all: %v)", byKind)
	for kind, taker := range map[string]string{
		"publication": "initCompletedResult",
		"teach":       "teachContentDigest",
		"dependency":  "addExplicitDependencies",
		"hold":        "HoldEntry",
		"upkeep":      "lockGraphAs",
	} {
		require.True(t, slices.ContainsFunc(byKind[kind], func(name string) bool { return strings.HasPrefix(name, taker) }), "%s took the graph lock as %s (all: %v)", taker, kind, byKind)
	}
}

// The remote cache's writers of the graph lock name their kind too: a
// restore's commit and the release of its holds, a hit's decode of a merged
// record, a lazy task's ownership, and a call's teaching of an equivalent
// call.
func TestRemoteCacheWritersNameTheirKind(t *testing.T) {
	set := firstUseTestCloud(t)
	ctx, c, srv, _ := shareTestCache(t)
	takes := lockTakes(t, c)

	reply, err := c.RestoreValues(ctx, set.bundle, nil, "test")
	require.NoError(t, err)
	hit, _ := firstUseTestLookup(t, ctx, c, srv, "fu-good")
	require.True(t, hit)
	require.NotNil(t, reply.Release)
	require.NoError(t, reply.Release(ctx))
	row := persistedListTestResult(t, ctx, c, srv, "kinds-lazy", &transferTestValue{Text: "lazy"})
	require.NoError(t, c.RunLazyTask(ctx, row, partTaskKey("obtain", PersistedPartAddress{Part: "kinds"}), LazyTaskSpec{Body: func(context.Context) error { return nil }}))
	alias := &ResultCall{Kind: ResultCallKindField, Field: "kinds-alias", Type: NewResultCallType((&transferTestValue{}).Type())}
	require.NoError(t, c.TeachCallEquivalentToResult(ctx, "test-session", alias, row))

	byKind := map[string][]string{}
	for name, marks := range takes() {
		for _, mark := range marks {
			kind := mark.kind.String()
			if !slices.Contains(byKind[kind], name) {
				byKind[kind] = append(byKind[kind], name)
			}
		}
	}
	require.Empty(t, byKind["other"], "takes that named no kind (all: %v)", byKind)
	for kind, taker := range map[string]string{
		"restore":    "CommitValueMerge",
		"lookup":     "updateDecodedPayloadBytes",
		"hold":       "releaseHeld",
		"evaluation": "runLazyTask",
		"teach":      "TeachCallEquivalentToResult",
	} {
		require.True(t, slices.ContainsFunc(byKind[kind], func(name string) bool { return strings.HasPrefix(name, taker) }), "%s took the graph lock as %s (all: %v)", taker, kind, byKind)
	}
}

// A first use, or a hold of the graph lock, is the work of the pathway that
// started it: a later pathway's mark doesn't replace the first's.
func TestCacheWorkKeepsTheFirstPathway(t *testing.T) {
	ctx := withCacheWork(context.Background(), workSharing)
	require.Equal(t, workSharing, cacheWorkOf(withCacheWork(ctx, workPart), workOther))
	require.Equal(t, workLookup, cacheWorkOf(context.Background(), workLookup))
}
