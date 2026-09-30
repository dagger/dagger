package dagql

import (
	"context"
	"errors"
	"testing"

	"github.com/containerd/containerd/v2/core/leases"
	cerrdefs "github.com/containerd/errdefs"
	snapshots "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

// failingAttachManager fails result owner lease attachment, as a failed
// publication does.
type failingAttachManager struct {
	snapshots.SnapshotManager
}

func (failingAttachManager) AttachLease(context.Context, string, string) error {
	return errors.New("injected owner lease failure")
}

type reusedSnapshotFixture struct {
	store      *testutil.Store
	cache      *Cache
	base       context.Context
	donor      snapshots.ImmutableRef
	donorOwner string
	snapshotID string
}

func newReusedSnapshotFixture(t *testing.T, failAttach bool) *reusedSnapshotFixture {
	t.Helper()
	store := testutil.NewStore(t)
	base := cacheTestContext(t.Context())
	base = ContextWithOperationLeaseProvider(base, OperationLeaseProviderFunc(func(ctx context.Context) (context.Context, func(context.Context) error, error) {
		return snapshots.WithLazyLease(ctx, store.Leases, snapshots.MakeTemporary)
	}))
	manager := store.Manager
	if failAttach {
		manager = failingAttachManager{SnapshotManager: store.Manager}
	}
	cache, err := NewCache(base, "", manager, nil)
	require.NoError(t, err)
	donor, donorOwner := store.Build(t, nil, "file", "content")
	return &reusedSnapshotFixture{
		store:      store,
		cache:      cache,
		base:       ContextWithCache(base, cache),
		donor:      donor,
		donorOwner: donorOwner,
		snapshotID: donor.SnapshotID(),
	}
}

// reuse reopens the donor's snapshot under the call's operation lease, as
// filesync's content-hash reuse does, and returns the operation lease ID.
func (f *reusedSnapshotFixture) reuse(t *testing.T, callCtx context.Context) (snapshots.ImmutableRef, string) {
	t.Helper()
	ref, err := f.store.Manager.LeaseExistingSnapshot(callCtx, f.snapshotID)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ref.Release(context.Background()) })
	leaseCtx, err := snapshots.EnsureLease(callCtx)
	require.NoError(t, err)
	operationID, _ := leases.FromContext(leaseCtx)
	return ref, operationID
}

func (f *reusedSnapshotFixture) releaseDonor(t *testing.T) {
	t.Helper()
	require.NoError(t, f.donor.Release(context.Background()))
	require.NoError(t, f.store.Manager.RemoveLease(context.Background(), f.donorOwner))
	f.store.GC(t)
}

func (f *reusedSnapshotFixture) requireLeaseGone(t *testing.T, leaseID string) {
	t.Helper()
	require.NotEmpty(t, leaseID)
	all, err := f.store.Leases.List(context.Background())
	require.NoError(t, err)
	for _, lease := range all {
		require.NotEqual(t, leaseID, lease.ID, "operation lease must be released")
	}
}

// requireCollected checks that GC removed the snapshot and that no lease
// still names it.
func (f *reusedSnapshotFixture) requireCollected(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	f.store.GC(t)
	_, err := f.store.Snapshots.Stat(ctx, f.snapshotID)
	require.True(t, cerrdefs.IsNotFound(err), "snapshot still exists: %v", err)
	all, err := f.store.Leases.List(ctx)
	require.NoError(t, err)
	for _, lease := range all {
		resources, err := f.store.Leases.ListResources(ctx, lease)
		require.NoError(t, err)
		for _, resource := range resources {
			require.NotEqual(t, f.snapshotID, resource.ID, "lease %s still references the snapshot", lease.ID)
		}
	}
}

func reusedSnapshotCall(field string) *CallRequest {
	return &CallRequest{
		ResultCall: &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType((&persistSnapshotValue{}).Type()),
			Field: field,
		},
		ConcurrencyKey: field,
	}
}

// A snapshot reused by content hash stays owned by the call from reuse until
// the published result's owner lease holds it, even when its donor is
// released and GC runs after the resolver returns and before publication.
func TestCacheReusedSnapshotSurvivesUntilPublication(t *testing.T) {
	t.Parallel()
	for _, retained := range []bool{false, true} {
		name := "session"
		if retained {
			name = "retained"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newReusedSnapshotFixture(t, false)
			f.cache.testAfterCallbackWaiterCheck = func() { f.releaseDonor(t) }
			req := reusedSnapshotCall("reused-published")
			req.IsPersistable = retained
			var reused snapshots.ImmutableRef
			var operationID string
			_, err := f.cache.GetOrInitCall(f.base, "test-session", noopTypeResolver{}, req, func(callCtx context.Context) (AnyResult, error) {
				reused, operationID = f.reuse(t, callCtx)
				return cacheTestPlainResult(&persistSnapshotValue{Name: "reused", SnapshotID: f.snapshotID}), nil
			})
			require.NoError(t, err)
			f.requireLeaseGone(t, operationID)
			f.store.GC(t)
			testutil.CheckFile(t, reused, "file", "content")

			require.NoError(t, f.cache.ReleaseSession(f.base, "test-session"))
			if retained {
				// A retained result keeps its snapshot after its session closes.
				f.store.GC(t)
				testutil.CheckFile(t, reused, "file", "content")
				_, err := f.cache.Prune(f.base, []CachePrunePolicy{{All: true}})
				require.NoError(t, err)
			}
			f.requireCollected(t)
		})
	}
}

// A reused snapshot whose result is never published is released with the
// call's operation lease.
func TestCacheReusedSnapshotReleasedWhenAbandoned(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"cancel-after-callback", "cancel-before-callback-return", "publication-fails"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newReusedSnapshotFixture(t, mode == "publication-fails")
			ctx, cancel := context.WithCancelCause(f.base)
			defer cancel(nil)
			waiterDone := make(chan struct{})
			abandon := func() {
				cancel(errors.New("caller left"))
				<-waiterDone
			}
			if mode == "cancel-after-callback" {
				f.cache.testAfterCallbackWaiterCheck = abandon
			}
			req := reusedSnapshotCall("reused-" + mode)
			calls := make(chan *ongoingCall, 1)
			var operationID string
			_, err := f.cache.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, req, func(callCtx context.Context) (AnyResult, error) {
				f.cache.callsMu.Lock()
				for _, oc := range f.cache.ongoingCalls {
					calls <- oc
				}
				f.cache.callsMu.Unlock()
				_, operationID = f.reuse(t, callCtx)
				if mode == "cancel-before-callback-return" {
					abandon()
				}
				return cacheTestPlainResult(&persistSnapshotValue{Name: "reused", SnapshotID: f.snapshotID}), nil
			})
			close(waiterDone)
			<-(<-calls).waitCh
			if mode == "publication-fails" {
				require.ErrorContains(t, err, "injected owner lease failure")
			} else {
				require.ErrorContains(t, err, "caller left")
			}
			f.requireLeaseGone(t, operationID)

			require.NoError(t, f.cache.ReleaseSession(f.base, "test-session"))
			f.releaseDonor(t)
			f.requireCollected(t)
		})
	}
}

// Two sessions reuse the same snapshot for one recipe. The later publication
// adopts the earlier one's entry and discards its own value; its operation
// lease goes, and the adopted entry keeps the snapshot.
func TestCacheReusedSnapshotAdoptedPublication(t *testing.T) {
	t.Parallel()
	f := newReusedSnapshotFixture(t, false)
	// Without a concurrency key, each session runs its own call of the recipe.
	req := func() *CallRequest {
		req := reusedSnapshotCall("reused-adopted")
		req.ConcurrencyKey = ""
		return req
	}

	aReused, aFinish := make(chan struct{}), make(chan struct{})
	var aOperationID string
	aDone := make(chan error, 1)
	go func() {
		_, err := f.cache.GetOrInitCall(f.base, "session-a", noopTypeResolver{}, req(), func(callCtx context.Context) (AnyResult, error) {
			_, aOperationID = f.reuse(t, callCtx)
			close(aReused)
			<-aFinish
			return cacheTestPlainResult(&persistSnapshotValue{Name: "a", SnapshotID: f.snapshotID}), nil
		})
		aDone <- err
	}()
	<-aReused

	var reused snapshots.ImmutableRef
	var bOperationID string
	resB, err := f.cache.GetOrInitCall(f.base, "session-b", noopTypeResolver{}, req(), func(callCtx context.Context) (AnyResult, error) {
		reused, bOperationID = f.reuse(t, callCtx)
		return cacheTestPlainResult(&persistSnapshotValue{Name: "b", SnapshotID: f.snapshotID}), nil
	})
	require.NoError(t, err)
	f.releaseDonor(t)
	close(aFinish)
	require.NoError(t, <-aDone)

	f.cache.egraphMu.RLock()
	require.Len(t, f.cache.resultsByID, 1, "session A adopted session B's entry")
	f.cache.egraphMu.RUnlock()
	require.NotZero(t, resB.cacheSharedResult().id)
	f.requireLeaseGone(t, aOperationID)
	f.requireLeaseGone(t, bOperationID)
	f.store.GC(t)
	testutil.CheckFile(t, reused, "file", "content")

	require.NoError(t, f.cache.ReleaseSession(f.base, "session-a"))
	require.NoError(t, f.cache.ReleaseSession(f.base, "session-b"))
	f.requireCollected(t)
}
