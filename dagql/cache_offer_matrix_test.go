package dagql

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// onSecondRevisionRead runs fn between OfferParts' owner preparation and its
// publication: capture reads the output revision once, and the check after
// preparation reads it again.
func onSecondRevisionRead(object *transferTestValue, fn func()) {
	var reads atomic.Int32
	object.revisionHook = func() {
		if reads.Add(1) == 2 {
			fn()
		}
	}
}

func bumpGateRevision(c *Cache, row *sharedResult) {
	gate := row.partGate.loadOrCreate()
	c.egraphMu.Lock()
	gate.mu.Lock()
	gate.revision++
	gate.mu.Unlock()
	c.egraphMu.Unlock()
}

func TestOfferPartsPreparationWindow(t *testing.T) {
	blob := digest.FromString("content")
	offerWith := func(dep AnyResult) PersistedPartOffer {
		offer := testLiveOffer()
		offer.Owner.DependencyIDs = []uint64{uint64(dep.cacheSharedResult().id)}
		offer.Chain.Layers = []snapshots.ExportLayer{{Descriptor: ocispec.Descriptor{Digest: blob, Size: 7, MediaType: ocispec.MediaTypeImageLayer}}}
		return offer
	}
	t.Run("preparation holds references across collection", func(t *testing.T) {
		ctx, c, srv := transferTestCache(t)
		object := &transferTestValue{Text: "pending"}
		receiver := persistedListTestResult(t, ctx, c, srv, "receiver", object)
		dep := persistedListTestResult(t, ctx, c, srv, "dep", String("dep"))
		depRow := dep.cacheSharedResult()
		collected := false
		onSecondRevisionRead(object, func() {
			// Drop every other hold on the reference while only the prepared
			// owner protects it.
			require.NoError(t, c.ReleaseSession(ctx, "test-session"))
			_, err := c.removePersistedEdge(ctx, depRow.id)
			require.NoError(t, err)
			c.egraphMu.RLock()
			collected = c.resultsByID[depRow.id] != depRow
			c.egraphMu.RUnlock()
		})
		out, err := c.OfferParts(ctx, receiver, []PersistedPartOffer{offerWith(dep)})
		require.NoError(t, err)
		require.False(t, collected, "the preparation hold kept the reference registered")
		require.Equal(t, OfferAccepted, out[0].Outcome)
		row := receiver.cacheSharedResult()
		c.egraphMu.Lock()
		require.Equal(t, int64(1), depRow.incomingOwnershipCount, "only the published slot owns it")
		queue, err := c.retirePartOfferLocked(ctx, row, testLiveOffer().Address)
		require.NoError(t, err)
		callbacks, err := c.collectUnownedResultsLocked(ctx, queue)
		require.NoError(t, err)
		require.Nil(t, c.resultsByID[depRow.id], "the slot was its last owner")
		c.egraphMu.Unlock()
		require.NoError(t, runOnReleaseFuncs(ctx, callbacks))
	})
	for _, mode := range []string{"new offer", "same-owner refresh", "different-owner replacement"} {
		t.Run(mode+" refused after preparation", func(t *testing.T) {
			ctx, c, srv := transferTestCache(t)
			object := &transferTestValue{Text: "pending"}
			receiver := persistedListTestResult(t, ctx, c, srv, "receiver", object)
			dep := persistedListTestResult(t, ctx, c, srv, "dep", String("dep"))
			other := persistedListTestResult(t, ctx, c, srv, "other", String("other"))
			row := receiver.cacheSharedResult()
			key, _ := partAddressKey(testLiveOffer().Address)
			initial := offerWith(dep)
			var owner *offerOwner
			if mode != "new offer" {
				out, err := c.OfferParts(ctx, receiver, []PersistedPartOffer{initial})
				require.NoError(t, err)
				require.Equal(t, OfferAccepted, out[0].Outcome)
				owner = row.partOffers[key].owner
			}
			next := offerWith(dep)
			switch mode {
			case "same-owner refresh":
				next.Chain.Addresses = map[digest.Digest]BlobAddress{blob: {URL: "https://refresh.invalid/blob"}}
			case "different-owner replacement":
				next = offerWith(other)
			}
			c.egraphMu.RLock()
			depBase, otherBase, revision := dep.cacheSharedResult().incomingOwnershipCount, other.cacheSharedResult().incomingOwnershipCount, row.transferRevision
			c.egraphMu.RUnlock()
			onSecondRevisionRead(object, func() { bumpGateRevision(c, row) })
			out, err := c.OfferParts(ctx, receiver, []PersistedPartOffer{next})
			require.ErrorIs(t, err, ErrPersistStateNotReady)
			require.Equal(t, OfferUnavailable, out[0].Outcome)
			require.False(t, out[0].Replaced)
			c.egraphMu.RLock()
			defer c.egraphMu.RUnlock()
			require.Equal(t, revision, row.transferRevision, "nothing published")
			require.Equal(t, depBase, dep.cacheSharedResult().incomingOwnershipCount)
			require.Equal(t, otherBase, other.cacheSharedResult().incomingOwnershipCount)
			if mode == "new offer" {
				require.Empty(t, row.partOffers)
				require.Empty(t, c.offerOwners, "the prepared owner was released")
				return
			}
			require.Same(t, owner, row.partOffers[key].owner)
			require.Equal(t, initial, row.partOffers[key].record, "the slot keeps its record")
			require.Equal(t, int64(1), owner.holds, "the preparation hold was released exactly once")
			require.Len(t, c.offerOwners, 1)
		})
	}
}

type settlementFixture struct {
	ctx      context.Context
	cache    *Cache
	manager  *lifetimeFailManager
	receiver AnyResult
	first    AnyResult
	second   AnyResult
	offer    PersistedPartOffer
	address  PersistedPartAddress
	entered  chan struct{}
	resume   func()
}

func newSettlementFixture(t *testing.T) *settlementFixture {
	t.Helper()
	chain := newChainFixture(t, "settlement")
	store := testutil.NewStore(t)
	ctx, c, srv := transferTestCache(t)
	f := &settlementFixture{ctx: ctx, cache: c, manager: &lifetimeFailManager{SnapshotManager: store.Manager}, address: PersistedPartAddress{Part: "snapshot"}, entered: make(chan struct{})}
	c.snapshotManager = f.manager
	f.receiver = persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	f.first = persistedListTestResult(t, ctx, c, srv, "first", String("first"))
	f.second = persistedListTestResult(t, ctx, c, srv, "second", String("second"))
	partEncodedReceiver(t, ctx, c, f.receiver)
	release := make(chan struct{})
	var entered, resumed sync.Once
	f.resume = func() { resumed.Do(func() { close(release) }) }
	t.Cleanup(f.resume)
	provider := &testutil.Provider{InfoReaderProvider: chain.chain.Provider, BeforeRead: func(ctx context.Context, _ ocispec.Descriptor) error {
		entered.Do(func() { close(f.entered) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}}
	c.SetPartContentSource(lifetimeChainSource{provider})
	f.offer = PersistedPartOffer{Address: f.address, Value: SnapshotValue{Kind: "directory", Path: "/"}, Chain: OfferedChain{Layers: chain.lower.Layers}, Owner: PersistedOfferOwner{DependencyIDs: []uint64{uint64(f.first.cacheSharedResult().id)}}}
	out, err := c.OfferParts(ctx, f.receiver, []PersistedPartOffer{f.offer})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	return f
}

// acquire installs the receiver's own offer in an obtain task.
func (f *settlementFixture) acquire(syncReady <-chan struct{}, committed chan<- struct{}) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- f.cache.RunLazyTask(f.ctx, f.receiver, "obtain:settlement", LazyTaskSpec{OwnerSyncReady: syncReady, Body: func(ctx context.Context) error {
			source, err := f.cache.AcquireEquivalentPartSource(ctx, f.receiver, f.address)
			if err != nil {
				return err
			}
			if source == nil || source.offerOwner == nil {
				return errors.New("expected the receiver's own offer")
			}
			permit, _, err := f.cache.TryAcquire(ctx, f.receiver, f.address, PartTaskFromContext(ctx))
			if err != nil {
				return errors.Join(err, source.Release(ctx))
			}
			err = f.cache.installChainPart(ctx, f.receiver, source, permit, &PartDemandState{target: f.address})
			if committed != nil {
				close(committed)
			}
			return err
		}})
	}()
	return done
}

func (f *settlementFixture) replacement() PersistedPartOffer {
	offer := f.offer
	offer.Value.Path = "/replacement"
	offer.Owner.DependencyIDs = []uint64{uint64(f.second.cacheSharedResult().id)}
	return offer
}

func (f *settlementFixture) counts() (first, second int64) {
	f.cache.egraphMu.RLock()
	defer f.cache.egraphMu.RUnlock()
	return f.first.cacheSharedResult().incomingOwnershipCount, f.second.cacheSharedResult().incomingOwnershipCount
}

func (f *settlementFixture) requireSettled(t *testing.T, firstBase, secondBase int64) {
	t.Helper()
	row := f.receiver.cacheSharedResult()
	f.cache.egraphMu.RLock()
	require.Empty(t, row.partOffers, "settlement removed every current offer")
	require.Empty(t, f.cache.offerOwners)
	f.cache.egraphMu.RUnlock()
	first, second := f.counts()
	require.Equal(t, firstBase, first, "no double decrement")
	require.Equal(t, secondBase, second, "no double decrement")
	key, _ := partAddressKey(f.address)
	gate := row.partGate.loadOrCreate()
	gate.mu.Lock()
	require.Equal(t, PartComplete, gate.outputs[key].phase)
	gate.mu.Unlock()
	out, err := f.cache.OfferParts(f.ctx, f.receiver, []PersistedPartOffer{f.replacement()})
	require.NoError(t, err)
	require.Equal(t, OfferAlreadyComplete, out[0].Outcome)
	f.cache.egraphMu.RLock()
	require.Empty(t, row.partOffers, "a final part attracts no offer")
	f.cache.egraphMu.RUnlock()
}

func TestOfferSettlementReplacement(t *testing.T) {
	t.Run("replacement during the winning acquisition is retired", func(t *testing.T) {
		f := newSettlementFixture(t)
		firstBase, secondBase := f.counts()
		done := f.acquire(nil, nil)
		waitWithinT(t, f.entered, done)
		key, _ := partAddressKey(f.address)
		f.cache.egraphMu.RLock()
		winner := f.receiver.cacheSharedResult().partOffers[key].owner
		f.cache.egraphMu.RUnlock()
		out, err := f.cache.OfferParts(f.ctx, f.receiver, []PersistedPartOffer{f.replacement()})
		require.NoError(t, err)
		require.Equal(t, OfferAccepted, out[0].Outcome)
		require.True(t, out[0].Replaced)
		f.cache.egraphMu.RLock()
		require.Equal(t, int64(0), winner.slots)
		require.Equal(t, int64(1), winner.holds, "the admitted acquisition keeps its own hold")
		f.cache.egraphMu.RUnlock()
		f.resume()
		require.NoError(t, within(t, done))
		// The winner's owner ended with its acquisition; the replacement
		// ended at settlement.
		f.requireSettled(t, firstBase-1, secondBase)
	})
	t.Run("acceptance after commit is already complete", func(t *testing.T) {
		f := newSettlementFixture(t)
		firstBase, secondBase := f.counts()
		syncReady, committed := make(chan struct{}), make(chan struct{})
		var opened sync.Once
		defer opened.Do(func() { close(syncReady) })
		f.resume()
		done := f.acquire(syncReady, committed)
		waitWithinT(t, committed, done)
		out, err := f.cache.OfferParts(f.ctx, f.receiver, []PersistedPartOffer{f.replacement()})
		require.NoError(t, err)
		require.Equal(t, OfferAlreadyComplete, out[0].Outcome, "an installed output closes offer admission")
		f.cache.egraphMu.RLock()
		require.Len(t, f.receiver.cacheSharedResult().partOffers, 1, "the older slot waits for settlement")
		f.cache.egraphMu.RUnlock()
		opened.Do(func() { close(syncReady) })
		require.NoError(t, within(t, done))
		f.requireSettled(t, firstBase-1, secondBase)
	})
	for _, backref := range []bool{false, true} {
		name := "failed sync keeps the output, not the acquisition hold"
		if backref {
			name += ", with a back-reference to the receiver"
		}
		t.Run(name, func(t *testing.T) {
			f := newSettlementFixture(t)
			if backref {
				transferTestDependency(f.cache, f.ctx, f.first, f.receiver)
			}
			firstBase, secondBase := f.counts()
			f.resume()
			f.manager.fail.Store(true)
			require.ErrorIs(t, within(t, f.acquire(nil, nil)), errLifetimeSync)
			row := f.receiver.cacheSharedResult()
			key, _ := partAddressKey(f.address)
			f.cache.egraphMu.RLock()
			slot := row.partOffers[key]
			require.NotNil(t, slot, "settlement has not run")
			require.Equal(t, slot.owner.slots, slot.owner.holds, "the acquisition hold ended before sync")
			f.cache.egraphMu.RUnlock()
			require.NotEmpty(t, row.loadPayloadState().snapshotLinkIntent.Links, "the installed output is retained")
			out, err := f.cache.OfferParts(f.ctx, f.receiver, []PersistedPartOffer{f.replacement()})
			require.NoError(t, err)
			require.Equal(t, OfferAlreadyComplete, out[0].Outcome)
			gate := row.partGate.loadOrCreate()
			gate.mu.Lock()
			state := gate.outputs[key]
			gate.mu.Unlock()
			require.Equal(t, PartOutputInstalled, state.phase)
			// A later demand retries bookkeeping only; settlement then retires
			// the slot once.
			require.NoError(t, f.cache.joinPartInstallation(f.ctx, f.receiver, state.task))
			f.requireSettled(t, firstBase-1, secondBase)
		})
	}
}

// waitWithinT waits for ready, failing early if the task ends first.
func waitWithinT(t *testing.T, ready <-chan struct{}, done <-chan error) {
	t.Helper()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("task ended before the barrier: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the barrier")
	}
}

func TestOfferResourcesDoNotGateLookup(t *testing.T) {
	chain := newChainFixture(t, "resources")
	f := newExhaustionFixture(t, chain)
	c := f.cache
	row := f.receiver.cacheSharedResult()
	socket := persistedListTestResult(t, f.ctx, c, f.srv, "socket", String("socket"))
	c.egraphMu.Lock()
	socket.cacheSharedResult().sessionResourceHandle = "socket"
	_, err := c.recomputeRequiredSessionResourcesLocked(socket.cacheSharedResult())
	c.egraphMu.Unlock()
	require.NoError(t, err)
	lookup, err := c.partLookupFor(row)
	require.NoError(t, err)
	ordinary := func(session string) *sharedResult {
		c.egraphMu.Lock()
		defer c.egraphMu.Unlock()
		match := c.lookupMatchForCallLocked(row.loadResultCall(), lookup.recipe, lookup.self, lookup.inputs, time.Now().Unix())
		return c.selectLookupCandidateForSessionLocked(session, match.candidates)
	}
	require.Same(t, row, ordinary("no-socket"))
	generation := row.requiredSessionResourcesGen.Load()

	offer := exhaustionOffer(chain, "", true)
	socketID := uint64(socket.cacheSharedResult().id)
	offer.Value.Services = []TransferredServiceBinding{{ServiceResultID: socketID, Hostname: "svc"}}
	offer.Owner.DependencyIDs = []uint64{socketID}
	out, err := c.OfferParts(f.ctx, f.receiver, []PersistedPartOffer{offer})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	c.egraphMu.RLock()
	require.True(t, row.requiredSessionResources == nil || row.requiredSessionResources.Empty(), "offer-only resources are not lookup requirements")
	c.egraphMu.RUnlock()
	require.Equal(t, generation, row.requiredSessionResourcesGen.Load())
	require.Same(t, row, ordinary("no-socket"), "a session without the socket still hits the receiver")

	// An unauthorized demand skips only that offer and runs the next route.
	require.ErrorIs(t, f.run(), errRenewalFallbackRan)
	require.EqualValues(t, 1, f.operation.runs.Load())
	require.Zero(t, f.transport.total())
	c.egraphMu.RLock()
	require.Len(t, row.partOffers, 1, "skipping does not remove the slot")
	c.egraphMu.RUnlock()

	// An authorized demand installs the exact references; ordinary
	// requirements then propagate.
	session, err := partSession(f.ctx)
	require.NoError(t, err)
	require.NoError(t, c.BindSessionResource(f.ctx, session, "client", "socket", new(int)))
	f.demand = &PartDemandState{target: f.address}
	require.NoError(t, f.run())
	require.True(t, f.installed())
	require.EqualValues(t, 1, f.operation.runs.Load())
	c.egraphMu.RLock()
	require.Contains(t, row.deps, sharedResultID(socketID), "installed references are direct edges")
	require.True(t, row.requiredSessionResources.Contains("socket"))
	c.egraphMu.RUnlock()
	require.Greater(t, row.requiredSessionResourcesGen.Load(), generation)
	require.Nil(t, ordinary("no-socket"), "the installed output's requirement now gates lookup")
	require.Same(t, row, ordinary(session))
}

func TestCacheCloseWithShutdownError(t *testing.T) {
	cause := errors.New("remote cache integration did not stop")
	closeContext := func(t *testing.T) context.Context {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		return ctx
	}
	// These caches are closed by each case; a close that shares the once
	// returns the earlier result, so cleanup does not assert it.
	open := func(t *testing.T, path string) *Cache {
		ctx := cacheTestContext(t.Context())
		c, err := NewCache(ctx, path, nil, nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.CloseDiscardingPersistence() })
		srv := newDagqlServerForTest(t, &persistCodecRoot{})
		ctx = srvToContext(ContextWithCache(ctx, c), srv)
		persistedListTestResult(t, ctx, c, srv, "root", String("value"))
		return c
	}
	t.Run("seeded cause keeps the checkpoint dirty", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cache.db")
		c := open(t, path)
		err := c.CloseWithShutdownError(closeContext(t), cause)
		require.ErrorIs(t, err, cause)
		require.ErrorIs(t, c.Close(closeContext(t)), cause, "a later close returns the same failure")
		_, reopened, _ := persistedListTestCache(t, path)
		require.Equal(t, CachePersistenceResetUncleanShutdown, reopened.PersistenceResetReason())
	})
	t.Run("nil cause closes cleanly", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cache.db")
		c := open(t, path)
		require.NoError(t, c.CloseWithShutdownError(closeContext(t), nil))
		_, reopened, _ := persistedListTestCache(t, path)
		require.Equal(t, CachePersistenceResetNone, reopened.PersistenceResetReason())
	})
	t.Run("discarded cache ignores a later cause", func(t *testing.T) {
		c := open(t, filepath.Join(t.TempDir(), "cache.db"))
		require.NoError(t, c.CloseDiscardingPersistence())
		require.NoError(t, c.CloseWithShutdownError(closeContext(t), cause))
	})
}

func TestRenewalShutdownDrainsOwnership(t *testing.T) {
	chain := newChainFixture(t, "drain")
	closeAsync := func(c *Cache) <-chan error {
		closed := make(chan error, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		go func() {
			defer cancel()
			closed <- c.Close(ctx)
		}()
		return closed
	}
	requireReleased := func(t *testing.T, f *exhaustionFixture) {
		t.Helper()
		f.cache.egraphMu.RLock()
		defer f.cache.egraphMu.RUnlock()
		for _, owner := range f.cache.offerOwners {
			require.Equal(t, owner.slots, owner.holds, "no acquisition hold remains")
		}
		gate := f.receiver.cacheSharedResult().partGate.loadOrCreate()
		gate.mu.Lock()
		require.Empty(t, gate.writers, "no permit remains")
		gate.mu.Unlock()
	}
	t.Run("delivered renewal", func(t *testing.T) {
		f := newExhaustionFixture(t, chain)
		bridge := attachTestBridge(t, f.cache.PartContentSource())
		f.attach(t, f.receiver, exhaustionOffer(chain, "key", false))
		demanded := make(chan error, 1)
		go func() { demanded <- f.run() }()
		waitQueued(t, bridge, 1)
		request := takeNow(t, bridge)
		closed := closeAsync(f.cache)
		within(t, request.Done)
		require.Error(t, within(t, demanded), "the pending demand ends")
		require.ErrorIs(t, f.demand.causes(), ErrRenewalUnavailable)
		require.NoError(t, within(t, closed))
		live, queued := liveExchanges(bridge)
		require.Zero(t, live)
		require.Zero(t, queued)
		require.Zero(t, f.transport.total())
		requireReleased(t, f)
	})
	t.Run("active reader", func(t *testing.T) {
		f := newExhaustionFixture(t, chain)
		f.transport.opened = make(chan struct{}, 4)
		f.transport.fault("https://fixed.invalid/drain/0", contentTestFault{stallAfter: 0})
		f.attach(t, f.receiver, exhaustionOffer(chain, "key", true))
		demandCtx, cancelDemand := context.WithCancel(f.ctx)
		defer cancelDemand()
		f.ctx = demandCtx
		demanded := make(chan error, 1)
		go func() { demanded <- f.run() }()
		within(t, f.transport.opened)
		closing := make(chan struct{})
		f.cache.testAfterCacheClosing = func() { close(closing) }
		closed := closeAsync(f.cache)
		within(t, closing)
		select {
		case err := <-closed:
			t.Fatalf("close finished while a reader was active: %v", err)
		default:
		}
		cancelDemand()
		require.ErrorIs(t, within(t, demanded), context.Canceled)
		require.NoError(t, within(t, closed))
		require.Zero(t, f.transport.open.Load(), "the reader's response was closed")
		requireReleased(t, f)
	})
}
