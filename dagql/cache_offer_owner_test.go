package dagql

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	set "github.com/hashicorp/go-set/v3"
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
	after := row.incomingOwnershipCount
	for _, offer := range row.partOffers {
		offer.record.Chain.Layers = nil
	}
	c.egraphMu.Unlock()
	require.Equal(t, before, after)
	_, err = c.CapturePersistedRecord(ctx, root)
	require.NoError(t, err)
}

func TestValueTransferOfferOwners(t *testing.T) {
	ctx, c, srv := persistedListTestCache(t, "")
	srv.InstallObject(NewClass(srv, ClassOpts[*transferTestValue]{}))
	r := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "r"}).cacheSharedResult()
	s := persistedListTestResult(t, ctx, c, srv, "service", String("s")).cacheSharedResult()
	dependent := persistedListTestResult(t, ctx, c, srv, "dependent", String("d")).cacheSharedResult()
	var base, initialOwners, replacedOwners, installedOwners, releasedOwners, retiredOwners int64
	var holds, slots int64
	var debugOwnerCount, replacementQueueCount, releasedQueueCount int
	var receiverRequirements, dependentRequirements set.TreeSet[SessionResourceHandle]
	var receiverRequiresSocket, dependentRequiresSocket bool
	var before, after uint64
	var cycleErr error
	var second *partOffer
	// Keep all intermediate ownership observations in the original critical
	// section. Return setup errors before asserting, so cleanup cannot retain E.
	err := func() error {
		c.egraphMu.Lock()
		defer c.egraphMu.Unlock()
		if err := c.addExplicitDependencyLocked(ctx, dependent, r, "test"); err != nil {
			return err
		}
		s.sessionResourceHandle = "socket"
		if _, err := c.recomputeRequiredSessionResourcesLocked(s); err != nil {
			return err
		}
		base = s.incomingOwnershipCount
		makeOffer := func(part PartKey, dep *sharedResult) (*partOffer, error) {
			owner, err := c.newOfferOwnerLocked(ctx, PersistedOfferOwner{DependencyIDs: []uint64{uint64(dep.id), uint64(dep.id)}})
			if err != nil {
				return nil, err
			}
			return &partOffer{owner: owner, record: PersistedPartOffer{Address: PersistedPartAddress{Part: part}, Owner: owner.record, Value: SnapshotValue{Kind: "directory", Services: []TransferredServiceBinding{{ServiceResultID: uint64(dep.id)}}}}}, nil
		}
		first, err := makeOffer("fs", s)
		if err != nil {
			return err
		}
		second, err = makeOffer("mount:/src", s)
		if err != nil {
			return err
		}
		if err := c.attachPartOfferLocked(r, first.record.Address, first); err != nil {
			return err
		}
		if err := c.attachPartOfferLocked(r, second.record.Address, second); err != nil {
			return err
		}
		initialOwners = s.incomingOwnershipCount
		if r.requiredSessionResources != nil {
			receiverRequirements = *r.requiredSessionResources
		}
		if dependent.requiredSessionResources != nil {
			dependentRequirements = *dependent.requiredSessionResources
		}
		debugOwnerCount = len(c.debugOfferOwnersLocked())
		c.retainOfferOwnerLocked(first.owner)
		replacement, err := makeOffer("fs", s)
		if err != nil {
			return err
		}
		queue, err := c.replacePartOfferLocked(ctx, r, replacement.record.Address, replacement)
		if err != nil {
			return err
		}
		replacementQueueCount = len(queue)
		holds, slots = first.owner.holds, first.owner.slots
		replacedOwners = s.incomingOwnershipCount
		if err := c.addExplicitDependencyLocked(ctx, r, s, "installed output"); err != nil {
			return err
		}
		receiverRequiresSocket = cacheTestSessionResourceSetContains(r.requiredSessionResources, "socket")
		dependentRequiresSocket = cacheTestSessionResourceSetContains(dependent.requiredSessionResources, "socket")
		installedOwners = s.incomingOwnershipCount
		queue, err = c.releaseOfferOwnerLocked(ctx, first.owner)
		if err != nil {
			return err
		}
		releasedQueueCount = len(queue)
		releasedOwners = s.incomingOwnershipCount
		cycle, err := makeOffer("fs", dependent)
		if err != nil {
			return err
		}
		before = r.transferRevision
		_, cycleErr = c.replacePartOfferLocked(ctx, r, cycle.record.Address, cycle)
		after = r.transferRevision
		if _, err := c.releaseOfferOwnerLocked(ctx, cycle.owner); err != nil {
			return err
		}
		if _, err := c.retirePartOfferLocked(ctx, r, replacement.record.Address); err != nil {
			return err
		}
		retiredOwners = s.incomingOwnershipCount
		return nil
	}()
	require.NoError(t, err)
	require.Equal(t, base+2, initialOwners)
	require.Empty(t, receiverRequirements)
	require.Empty(t, dependentRequirements)
	require.Equal(t, 2, debugOwnerCount)
	require.Zero(t, replacementQueueCount)
	require.Equal(t, int64(1), holds)
	require.Equal(t, int64(0), slots)
	require.Equal(t, base+3, replacedOwners)
	require.True(t, receiverRequiresSocket)
	require.True(t, dependentRequiresSocket)
	require.Equal(t, base+4, installedOwners)
	require.Zero(t, releasedQueueCount)
	require.Equal(t, base+3, releasedOwners)
	require.ErrorContains(t, cycleErr, "cycle")
	require.Equal(t, before, after)
	require.Equal(t, base+2, retiredOwners)

	snapshot := c.snapshotPruneState(nil, pruneSnapshotMetadata, 10)
	require.Len(t, snapshot.owners, 1)
	c.egraphMu.Lock()
	c.retainOfferOwnerLocked(second.owner)
	_, err = c.retirePartOfferLocked(ctx, r, second.record.Address)
	c.egraphMu.Unlock()
	require.NoError(t, err)
	snapshot = c.snapshotPruneState(nil, pruneSnapshotMetadata, 10)
	require.Contains(t, pruneActiveClosure(snapshot, nil), s.id)
	c.egraphMu.Lock()
	_, err = c.releaseOfferOwnerLocked(ctx, second.owner)
	ownerCount := len(c.offerOwners)
	finalOwners := s.incomingOwnershipCount
	c.egraphMu.Unlock()
	require.NoError(t, err)
	require.Zero(t, ownerCount)
	require.Equal(t, base+1, finalOwners)
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
	if err == nil {
		offer := &partOffer{owner: owner, record: PersistedPartOffer{Address: PersistedPartAddress{Part: "snapshot"}, Owner: owner.record, Value: SnapshotValue{Kind: "directory"}}}
		err = c.attachPartOfferLocked(r, offer.record.Address, offer)
	}
	c.egraphMu.Unlock()
	require.NoError(t, err)
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
