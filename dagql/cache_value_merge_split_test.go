package dagql

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// splitTestSource publishes a root with a dependency in a sending cache and
// exports it: two records.
func splitTestSource(t *testing.T) ValueBundle {
	t.Helper()
	ctx, c, srv := transferTestCache(t)
	root := persistedListTestResult(t, ctx, c, srv, "split-root", &transferTestValue{Text: "sent"})
	transferTestDependency(c, ctx, root, persistedListTestResult(t, ctx, c, srv, "split-dep", String("dep")))
	bundle := exportTestBundle(t, ctx, c, root)
	require.Len(t, bundle.Values, 2)
	return bundle
}

// splitTestReply returns reply without what differs between two equal
// commits: the clock and the length of the hold.
func splitTestReply(reply MergeReply) MergeReply {
	reply.CommitHold, reply.EngineTimeUnixNano = 0, 0
	return reply
}

// splitTestEntries returns the number of entries c holds.
func splitTestEntries(c *Cache) int {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return len(c.resultsByID)
}

// A prepared merge that is committed does what MergeValues does: into equal
// caches, the replies and the entries are the same. On an engine, a record
// whose entry holds a value merge keeps is prepared light, as MergeValues
// prepares it.
func TestPreparedValueMergeCommitsAsMergeValues(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"cloud", "engine", "engine-kept"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			bundle := splitTestSource(t)
			from := CacheID("cache-a")
			if target != "cloud" {
				from = cloudCacheID
			}
			newTarget := func() (context.Context, *Cache) {
				if target == "cloud" {
					return storedPartTestCache(t, WithBlobStore())
				}
				ctx, c, srv := transferTestCache(t)
				if target == "engine-kept" {
					persistedListTestResult(t, ctx, c, srv, "split-root", &transferTestValue{Text: "local"})
				}
				return ctx, c
			}

			mctx, merged := newTarget()
			want, err := merged.MergeValues(mctx, from, bundle)
			require.NoError(t, err)
			require.True(t, want.Committed)

			sctx, split := newTarget()
			prepared := 0
			split.testTransferPlanPrepared = func(i int) error {
				if i == 1 {
					prepared++
				}
				return nil
			}
			p, err := split.PrepareValueMerge(sctx, from, bundle)
			require.NoError(t, err)
			got, err := split.CommitValueMerge(sctx, p)
			require.NoError(t, err)
			require.True(t, got.Committed)
			require.Equal(t, 1, prepared, "prepared once")
			require.Equal(t, splitTestReply(want), splitTestReply(got))
			require.Equal(t, splitTestEntries(merged), splitTestEntries(split))
			total, sum := cacheTestPayloadTotalConsistent(split)
			require.Equal(t, sum, total)
		})
	}
}

// A prepared merge holds a cache operation, which Release ends: a Close
// waits for it, and returns once it is released. Release changes nothing in
// the cache, and the numbers the preparation reserved stay unused. A
// preparation that fails returns no prepared merge and holds no operation.
func TestPreparedValueMergeReleaseEndsItsOperation(t *testing.T) {
	t.Parallel()
	bundle := splitTestSource(t)
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	cloud.egraphMu.RLock()
	reserved := cloud.nextSharedResultID
	cloud.egraphMu.RUnlock()

	p, err := cloud.PrepareValueMerge(ctx, "cache-a", bundle)
	require.NoError(t, err)
	require.EqualValues(t, 1, cloud.activeGlobalOperations.Load(), "the prepared merge holds an operation")
	require.Zero(t, splitTestEntries(cloud), "preparing changes nothing")
	p.Release()
	require.Zero(t, cloud.activeGlobalOperations.Load(), "Release ends it")
	require.Zero(t, splitTestEntries(cloud), "Release changes nothing")
	total, sum := cacheTestPayloadTotalConsistent(cloud)
	require.Equal(t, sum, total)

	reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	for _, value := range reply.Values {
		require.GreaterOrEqual(t, value.Number, uint64(reserved)+uint64(len(bundle.Values)), "the released numbers are not reused")
	}

	bad, _ := mergeTestSource(t, "bad-root", "sent")
	mergeTestBadPayload(t, bad)
	failed, err := cloud.PrepareValueMerge(ctx, "cache-a", bad)
	require.ErrorContains(t, err, "codec dagql_test.Transfer")
	require.Nil(t, failed)
	require.Zero(t, cloud.activeGlobalOperations.Load(), "a failed preparation holds nothing")

	p, err = cloud.PrepareValueMerge(ctx, "cache-a", bundle)
	require.NoError(t, err)
	closing, closed := make(chan struct{}), make(chan error, 1)
	cloud.testAfterCacheClosing = func() { close(closing) }
	go func() { closed <- cloud.Close(context.Background()) }()
	<-closing
	require.False(t, cloud.operationsQuiescent(), "the prepared merge keeps the cache from quiescence")
	select {
	case err := <-closed:
		t.Fatalf("Close finished with a prepared merge open: %v", err)
	default:
	}
	p.Release()
	require.NoError(t, <-closed)
	require.Zero(t, cloud.activeGlobalOperations.Load())
}

// A commit decides by the cache as it stands when it commits, not as it stood
// when the merge was prepared.
func TestCommitValueMergeDecidesByTheCacheAtCommit(t *testing.T) {
	t.Parallel()

	// An entry merge would have kept expires after the preparation: the
	// commit prepares the bundle in full and installs its record.
	t.Run("kept-entry-expired", func(t *testing.T) {
		t.Parallel()
		bundle := splitTestSource(t)
		ctx, c, srv := transferTestCache(t)
		local := persistedListTestResult(t, ctx, c, srv, "split-root", &transferTestValue{Text: "local"})
		prepared := 0
		c.testTransferPlanPrepared = func(i int) error {
			if i == 1 {
				prepared++
			}
			return nil
		}
		p, err := c.PrepareValueMerge(ctx, cloudCacheID, bundle)
		require.NoError(t, err)
		mergeTestExpire(c, local.cacheSharedResult())
		reply, err := c.CommitValueMerge(ctx, p)
		require.NoError(t, err)
		require.True(t, reply.Committed)
		require.Equal(t, 2, prepared, "prepared light, then in full")
		c.egraphMu.RLock()
		res := c.resultsByID[sharedResultID(reply.Imported()[0].ResultID)]
		imported, envelope := res.imported, res.persistedEnvelope
		c.egraphMu.RUnlock()
		require.True(t, imported, "the record replaced the expired value")
		require.JSONEq(t, `{"text":"sent"}`, string(envelope.ObjectJSON))
	})

	// An entry of the root's recipe is published after the preparation: the
	// commit keeps its value instead of creating an entry.
	t.Run("entry-published", func(t *testing.T) {
		t.Parallel()
		bundle := splitTestSource(t)
		ctx, c, srv := transferTestCache(t)
		p, err := c.PrepareValueMerge(ctx, cloudCacheID, bundle)
		require.NoError(t, err)
		local := persistedListTestResult(t, ctx, c, srv, "split-root", &transferTestValue{Text: "local"})
		reply, err := c.CommitValueMerge(ctx, p)
		require.NoError(t, err)
		require.Equal(t, uint64(local.cacheSharedResult().id), reply.Imported()[0].ResultID, "the published entry keeps its value")
		c.egraphMu.RLock()
		imported := local.cacheSharedResult().imported
		c.egraphMu.RUnlock()
		require.False(t, imported)
	})

	// Another sender's merge of the same records commits after the
	// preparation: the commit lands on the entries it created, and adds its
	// own sender's holdings.
	t.Run("merged-meanwhile", func(t *testing.T) {
		t.Parallel()
		bundle := splitTestSource(t)
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		p, err := cloud.PrepareValueMerge(ctx, "cache-a", bundle)
		require.NoError(t, err)
		first, err := cloud.MergeValues(ctx, "cache-b", bundle)
		require.NoError(t, err)
		entries := splitTestEntries(cloud)
		reply, err := cloud.CommitValueMerge(ctx, p)
		require.NoError(t, err)
		require.True(t, reply.Committed)
		require.Equal(t, first.Imported(), reply.Imported(), "the same entries")
		require.Equal(t, entries, splitTestEntries(cloud), "the commit creates nothing")
		root := reply.Imported()[0].ResultID
		sender := HolderKey{Cache: "cache-a", Number: bundle.Values[int(bundle.Roots[0].Ordinal)-1].SenderNumber}
		cloud.egraphMu.RLock()
		held, _ := cloud.holdingLocked(sender)
		cloud.egraphMu.RUnlock()
		require.NotNil(t, held, "the sender holds the entry")
		require.Equal(t, root, uint64(held.id))
		require.Zero(t, cloud.activeGlobalOperations.Load())
	})
}

// A prepared merge honours cancellation: a preparation whose context is
// cancelled returns its cause and no prepared merge, and a commit whose
// context is cancelled, before it decides or while it waits on an
// attachment, returns its cause, changes nothing, and ends the prepared
// merge.
func TestPreparedValueMergeCancellation(t *testing.T) {
	t.Parallel()

	t.Run("preparing", func(t *testing.T) {
		t.Parallel()
		bundle := splitTestSource(t)
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		cloud.testTransferPlanPrepared = func(i int) error {
			if i == 1 {
				cancel()
			}
			return nil
		}
		p, err := cloud.PrepareValueMerge(ctx, "cache-a", bundle)
		require.ErrorIs(t, err, context.Canceled)
		require.Nil(t, p)
		require.Zero(t, cloud.activeGlobalOperations.Load())
		require.Zero(t, splitTestEntries(cloud))
	})

	// An engine that holds every record prepares them all light, with no
	// payload to check cancellation before.
	t.Run("preparing-all-light", func(t *testing.T) {
		t.Parallel()
		bundle := splitTestSource(t)
		ctx, c, srv := transferTestCache(t)
		persistedListTestResult(t, ctx, c, srv, "split-root", &transferTestValue{Text: "local"})
		persistedListTestResult(t, ctx, c, srv, "split-dep", String("local"))
		entries := splitTestEntries(c)
		cancelled, cancel := context.WithCancel(ctx)
		c.testTransferPlanPrepared = func(i int) error {
			if i == len(bundle.Values) {
				cancel()
			}
			return nil
		}
		p, err := c.PrepareValueMerge(cancelled, cloudCacheID, bundle)
		require.ErrorIs(t, err, context.Canceled)
		require.Nil(t, p)
		require.Zero(t, c.activeGlobalOperations.Load())
		require.Equal(t, entries, splitTestEntries(c))

		c.testTransferPlanPrepared = nil
		p, err = c.PrepareValueMerge(ctx, cloudCacheID, bundle)
		require.NoError(t, err)
		light := p.prep.light
		p.Release()
		require.Len(t, light, len(bundle.Values), "every record is prepared light")
	})

	t.Run("committing", func(t *testing.T) {
		t.Parallel()
		bundle := splitTestSource(t)
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		p, err := cloud.PrepareValueMerge(ctx, "cache-a", bundle)
		require.NoError(t, err)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		reply, err := cloud.CommitValueMerge(cancelled, p)
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, reply.Committed)
		require.Zero(t, splitTestEntries(cloud), "a cancelled commit changes nothing")
		require.Zero(t, cloud.activeGlobalOperations.Load(), "and ends the prepared merge")
		_, err = cloud.CommitValueMerge(ctx, p)
		require.ErrorIs(t, err, errValueMergeEnded)
	})

	t.Run("waiting-on-an-attachment", func(t *testing.T) {
		t.Parallel()
		actx, a := storedPartTestCache(t)
		bundle := exportTestBundle(t, actx, a, storedPartTestPublish(t, actx, a, "a", "split-attach", &captureTestValue{text: "sent"}))

		bctx, b := storedPartTestCache(t)
		frame := &ResultCall{Kind: ResultCallKindField, Field: "split-attach", Type: NewResultCallType((&captureTestValue{}).Type())}
		attachStarted, attachRelease := make(chan struct{}), make(chan struct{})
		local := &captureTestValue{text: "local", attach: func(context.Context) error {
			close(attachStarted)
			<-attachRelease
			return nil
		}}
		published := make(chan error, 1)
		go func() {
			_, err := b.GetOrInitCall(bctx, "b", noopTypeResolver{}, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
				return NewResultForCall(local, frame.clone())
			})
			published <- err
		}()
		<-attachStarted
		p, err := b.PrepareValueMerge(bctx, cloudCacheID, bundle)
		require.NoError(t, err)
		waited := make(chan sharedResultID, 1)
		b.testMergeWaitsOnAttachment = func(res *sharedResult) { waited <- res.id }
		ctx, cancel := context.WithCancel(bctx)
		committed := make(chan error, 1)
		go func() {
			_, err := b.CommitValueMerge(ctx, p)
			committed <- err
		}()
		localID := <-waited
		cancel()
		require.ErrorIs(t, <-committed, context.Canceled)
		require.Zero(t, b.activeGlobalOperations.Load(), "the commit ended the prepared merge")
		close(attachRelease)
		require.NoError(t, <-published)
		b.egraphMu.RLock()
		_, cloud := b.resultsByID[localID].cloudHoldingLocked()
		b.egraphMu.RUnlock()
		require.Nil(t, cloud, "the cancelled commit changed nothing")
	})
}

// A prepared merge has one terminal action, CommitValueMerge or Release:
// whichever comes first ends it, and every later one does nothing. A commit
// into another cache than the one that prepared it is refused, and leaves
// the prepared merge to its owner.
func TestPreparedValueMergeHasOneTerminalAction(t *testing.T) {
	t.Parallel()
	bundle := splitTestSource(t)
	prepare := func(t *testing.T) (context.Context, *Cache, *PreparedValueMerge) {
		t.Helper()
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		p, err := cloud.PrepareValueMerge(ctx, "cache-a", bundle)
		require.NoError(t, err)
		return ctx, cloud, p
	}

	t.Run("commit-twice", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, p := prepare(t)
		_, err := cloud.CommitValueMerge(ctx, p)
		require.NoError(t, err)
		entries := splitTestEntries(cloud)
		reply, err := cloud.CommitValueMerge(ctx, p)
		require.ErrorIs(t, err, errValueMergeEnded)
		require.False(t, reply.Committed)
		require.Equal(t, entries, splitTestEntries(cloud))
		require.Zero(t, cloud.activeGlobalOperations.Load())
	})

	t.Run("commit-then-release", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, p := prepare(t)
		_, err := cloud.CommitValueMerge(ctx, p)
		require.NoError(t, err)
		p.Release()
		require.Zero(t, cloud.activeGlobalOperations.Load(), "the release after a commit ends nothing more")
	})

	t.Run("release-then-commit", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, p := prepare(t)
		p.Release()
		p.Release()
		require.Zero(t, cloud.activeGlobalOperations.Load())
		_, err := cloud.CommitValueMerge(ctx, p)
		require.ErrorIs(t, err, errValueMergeEnded)
		require.Zero(t, splitTestEntries(cloud), "a released merge commits nothing")
		require.Zero(t, cloud.activeGlobalOperations.Load())
	})

	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		var p *PreparedValueMerge
		p.Release()
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		_, err := cloud.CommitValueMerge(ctx, p)
		require.Error(t, err)
	})

	t.Run("another-cache", func(t *testing.T) {
		t.Parallel()
		_, cloud, p := prepare(t)
		octx, other := storedPartTestCache(t, WithBlobStore())
		_, err := other.CommitValueMerge(octx, p)
		require.Error(t, err)
		require.NotErrorIs(t, err, errValueMergeEnded)
		require.Zero(t, splitTestEntries(other))
		require.Zero(t, other.activeGlobalOperations.Load())
		require.EqualValues(t, 1, cloud.activeGlobalOperations.Load(), "the prepared merge is still its owner's")
		p.Release()
		require.Zero(t, cloud.activeGlobalOperations.Load())
	})

	// A commit and a release racing: exactly one is the terminal action.
	t.Run("commit-and-release-race", func(t *testing.T) {
		t.Parallel()
		for range 20 {
			ctx, cloud, p := prepare(t)
			var wg sync.WaitGroup
			var reply MergeReply
			var err error
			start := make(chan struct{})
			wg.Go(func() {
				<-start
				reply, err = cloud.CommitValueMerge(ctx, p)
			})
			wg.Go(func() {
				<-start
				p.Release()
			})
			close(start)
			wg.Wait()
			require.Zero(t, cloud.activeGlobalOperations.Load())
			if err != nil {
				require.True(t, errors.Is(err, errValueMergeEnded), "unexpected error: %v", err)
				require.Zero(t, splitTestEntries(cloud))
				continue
			}
			require.True(t, reply.Committed)
			require.Equal(t, len(bundle.Values), splitTestEntries(cloud))
		}
	})
}
