package dagql

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

// requireForwardedOffer checks one imported pending offer: the descriptor's
// Service and an explicit-only reference are owned by the offer, and the
// receiver keeps its own direct edge to the Service but not to the other.
func requireForwardedOffer(t *testing.T, c *Cache, id uint64) *offerOwner {
	t.Helper()
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	row := c.resultsByID[sharedResultID(id)]
	require.NotNil(t, row)
	require.Len(t, row.partOffers, 1)
	var offer *partOffer
	for _, slot := range row.partOffers {
		offer = slot
	}
	require.Len(t, offer.record.Value.Services, 1)
	service := offer.record.Value.Services[0].ServiceResultID
	require.Len(t, offer.record.Owner.DependencyIDs, 2)
	require.Contains(t, offer.record.Owner.DependencyIDs, service)
	explicit := offer.record.Owner.DependencyIDs[0]
	if explicit == service {
		explicit = offer.record.Owner.DependencyIDs[1]
	}
	require.Equal(t, offer.record.Owner.DependencyIDs, offer.owner.record.DependencyIDs)
	require.Contains(t, row.deps, sharedResultID(service), "the direct edge survives")
	require.NotContains(t, row.deps, sharedResultID(explicit), "the explicit-only reference stays offer-owned")
	for _, dep := range []uint64{service, explicit} {
		require.NotNil(t, c.resultsByID[sharedResultID(dep)])
		require.Positive(t, c.resultsByID[sharedResultID(dep)].incomingOwnershipCount)
	}
	require.Equal(t, int64(1), offer.owner.slots)
	require.Equal(t, int64(1), offer.owner.holds, "a fresh owner has only its slot hold")
	return offer.owner
}

func reopenTransferTestCache(t *testing.T, path string) (context.Context, *Cache) {
	t.Helper()
	ctx, c, srv := persistedListTestCache(t, path)
	srv.InstallObject(NewClass(srv, ClassOpts[*transferTestValue]{}))
	require.Empty(t, c.PersistenceResetReason())
	return ctx, c
}

func TestOfferPendingRestartAndForward(t *testing.T) {
	chain := newChainFixture(t, "restart")
	store := testutil.NewStore(t)

	// A accepts a live offer whose owner holds the Service named by the
	// descriptor and an explicit-only reference. The receiver also owns the
	// Service directly.
	actx, a, asrv := transferTestCache(t)
	receiver := persistedListTestResult(t, actx, a, asrv, "receiver", &transferTestValue{Text: "pending"})
	service := persistedListTestResult(t, actx, a, asrv, "service", String("service"))
	explicit := persistedListTestResult(t, actx, a, asrv, "explicit", String("explicit"))
	transferTestDependency(a, actx, receiver, service)
	offer := exhaustionOffer(chain, "renewal-key", false)
	offer.Value.Services = []TransferredServiceBinding{{ServiceResultID: uint64(service.cacheSharedResult().id), Hostname: "svc"}}
	offer.Owner.DependencyIDs = []uint64{uint64(explicit.cacheSharedResult().id), uint64(service.cacheSharedResult().id)}
	out, err := a.OfferParts(actx, receiver, []PersistedPartOffer{offer})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	requireForwardedOffer(t, a, uint64(receiver.cacheSharedResult().id))

	// A to B forwards the pending offer without typed decode or bytes.
	path := filepath.Join(t.TempDir(), "b.db")
	bctx, b := reopenTransferTestCache(t, path)
	mapping, err := b.ImportValues(bctx, exportTestBundle(t, actx, a, receiver))
	require.NoError(t, err)
	id := mapping[0].ResultID
	before := requireForwardedOffer(t, b, id)

	// A renewal pending at checkpoint is retired, not persisted.
	oldBridge, created, err := b.AttachRemoteCacheBridge()
	require.NoError(t, err)
	require.True(t, created)
	request := renewalTestRequest(t, chain.chain.Layers)
	oldPending := startRenewal(bctx, oldBridge, request)
	waitQueued(t, oldBridge, 1)
	oldRequest := takeNow(t, oldBridge)
	closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, b.Close(closeCtx))
	require.ErrorContains(t, within(t, oldPending).err, "bridge detached")

	// Restart restores a fresh owner with the same references and no request.
	bctx, b = reopenTransferTestCache(t, path)
	transport := newContentTestTransport()
	transport.serve("https://renewed.invalid/restart/0", chain.blobs[0])
	transport.serve("https://renewed.invalid/restart/1", chain.blobs[1])
	b.snapshotManager = store.Manager
	b.partContentSource = NewPartContentSource(transport)
	after := requireForwardedOffer(t, b, id)
	require.NotSame(t, before, after)
	require.Equal(t, before.record.DependencyIDs, after.record.DependencyIDs)
	bridge, created, err := b.AttachRemoteCacheBridge()
	require.NoError(t, err)
	require.True(t, created)
	require.NotEqual(t, oldBridge.epoch, bridge.epoch)
	taken := renewalConsumer(t, bridge, renewChain(chain))
	require.Equal(t, RenewalReplyDiscarded, bridge.ReplyRenewal(RenewalReply{ID: oldRequest.ID, Chain: oldRequest.Chain, Unavailable: true}), "an old engine's reply cannot enter")

	// B to C forwards the restored offer, still without any request.
	cctx, c, _ := transferTestCache(t)
	forwarded, err := c.ImportValues(cctx, exportTestBundle(t, bctx, b, Result[Typed]{shared: b.resultsByID[sharedResultID(id)]}))
	require.NoError(t, err)
	requireForwardedOffer(t, c, forwarded[0].ResultID)
	require.Zero(t, transport.total())
	require.Empty(t, taken())

	// Only a demand renews the expired address, through the new epoch.
	f := &exhaustionFixture{ctx: bctx, cache: b, transport: transport, receiver: Result[Typed]{shared: b.resultsByID[sharedResultID(id)]}, operation: new(renewalFallbackOperation), address: PersistedPartAddress{Part: "snapshot"}}
	f.demand = &PartDemandState{target: f.address}
	partLazyPreparationHooks.Store(id, func(context.Context) (LazyOperationInvocation, error) { return f.operation, nil })
	defer partLazyPreparationHooks.Delete(id)
	require.NoError(t, f.run())
	require.True(t, f.installed())
	require.Zero(t, f.operation.runs.Load())
	requests := taken()
	require.Len(t, requests, 1)
	require.Equal(t, bridge.epoch, requests[0].ID.Epoch)
	require.Equal(t, "renewal-key", requests[0].RenewalKey)
	for i := range chain.blobs {
		require.Equal(t, 1, transport.count(fmt.Sprintf("https://renewed.invalid/restart/%d", i)))
	}
	b.egraphMu.RLock()
	row := b.resultsByID[sharedResultID(id)]
	require.Empty(t, row.partOffers, "settlement retired the installed part's offer")
	require.Empty(t, b.offerOwners)
	b.egraphMu.RUnlock()
}

func TestRemoteCacheUnusedCost(t *testing.T) {
	chain := newChainFixture(t, "unused")
	// A nil config leaves no bridge; ranking allocates nothing for a
	// renewal-only offer and never requests content.
	f := newExhaustionFixture(t, chain)
	source := f.cache.PartContentSource()
	require.Nil(t, source.bridge.Load())
	renewalOnly := exhaustionOffer(chain, "key", false)
	now := time.Now()
	require.Zero(t, testing.AllocsPerRun(100, func() {
		if source.Available(renewalOnly, now) {
			panic("renewal-only offer available without a bridge")
		}
	}))
	persistedListTestResult(t, f.ctx, f.cache, f.srv, "ordinary-miss", String("computed"))
	require.Zero(t, f.transport.total(), "an ordinary miss never consults the source")

	// Fixed addresses still work, and no renewal state is created.
	fixed := exhaustionOffer(chain, "key", true)
	f.attach(t, f.receiver, fixed)
	require.True(t, source.Available(fixed, now))
	require.NoError(t, f.run())
	require.True(t, f.installed())
	require.Nil(t, source.bridge.Load())
	require.Nil(t, f.demand.renewals)
	require.Zero(t, f.demand.revision)
	for i := range chain.blobs {
		require.Equal(t, 1, f.transport.count(fmt.Sprintf("https://fixed.invalid/unused/%d", i)))
	}
	require.Equal(t, len(chain.blobs), f.transport.total())
}
