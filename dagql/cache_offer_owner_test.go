package dagql

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
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
	for _, offer := range row.testPartOffers() {
		offer.record.Chain.Layers = []snapshots.ExportLayer{{CreatedAt: &invalid}}
	}
	before := row.incomingOwnershipCount.Load()
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
	after := row.incomingOwnershipCount.Load()
	for _, offer := range row.testPartOffers() {
		offer.record.Chain.Layers = nil
	}
	c.egraphMu.Unlock()
	require.Equal(t, before, after)
	_, err = c.CapturePersistedRecord(ctx, root)
	require.NoError(t, err)
}

// A copied offer equals the original and shares none of its maps, slices or
// pointers, whatever fields its types gain: StoredBundle relocates the copy of
// a stored part and removes its addresses and renewal key, and neither may
// reach the part.
func TestClonePartOffersShareNothing(t *testing.T) {
	var offer PersistedPartOffer
	fillCloneTestValue(reflect.ValueOf(&offer).Elem())
	require.NotEmpty(t, offer.Chain.Layers[0].Descriptor.Annotations)
	copied, err := clonePartOffers([]PersistedPartOffer{offer})
	require.NoError(t, err)
	require.Equal(t, offer, copied[0])
	requireNothingShared(t, reflect.ValueOf(offer), reflect.ValueOf(copied[0]), "offer")
}

// fillCloneTestValue sets every exported field v reaches to a value that
// isn't zero, with one element in each slice and map.
func fillCloneTestValue(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fillCloneTestValue(v.Elem())
	case reflect.Struct:
		if v.Type() == reflect.TypeFor[time.Time]() {
			v.Set(reflect.ValueOf(time.Unix(1_760_000_000, 0).UTC()))
			return
		}
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				fillCloneTestValue(v.Field(i))
			}
		}
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		fillCloneTestValue(v.Index(0))
	case reflect.Map:
		key, elem := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
		fillCloneTestValue(key)
		fillCloneTestValue(elem)
		v.Set(reflect.MakeMap(v.Type()))
		v.SetMapIndex(key, elem)
	case reflect.String:
		v.SetString("set")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	}
}

// requireNothingShared fails if a and b share a map, a slice's elements or a
// pointer's target through their exported fields.
func requireNothingShared(t *testing.T, a, b reflect.Value, path string) {
	t.Helper()
	switch a.Kind() {
	case reflect.Pointer:
		if a.IsNil() {
			return
		}
		require.NotEqual(t, a.Pointer(), b.Pointer(), "%s is shared", path)
		requireNothingShared(t, a.Elem(), b.Elem(), path)
	case reflect.Struct:
		for i := range a.NumField() {
			if field := a.Type().Field(i); field.IsExported() {
				requireNothingShared(t, a.Field(i), b.Field(i), path+"."+field.Name)
			}
		}
	case reflect.Slice:
		if a.Len() == 0 {
			return
		}
		require.NotEqual(t, a.Pointer(), b.Pointer(), "%s is shared", path)
		for i := range a.Len() {
			requireNothingShared(t, a.Index(i), b.Index(i), fmt.Sprintf("%s[%d]", path, i))
		}
	case reflect.Map:
		if a.Len() == 0 {
			return
		}
		require.NotEqual(t, a.Pointer(), b.Pointer(), "%s is shared", path)
		for _, key := range a.MapKeys() {
			requireNothingShared(t, a.MapIndex(key), b.MapIndex(key), fmt.Sprintf("%s[%v]", path, key))
		}
	}
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
		base = s.incomingOwnershipCount.Load()
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
		if err := c.testAttachPartOfferLocked(r, first.record.Address, first); err != nil {
			return err
		}
		if err := c.testAttachPartOfferLocked(r, second.record.Address, second); err != nil {
			return err
		}
		initialOwners = s.incomingOwnershipCount.Load()
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
		queue, err := c.testReplacePartOfferLocked(ctx, r, replacement.record.Address, replacement)
		if err != nil {
			return err
		}
		replacementQueueCount = len(queue)
		holds, slots = first.owner.holds, first.owner.slots
		replacedOwners = s.incomingOwnershipCount.Load()
		if err := c.addExplicitDependencyLocked(ctx, r, s, "installed output"); err != nil {
			return err
		}
		receiverRequiresSocket = cacheTestSessionResourceSetContains(r.requiredSessionResources, "socket")
		dependentRequiresSocket = cacheTestSessionResourceSetContains(dependent.requiredSessionResources, "socket")
		installedOwners = s.incomingOwnershipCount.Load()
		queue, err = c.releaseOfferOwnerLocked(ctx, first.owner)
		if err != nil {
			return err
		}
		releasedQueueCount = len(queue)
		releasedOwners = s.incomingOwnershipCount.Load()
		cycle, err := makeOffer("fs", dependent)
		if err != nil {
			return err
		}
		before = r.transferRevision
		_, cycleErr = c.testReplacePartOfferLocked(ctx, r, cycle.record.Address, cycle)
		after = r.transferRevision
		if _, err := c.releaseOfferOwnerLocked(ctx, cycle.owner); err != nil {
			return err
		}
		if _, err := c.retirePartOfferLocked(ctx, r, replacement.record.Address); err != nil {
			return err
		}
		retiredOwners = s.incomingOwnershipCount.Load()
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

	snapshot := c.snapshotPruneState(pruneSnapshotMetadata, 10)
	require.Len(t, snapshot.owners, 1)
	c.egraphMu.Lock()
	c.retainOfferOwnerLocked(second.owner)
	_, err = c.retirePartOfferLocked(ctx, r, second.record.Address)
	c.egraphMu.Unlock()
	require.NoError(t, err)
	snapshot = c.snapshotPruneState(pruneSnapshotMetadata, 10)
	require.Contains(t, pruneActiveClosure(snapshot, nil), s.id)
	c.egraphMu.Lock()
	_, err = c.releaseOfferOwnerLocked(ctx, second.owner)
	ownerCount := len(c.offerOwners)
	finalOwners := s.incomingOwnershipCount.Load()
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
		err = c.testAttachPartOfferLocked(r, offer.record.Address, offer)
	}
	c.egraphMu.Unlock()
	require.NoError(t, err)
	require.NoError(t, c.ReleaseSession(ctx, "test-session"))
	_, _, err = c.removePersistedEdge(ctx, s.id)
	require.NoError(t, err)
	require.NoError(t, c.Close(ctx))
	ctx, c, _ = persistedListTestCache(t, path)
	require.Empty(t, c.PersistenceResetReason())
	restored := c.resultsByID[r.id]
	require.NotNil(t, restored)
	require.True(t, IsImportedResult(Result[Typed]{shared: restored}))
	require.Empty(t, restored.deps)
	require.Len(t, restored.testPartOffers(), 1)
	require.Equal(t, int64(1), c.resultsByID[s.id].incomingOwnershipCount.Load())
	require.Len(t, c.offerOwners, 1)
	_, removed, err := c.removePersistedEdge(context.WithoutCancel(ctx), r.id)
	require.NoError(t, err)
	require.True(t, removed)
	require.Empty(t, c.offerOwners)
	require.Nil(t, c.resultsByID[s.id])
}
