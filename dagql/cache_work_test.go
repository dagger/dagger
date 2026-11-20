package dagql

import (
	"context"
	"sync"
	"testing"
	"time"

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
	waitBlockedIn(t, "prepareValueMerge(")
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

// A snapshot-sharing pass is counted by the kind of class it ran for, with
// the first uses its probes made: a merged record alone in its class is
// prepared by the pass's probe, which can make no slot.
func TestSharingCountsPassesByKindOfClass(t *testing.T) {
	set := firstUseTestCloud(t)
	ctx, c, _, _ := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	_, err := c.MergeValues(ctx, cloudCacheID, set.bundle)
	require.NoError(t, err)
	barrier.awaitPass(t)
	// Every pass, until none is queued and none ends for a while.
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

	stats := c.Sharing()
	require.Len(t, stats.Kinds, 1, "every merged record is alone in its class: %+v", stats.Kinds)
	alone := stats.Kinds["alone"]
	require.Positive(t, alone.Passes)
	require.Equal(t, alone.Passes, alone.Members)
	require.Zero(t, alone.Slotted)
	require.Zero(t, alone.Slots)
	require.Positive(t, alone.Prepared, "the probes prepared records nothing used")
	sharing := c.FirstUse().Paths["sharing"]
	require.Equal(t, sharing.Prepared, alone.Prepared, "the passes made every first use by sharing")
	require.Equal(t, sharing.Total, alone.PrepareTotal)
}

// A first use, or a hold of the graph lock, is the work of the pathway that
// started it: a later pathway's mark doesn't replace the first's.
func TestCacheWorkKeepsTheFirstPathway(t *testing.T) {
	ctx := withCacheWork(context.Background(), workSharing)
	require.Equal(t, workSharing, cacheWorkOf(withCacheWork(ctx, workPart), workOther))
	require.Equal(t, workLookup, cacheWorkOf(context.Background(), workLookup))
}
