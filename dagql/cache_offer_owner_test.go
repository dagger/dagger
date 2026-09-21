package dagql

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/stretchr/testify/require"
)

func TestValueTransferOfferCopyFailure(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	root := persistedListTestResult(t, ctx, c, srv, "root", &transferTestValue{Text: "pending"})
	dep := persistedListTestResult(t, ctx, c, srv, "dep", String("dep"))
	transferTestOffer(t, c, ctx, root, dep)
	row := root.cacheSharedResult()
	invalid := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	c.egraphMu.Lock()
	for _, offer := range row.partOffers {
		offer.record.Chain.Layers = []snapshots.ExportLayer{{CreatedAt: &invalid}}
	}
	before := row.incomingOwnershipCount
	c.egraphMu.Unlock()
	_, err := c.CapturePersistedRecord(ctx, root)
	require.ErrorIs(t, err, ErrPersistStateNotReady)
	err = c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{root}}, config.RefConfig{}, func(context.Context, *ExportedValues) error {
		t.Fatal("invalid offer must not reach consumer")
		return nil
	})
	require.ErrorIs(t, err, ErrPersistStateNotReady)
	// Both failed captures released the graph lock and leaked no capture hold.
	c.egraphMu.Lock()
	require.Equal(t, before, row.incomingOwnershipCount)
	for _, offer := range row.partOffers {
		offer.record.Chain.Layers = nil
	}
	c.egraphMu.Unlock()
	_, err = c.CapturePersistedRecord(ctx, root)
	require.NoError(t, err)
}

func TestValueTransferOfferOwners(t *testing.T) {
	ctx, c, srv := persistedListTestCache(t, "")
	srv.InstallObject(NewClass(srv, ClassOpts[*transferTestValue]{}))
	r := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "r"}).cacheSharedResult()
	s := persistedListTestResult(t, ctx, c, srv, "service", String("s")).cacheSharedResult()
	dependent := persistedListTestResult(t, ctx, c, srv, "dependent", String("d")).cacheSharedResult()
	c.egraphMu.Lock()
	require.NoError(t, c.addExplicitDependencyLocked(ctx, dependent, r, "test"))
	s.sessionResourceHandle = "socket"
	_, err := c.recomputeRequiredSessionResourcesLocked(s)
	require.NoError(t, err)
	base := s.incomingOwnershipCount
	makeOffer := func(part PartKey, dep *sharedResult) *partOffer {
		owner, err := c.newOfferOwnerLocked(ctx, PersistedOfferOwner{DependencyIDs: []uint64{uint64(dep.id), uint64(dep.id)}})
		require.NoError(t, err)
		return &partOffer{owner: owner, record: PersistedPartOffer{Address: PersistedPartAddress{Part: part}, Owner: owner.record, Value: SnapshotValue{Kind: "directory", Services: []TransferredServiceBinding{{ServiceResultID: uint64(dep.id)}}}}}
	}
	first, second := makeOffer("fs", s), makeOffer("mount:/src", s)
	require.NoError(t, c.attachPartOfferLocked(r, first.record.Address, first))
	require.NoError(t, c.attachPartOfferLocked(r, second.record.Address, second))
	require.Equal(t, base+2, s.incomingOwnershipCount)
	require.Empty(t, r.requiredSessionResources)
	require.Empty(t, dependent.requiredSessionResources)
	require.Len(t, c.debugOfferOwnersLocked(), 2)
	c.retainOfferOwnerLocked(first.owner)
	replacement := makeOffer("fs", s)
	queue, err := c.replacePartOfferLocked(ctx, r, replacement.record.Address, replacement)
	require.NoError(t, err)
	require.Empty(t, queue)
	require.Equal(t, int64(1), first.owner.holds)
	require.Equal(t, int64(0), first.owner.slots)
	require.Equal(t, base+3, s.incomingOwnershipCount)
	require.NoError(t, c.addExplicitDependencyLocked(ctx, r, s, "installed output"))
	require.True(t, r.requiredSessionResources.Contains("socket"))
	require.True(t, dependent.requiredSessionResources.Contains("socket"))
	require.Equal(t, base+4, s.incomingOwnershipCount)
	queue, err = c.releaseOfferOwnerLocked(ctx, first.owner)
	require.NoError(t, err)
	require.Empty(t, queue)
	require.Equal(t, base+3, s.incomingOwnershipCount)
	cycle := makeOffer("fs", dependent)
	before := r.transferRevision
	_, err = c.replacePartOfferLocked(ctx, r, cycle.record.Address, cycle)
	require.ErrorContains(t, err, "cycle")
	require.Equal(t, before, r.transferRevision)
	_, err = c.releaseOfferOwnerLocked(ctx, cycle.owner)
	require.NoError(t, err)
	_, err = c.retirePartOfferLocked(ctx, r, replacement.record.Address)
	require.NoError(t, err)
	require.Equal(t, base+2, s.incomingOwnershipCount)
	c.egraphMu.Unlock()

	snapshot := c.snapshotPruneState(nil, pruneSnapshotMetadata, 10)
	require.Len(t, snapshot.owners, 1)
	c.egraphMu.Lock()
	c.retainOfferOwnerLocked(second.owner)
	_, err = c.retirePartOfferLocked(ctx, r, second.record.Address)
	require.NoError(t, err)
	c.egraphMu.Unlock()
	snapshot = c.snapshotPruneState(nil, pruneSnapshotMetadata, 10)
	require.Contains(t, pruneActiveClosure(snapshot, nil), s.id)
	c.egraphMu.Lock()
	_, err = c.releaseOfferOwnerLocked(ctx, second.owner)
	require.NoError(t, err)
	require.Empty(t, c.offerOwners)
	require.Equal(t, base+1, s.incomingOwnershipCount)
	c.egraphMu.Unlock()
}

func TestValueTransferOwnerPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, c, srv := persistedListTestCache(t, path)
	srv.InstallObject(NewClass(srv, ClassOpts[*transferTestValue]{}))
	r := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "r"}).cacheSharedResult()
	s := persistedListTestResult(t, ctx, c, srv, "service", String("s")).cacheSharedResult()
	c.egraphMu.Lock()
	r.imported = true
	owner, err := c.newOfferOwnerLocked(ctx, PersistedOfferOwner{DependencyIDs: []uint64{uint64(s.id)}})
	require.NoError(t, err)
	offer := &partOffer{owner: owner, record: PersistedPartOffer{Address: PersistedPartAddress{Part: "snapshot"}, Owner: owner.record, Value: SnapshotValue{Kind: "directory"}}}
	require.NoError(t, c.attachPartOfferLocked(r, offer.record.Address, offer))
	c.egraphMu.Unlock()
	require.NoError(t, c.ReleaseSession(ctx, "test-session"))
	_, err = c.removePersistedEdge(ctx, s.id)
	require.NoError(t, err)
	require.NoError(t, c.Close(ctx))
	ctx, c, _ = persistedListTestCache(t, path)
	require.Empty(t, c.PersistenceResetReason())
	restored := c.resultsByID[r.id]
	require.NotNil(t, restored)
	require.True(t, IsImportedResult(Result[Typed]{shared: restored}))
	require.Empty(t, restored.deps)
	require.Len(t, restored.partOffers, 1)
	require.Equal(t, int64(1), c.resultsByID[s.id].incomingOwnershipCount)
	require.Len(t, c.offerOwners, 1)
	removed, err := c.removePersistedEdge(context.WithoutCancel(ctx), r.id)
	require.NoError(t, err)
	require.True(t, removed)
	require.Empty(t, c.offerOwners)
	require.Nil(t, c.resultsByID[s.id])
}
