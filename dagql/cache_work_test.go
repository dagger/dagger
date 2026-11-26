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

	lookupBehind(workOffer, "lookupBehindOffer")
	landed := c.Landing()
	lookupBehind(workRestore, "lookupBehindRestore")
	landed()
	landed() // A second end of the same landing changes nothing.
	lookupBehind(workExport, "lookupBehindExport")
	require.NoError(t, <-lockWaitTestCall(ctx, c, cacheTestIntCall("lookupAtOnce"), nil))

	waits := c.LookupLockWaits()
	require.Equal(t, LookupLockWaits{
		Other: map[string]LockWaitStats{
			"offer":  waits.Other["offer"],
			"export": waits.Other["export"],
		},
		Landing: map[string]LockWaitStats{
			"restore": waits.Landing["restore"],
		},
	}, waits, "only the three waits, each by its writer's kind and window")
	for name, stats := range map[string]LockWaitStats{"offer": waits.Other["offer"], "export": waits.Other["export"], "restore": waits.Landing["restore"]} {
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
	engine.lockGraphFor(ctx, workOffer)
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
	require.GreaterOrEqual(t, reply.PrepareLockWaits["offer"], time.Millisecond, "the preparation waited behind the offer")
	require.Len(t, reply.PrepareLockWaits, 1)
	require.Equal(t, reply.PrepareLockWaits["offer"], reply.PrepareLockWait)
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
// restore's commit and the release of its holds, a lazy task's ownership,
// and a call's teaching of an equivalent call. A hit's decode of a merged
// record takes the lock only for reading, so it names none.
func TestRemoteCacheWritersNameTheirKind(t *testing.T) {
	set := firstUseTestCloud(t)
	ctx, c, srv, _ := partTestCache(t)
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
		"hold":       "releaseHeld",
		"evaluation": "runLazyTask",
		"teach":      "TeachCallEquivalentToResult",
	} {
		require.True(t, slices.ContainsFunc(byKind[kind], func(name string) bool { return strings.HasPrefix(name, taker) }), "%s took the graph lock as %s (all: %v)", taker, kind, byKind)
	}
	for kind, names := range byKind {
		require.False(t, slices.ContainsFunc(names, func(name string) bool { return strings.HasPrefix(name, "updateDecodedPayloadBytes") }), "the hit's decode took the graph lock for writing, as %s", kind)
	}
}

// A first use, or a hold of the graph lock, is the work of the pathway that
// started it: a later pathway's mark doesn't replace the first's.
func TestCacheWorkKeepsTheFirstPathway(t *testing.T) {
	ctx := withCacheWork(context.Background(), workOffer)
	require.Equal(t, workOffer, cacheWorkOf(withCacheWork(ctx, workPart), workOther))
	require.Equal(t, workLookup, cacheWorkOf(context.Background(), workLookup))
}
