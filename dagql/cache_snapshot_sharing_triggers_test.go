package dagql

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// shareTestSettledPair is a donor and an imported receiver already united
// under content, with the receiver filled and both passes that follow a union
// (the install and its empty successor) finished. What a test does next is the
// only thing that can queue their class again.
func shareTestSettledPair(t *testing.T, ctx context.Context, c *Cache, srv *Server, barrier *sharePassBarrier, label string) (donor, receiver AnyResult, content digest.Digest) {
	t.Helper()
	donor, receiver = shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
		map[string]sharePartState{"fs": {}},
	)
	shareTestUnite(t, ctx, c, label, donor, receiver)
	require.Equal(t, 1, barrier.awaitPass(t))
	require.Equal(t, 0, barrier.awaitPass(t))
	// Both passes also reported their start; leave nothing for a later wait.
	require.Equal(t, 1, barrier.awaitEntered(t))
	require.Equal(t, 0, barrier.awaitEntered(t))
	pending, _ := shareTestQueueDepth(c)
	require.Zero(t, pending)
	return donor, receiver, digest.FromString(label)
}

// shareTestItemMembers reports a cohort's members by row ID.
func shareTestItemMembers(item *snapshotShareItem) []sharedResultID {
	var ids []sharedResultID
	for id := range item.members {
		ids = append(ids, id)
	}
	return ids
}

// The two fault-injected observations of the publication trigger. Indexing a
// fresh result into a class is a membership notification, collected for the
// graph-lock interval of the publication and flushed before its first unlock.
func TestSnapshotSharingPublicationTriggerFaults(t *testing.T) {
	// A publication that fails inside that interval is rolled back before the
	// flush. The flush enumerates the class's registered members, so the
	// cohort it queues holds the survivors and never the removed row.
	t.Run("rollback inside the indexing interval", func(t *testing.T) {
		ctx, c, srv, _ := shareTestCache(t)
		barrier := newSharePassBarrier(c)
		donor, receiver, content := shareTestSettledPair(t, ctx, c, srv, barrier, "rolled-back-publication")

		// The cohort's members as the pass takes them; it clears them when it
		// releases its holds.
		var taken []sharedResultID
		passHook := c.testBeforeSharePass
		c.testBeforeSharePass = func(item *snapshotShareItem, planned int) {
			taken = shareTestItemMembers(item)
			passHook(item, planned)
		}

		// The fault: the published value's frame names a structural ref whose
		// row is collected between the digest derivations and the indexing
		// interval, so the dependency pass inside the interval fails.
		doomedCtx := ContextWithCache(engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{ClientID: "doomed-client", SessionID: "doomed"}), c)
		doomedFrame := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType((&shareTestValue{}).Type()), Field: "doomed"}
		doomed, err := c.GetOrInitCall(doomedCtx, "doomed", srv, &CallRequest{ResultCall: doomedFrame}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(newShareTestValue("doomed", nil), doomedFrame)
		})
		require.NoError(t, err)
		doomedRow := doomed.cacheSharedResult()
		var registeredBefore int
		var collect sync.Once
		c.testBeforePublicationIndex = func(*ongoingCall) {
			collect.Do(func() {
				require.NoError(t, c.ReleaseSession(doomedCtx, "doomed"))
				c.egraphMu.RLock()
				registeredBefore = len(c.resultsByID)
				c.egraphMu.RUnlock()
			})
		}
		// The value joins the pair's class through its content digest, which
		// is what makes its indexing a notification for that class.
		valueFrame := &ResultCall{
			Kind: ResultCallKindField, Type: NewResultCallType((&shareTestValue{}).Type()), Field: "rolled-back-value",
			ExtraDigests: []call.ExtraDigest{{Digest: content, Label: "content"}},
			Args: []*ResultCallArg{{Name: "doomed", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef,
				ResultRef: &ResultCallRef{ResultID: uint64(doomedRow.id), shared: doomedRow}}}},
		}
		request := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType((&shareTestValue{}).Type()), Field: "rolled-back-request"}
		_, err = c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: request}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(newShareTestValue("published", map[string]sharePartState{"fs": {Snapshot: "other-snap"}}), valueFrame)
		})
		require.ErrorContains(t, err, "missing cached result")

		require.Equal(t, 0, barrier.awaitPass(t), "the rolled-back membership still queued its class")
		require.ElementsMatch(t, []sharedResultID{donor.cacheSharedResult().id, receiver.cacheSharedResult().id}, taken,
			"the cohort holds the survivors and not the removed row")
		c.egraphMu.RLock()
		require.Equal(t, registeredBefore, len(c.resultsByID), "the rolled-back row is not registered")
		c.egraphMu.RUnlock()
	})

	// The flush happens at the publication's first unlock, before its
	// dependencies are attached. If attachment then fails, the queue hold is
	// the row's only owner. It retains the row for exactly one pass, which
	// skips the unfinished publication, and releasing the cohort collects it.
	t.Run("attachment failure after the early flush", func(t *testing.T) {
		ctx, c, srv, _ := shareTestCache(t)
		srv.InstallObject(NewClass(srv, ClassOpts[*captureTestValue]{}))
		barrier := newSharePassBarrier(c)
		donor, receiver, content := shareTestSettledPair(t, ctx, c, srv, barrier, "failed-attachment")

		var taken []sharedResultID
		var retained bool
		passHook := c.testBeforeSharePass
		c.testBeforeSharePass = func(item *snapshotShareItem, planned int) {
			taken = shareTestItemMembers(item)
			passHook(item, planned)
		}
		release := barrier.holdPasses()
		defer release()

		attachFailed := errors.New("attachment failed")
		frame := &ResultCall{
			Kind: ResultCallKindField, Type: NewResultCallType((&captureTestValue{}).Type()), Field: "unattached",
			ExtraDigests: []call.ExtraDigest{{Digest: content, Label: "content"}},
		}
		// The request carries no content digest, or the lookup would hit the
		// pair instead of publishing.
		request := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType((&captureTestValue{}).Type()), Field: "unattached-request"}
		_, err := c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: request}, func(context.Context) (AnyResult, error) {
			return NewObjectResultForCall(&captureTestValue{attach: func(context.Context) error { return attachFailed }}, srv, frame)
		})
		require.ErrorIs(t, err, attachFailed)

		// The pass is parked at its start: the failed publication is still
		// registered, held by the cohort alone.
		require.Equal(t, 0, barrier.awaitEntered(t))
		require.Len(t, taken, 3, "the flush queued the fresh row with the pair")
		var unattached sharedResultID
		for _, id := range taken {
			if id != donor.cacheSharedResult().id && id != receiver.cacheSharedResult().id {
				unattached = id
			}
		}
		c.egraphMu.RLock()
		row := c.resultsByID[unattached]
		retained = row != nil && row.incomingOwnershipCount == 1
		c.egraphMu.RUnlock()
		require.True(t, retained, "the queue hold is the unfinished publication's only owner")

		release()
		require.Equal(t, 0, barrier.awaitPass(t), "the pass plans nothing for an unfinished publication")
		c.egraphMu.RLock()
		_, stillRegistered := c.resultsByID[unattached]
		c.egraphMu.RUnlock()
		require.False(t, stillRegistered, "releasing the cohort ended the retention")
	})
}

// Congruence repair is a union nobody asked for by name: teaching two parents
// equivalent makes the same field of each congruent, and the repair merges the
// children's output classes. That union is a trigger like any other, so an
// imported child is filled from its sibling without the two ever being united
// directly.
func TestSnapshotSharingCongruenceRepairTrigger(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	srv.InstallObject(NewClass(srv, ClassOpts[*shareTestValue]{}))
	barrier := newSharePassBarrier(c)
	view := func(parent AnyResult, value *shareTestValue) AnyResult {
		t.Helper()
		frame := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType(value.Type()), Field: "view",
			Receiver: &ResultCallRef{ResultID: uint64(parent.cacheSharedResult().id)}}
		res, err := c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(value, frame)
		})
		require.NoError(t, err)
		return res
	}
	donorParent := persistedListTestResult(t, ctx, c, srv, "repair-parent-a", String("a"))
	receiverParent := persistedListTestResult(t, ctx, c, srv, "repair-parent-b", String("b"))
	donor := view(donorParent, newShareTestValue("donor", map[string]sharePartState{"fs": {Snapshot: "fs-snap"}}))
	receiver := view(receiverParent, newShareTestValue("receiver", map[string]sharePartState{"fs": {}}))
	require.NotSame(t, donor.cacheSharedResult(), receiver.cacheSharedResult(), "distinct parents give distinct children")
	shareTestEncodedReceiver(t, ctx, c, receiver)
	pending, _ := shareTestQueueDepth(c)
	require.Zero(t, pending, "nothing relates the children yet")

	shareTestUnite(t, ctx, c, "repair-parents", donorParent, receiverParent)
	require.Equal(t, 1, barrier.awaitPass(t), "the repaired union queued the children's class")
	require.True(t, shareTestHasLink(receiver, "fs-snap"))
}

// A notification only records work under the graph lock and wakes the worker.
// It never waits for the worker, so an import and a lookup that both queue a
// class return while the worker is parked inside a pass.
func TestSnapshotSharingEnqueueDoesNotWaitForTheWorker(t *testing.T) {
	ctx, a, asrv := transferTestCache(t)
	bundle := exportTestBundle(t, ctx, a, persistedListTestResult(t, ctx, a, asrv, "held-import-root", String("value")))

	ctx, c, srv, _ := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	donor, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
		map[string]sharePartState{"fs": {}},
	)
	release := barrier.holdPasses()
	defer release()
	shareTestUnite(t, ctx, c, "held-worker", donor, receiver)
	require.Equal(t, 1, barrier.awaitEntered(t), "the worker is parked inside its first pass")

	returned := make(chan error, 2)
	go func() {
		_, err := c.ImportValues(ctx, bundle)
		returned <- err
	}()
	go func() {
		// A lookup that hits the receiver by a new digest inserts a
		// membership, which is the lookup's own trigger.
		alias := receiver.cacheSharedResult().loadResultCall().clone()
		alias.ExtraDigests = append(alias.ExtraDigests, call.ExtraDigest{Digest: digest.FromString("held-worker-alias"), Label: "alias"})
		_, err := c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: alias, IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return nil, errors.New("the alias must hit the receiver")
		})
		returned <- err
	}()
	for range 2 {
		select {
		case err := <-returned:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("an enqueue waited for the held worker")
		}
	}
	pending, _ := shareTestQueueDepth(c)
	require.Equal(t, 2, pending, "the imported class and the receiver's successor are queued behind the held pass")

	release()
	require.Equal(t, 1, barrier.awaitPass(t))
	require.True(t, shareTestHasLink(receiver, "fs-snap"))
}

// An export is a capture, and a capture never waits: a row with any task in
// flight answers ErrPersistStateNotReady, the documented "not now" that the
// checkpoint worker and OfferParts' dispositions already carry. A sharing
// pass holding a prepared slot is one such task and an ordinary demand's is
// another; the export fails the same way for both and succeeds as soon as the
// task ends. Nothing is published, held or lost by the refused export.
func TestExportDuringAnActiveTaskIsNotReady(t *testing.T) {
	export := func(ctx context.Context, c *Cache, res AnyResult) error {
		return c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{res}}, config.RefConfig{}, func(context.Context, *ExportedValues) error { return nil })
	}
	t.Run("a sharing pass paused before Commit", func(t *testing.T) {
		ctx, c, srv, _ := shareTestCache(t)
		c.EnableTransferFixtureParts()
		barrier := newSharePassBarrier(c)
		donor, receiver := shareTestPair(t, ctx, c, srv,
			map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
			map[string]sharePartState{"fs": {}},
		)
		armed, err := c.ArmTransferFixtureBarrier(FixtureBarrierRequest{Key: "held", Point: FixtureBeforeCommit, Action: FixturePause})
		require.NoError(t, err)
		released := false
		release := func() {
			if !released {
				released = true
				require.NoError(t, c.ReleaseTransferFixtureBarrier(armed.Key, armed.Generation))
			}
		}
		defer release()
		shareTestUnite(t, ctx, c, "export-during-pass", donor, receiver)
		wait, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_, err = c.WaitTransferFixtureBarrier(wait, armed.Key, armed.Generation)
		require.NoError(t, err)

		holds := shareTestHolds(c, receiver)
		require.ErrorIs(t, export(ctx, c, receiver), ErrPersistStateNotReady)
		require.Equal(t, holds, shareTestHolds(c, receiver), "the refused export kept nothing")

		release()
		require.Equal(t, 1, barrier.awaitPass(t))
		require.True(t, shareTestHasLink(receiver, "fs-snap"), "the pass was not disturbed")
		require.NoError(t, export(ctx, c, receiver))
	})
	t.Run("an ordinary task of the same row", func(t *testing.T) {
		ctx, c, srv := transferTestCache(t)
		row := persistedListTestResult(t, ctx, c, srv, "busy-row", &transferTestValue{Text: "value"})
		entered, leave := make(chan struct{}), make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- c.RunLazyTask(ctx, row, "obtain:busy", LazyTaskSpec{Body: func(context.Context) error {
				close(entered)
				<-leave
				return nil
			}})
		}()
		defer func() {
			select {
			case <-leave:
			default:
				close(leave)
			}
		}()
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("the task never started")
		}
		require.ErrorIs(t, export(ctx, c, row), ErrPersistStateNotReady)
		close(leave)
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("the task never ended")
		}
		require.NoError(t, export(ctx, c, row))
	})
}
