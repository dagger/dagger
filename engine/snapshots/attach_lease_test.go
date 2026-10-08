package snapshots_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/leases"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// blockAdd makes the store's AddResource for lease, on a resource match
// accepts, wait until the returned release is called. entered closes when
// the first such call arrives.
func blockAdd(store *testutil.Store, lease string, match func(leases.Resource) bool) (entered <-chan struct{}, release func()) {
	enteredCh, releaseCh := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	store.BeforeAdd = func(ctx context.Context, l leases.Lease, r leases.Resource) error {
		if l.ID != lease || !match(r) {
			return nil
		}
		enterOnce.Do(func() { close(enteredCh) })
		select {
		case <-releaseCh:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	return enteredCh, func() { releaseOnce.Do(func() { close(releaseCh) }) }
}

func isSnapshot(r leases.Resource) bool { return r.Type != "content" }

func waitFor[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

// A slow lease write in AttachLease does not stall a reopen of an unrelated
// snapshot: the containerd calls run outside the manager's lock.
func TestAttachLeaseDoesNotBlockSnapshotReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	attached, _ := store.Build(t, nil, "a.txt", "attached")
	other, _ := store.Build(t, nil, "b.txt", "reopened")

	const owner = "dagql/result/slow-owner"
	entered, release := blockAdd(store, owner, isSnapshot)
	defer release()
	attachErr := make(chan error, 1)
	go func() { attachErr <- store.Manager.AttachLease(ctx, owner, attached.SnapshotID()) }()
	waitFor(t, entered, "AttachLease to reach its lease write")

	reopened := make(chan error, 1)
	go func() {
		ref, err := store.Manager.GetBySnapshotID(ctx, other.SnapshotID(), bkcache.NoUpdateLastUsed)
		if err == nil {
			err = ref.Release(ctx)
		}
		reopened <- err
	}()
	require.NoError(t, waitFor(t, reopened, "the reopen while the lease write is blocked"))

	release()
	require.NoError(t, waitFor(t, attachErr, "AttachLease"))
	_, snapshots, _ := leaseResources(t, store, owner)
	require.Equal(t, []string{attached.SnapshotID()}, snapshots)
	require.NoError(t, store.Manager.RemoveLease(ctx, owner))
}

// A blob recorded for a snapshot while AttachLease is attaching it ends up
// in the owner lease: AttachLease rechecks the snapshot's blobs before it
// records the owner, and a blob recorded after that is attached to the owner
// by the recording.
func TestAttachLeaseHoldsBlobRecordedDuringAttach(t *testing.T) {
	t.Parallel()

	t.Run("before the snapshot is attached", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		store := testutil.NewStore(t)
		built, buildOwner := buildSnapshot(t, store, nil, "c.txt", "diffed during attach")
		snapshotID := built.SnapshotID()

		const owner = "dagql/result/owner-snapshot"
		entered, release := blockAdd(store, owner, isSnapshot)
		defer release()
		attachErr := make(chan error, 1)
		go func() { attachErr <- store.Manager.AttachLease(ctx, owner, snapshotID) }()
		waitFor(t, entered, "AttachLease to reach the snapshot resource")

		// The first export diffs the snapshot and records the blob, before
		// AttachLease reads the snapshot's blobs.
		exported := make(chan error, 1)
		var blob string
		go func() {
			chain, err := built.ExportChain(ctx, uncompressed)
			if err == nil {
				blob = chain.Layers[0].Descriptor.Digest.String()
				err = chain.Release(ctx)
			}
			exported <- err
		}()
		require.NoError(t, waitFor(t, exported, "the export while AttachLease is blocked"))

		release()
		require.NoError(t, waitFor(t, attachErr, "AttachLease"))
		_, _, contents := leaseResources(t, store, owner)
		require.Equal(t, []string{blob}, contents, "AttachLease attached the blob recorded before it read the blobs")

		require.NoError(t, built.Release(ctx))
		require.NoError(t, store.Manager.RemoveLease(ctx, buildOwner))
		store.GC(t)
		require.True(t, snapshotPresent(t, store, snapshotID))
		require.NoError(t, store.Manager.RemoveLease(ctx, owner))
		store.GC(t)
		require.False(t, snapshotPresent(t, store, snapshotID))
	})

	t.Run("while its blobs are attached", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		store := testutil.NewStore(t)
		built, buildOwner := buildSnapshot(t, store, nil, "d.txt", "variant during attach")
		snapshotID := built.SnapshotID()
		recorded, _ := exportOnce(t, store, snapshotID)

		// Block on the recorded blob, which AttachLease has read.
		const owner = "dagql/result/owner-blobs"
		entered, release := blockAdd(store, owner, func(r leases.Resource) bool {
			return r.Type == "content" && r.ID == recorded.String()
		})
		defer release()
		attachErr := make(chan error, 1)
		go func() { attachErr <- store.Manager.AttachLease(ctx, owner, snapshotID) }()
		waitFor(t, entered, "AttachLease to reach the recorded blob")

		// A forced export records a gzip variant for the snapshot, which
		// AttachLease did not read: its recheck attaches the variant.
		exported := make(chan error, 1)
		var variant string
		go func() {
			chain, err := built.ExportChain(ctx, config.RefConfig{Compression: compression.New(compression.Gzip).SetForce(true)})
			if err == nil {
				variant = chain.Layers[0].Descriptor.Digest.String()
				err = chain.Release(ctx)
			}
			exported <- err
		}()
		require.NoError(t, waitFor(t, exported, "the forced export while AttachLease is blocked"))
		require.NotEqual(t, recorded.String(), variant)

		release()
		require.NoError(t, waitFor(t, attachErr, "AttachLease"))
		_, _, contents := leaseResources(t, store, owner)
		require.ElementsMatch(t, []string{recorded.String(), variant}, contents, "the owner holds the recorded blob and the variant recorded while it attached")

		require.NoError(t, built.Release(ctx))
		require.NoError(t, store.Manager.RemoveLease(ctx, buildOwner))
		store.GC(t)
		require.True(t, blobPresent(t, store, recorded))
		require.NoError(t, store.Manager.RemoveLease(ctx, owner))
	})
}

// Many owners attach and remove leases on one chain at once, alongside
// reopens: each remaining owner holds every snapshot of the chain and its
// blob, the removed ones hold nothing, and the chain lives exactly as long
// as an owner does.
func TestAttachLeaseConcurrentOwners(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	parent, parentOwner := buildSnapshot(t, store, nil, "p.txt", "parent")
	child, childOwner := buildSnapshot(t, store, parent, "c.txt", "child")
	chain, err := child.ExportChain(ctx, uncompressed)
	require.NoError(t, err)
	require.Len(t, chain.Layers, 2)
	blobs := []string{chain.Layers[0].Descriptor.Digest.String(), chain.Layers[1].Descriptor.Digest.String()}
	require.NoError(t, chain.Release(ctx))

	const owners = 16
	var wg sync.WaitGroup
	errs := make(chan error, 3*owners)
	for i := range owners {
		wg.Add(2)
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("dagql/result/concurrent-%d", i)
			if err := store.Manager.AttachLease(ctx, id, child.SnapshotID()); err != nil {
				errs <- err
				return
			}
			if i%2 == 1 {
				errs <- store.Manager.RemoveLease(ctx, id)
			}
		}()
		go func() {
			defer wg.Done()
			ref, err := store.Manager.GetBySnapshotID(ctx, parent.SnapshotID(), bkcache.NoUpdateLastUsed)
			if err == nil {
				err = ref.Release(ctx)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	for i := range owners {
		id := fmt.Sprintf("dagql/result/concurrent-%d", i)
		if i%2 == 1 {
			all, err := store.Leases.List(ctx, "id=="+id)
			require.NoError(t, err)
			require.Empty(t, all, "removed owner %s is gone", id)
			continue
		}
		_, snapshots, contents := leaseResources(t, store, id)
		require.ElementsMatch(t, []string{parent.SnapshotID(), child.SnapshotID()}, snapshots, "owner %s holds the chain", id)
		require.ElementsMatch(t, blobs, contents, "owner %s holds the chain's blobs", id)
	}

	require.NoError(t, child.Release(ctx))
	require.NoError(t, parent.Release(ctx))
	require.NoError(t, store.Manager.RemoveLease(ctx, childOwner))
	require.NoError(t, store.Manager.RemoveLease(ctx, parentOwner))
	store.GC(t)
	require.True(t, snapshotPresent(t, store, child.SnapshotID()))
	require.True(t, snapshotPresent(t, store, parent.SnapshotID()))
	for i := 0; i < owners; i += 2 {
		require.NoError(t, store.Manager.RemoveLease(ctx, fmt.Sprintf("dagql/result/concurrent-%d", i)))
	}
	store.GC(t)
	require.False(t, snapshotPresent(t, store, child.SnapshotID()))
	require.False(t, snapshotPresent(t, store, parent.SnapshotID()))
	for _, blob := range blobs {
		require.False(t, blobPresent(t, store, digest.Digest(blob)))
	}
}

// A failed lease write fails AttachLease with the write's error. The lease
// is not recorded as an owner of a snapshot whose blobs it failed to take, so
// an export of the snapshot meanwhile neither writes to it nor fails on it. A
// retry attaches the whole chain, and removing the owner releases it.
func TestAttachLeaseWriteFailure(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		match func(leases.Resource) bool
	}{
		{"snapshot", isSnapshot},
		{"blob", func(r leases.Resource) bool { return r.Type == "content" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := testutil.NewStore(t)
			built, buildOwner := buildSnapshot(t, store, nil, "e.txt", "attach fails once")
			snapshotID := built.SnapshotID()
			recorded, _ := exportOnce(t, store, snapshotID)

			const owner = "dagql/result/fails"
			injected := errors.New("injected lease write failure")
			store.BeforeAdd = func(_ context.Context, l leases.Lease, r leases.Resource) error {
				if l.ID == owner && tc.match(r) {
					return injected
				}
				return nil
			}
			err := store.Manager.AttachLease(ctx, owner, snapshotID)
			require.ErrorIs(t, err, injected)
			require.ErrorContains(t, err, owner)
			_, _, contents := leaseResources(t, store, owner)
			require.Empty(t, contents, "the failed attach holds no blob")

			// A forced export records a gzip variant of the snapshot with
			// the failing writes still in place.
			forced, err := built.ExportChain(ctx, config.RefConfig{Compression: compression.New(compression.Gzip).SetForce(true)})
			require.NoError(t, err, "the export does not write to the failed owner")
			variant := forced.Layers[0].Descriptor.Digest
			require.NotEqual(t, recorded, variant)
			require.NoError(t, forced.Release(ctx))
			_, _, contents = leaseResources(t, store, owner)
			require.Empty(t, contents, "the failed owner did not take the variant")

			store.BeforeAdd = nil
			require.NoError(t, store.Manager.AttachLease(ctx, owner, snapshotID))
			_, snapshots, contents := leaseResources(t, store, owner)
			require.Equal(t, []string{snapshotID}, snapshots)
			require.ElementsMatch(t, []string{recorded.String(), variant.String()}, contents)

			require.NoError(t, built.Release(ctx))
			require.NoError(t, store.Manager.RemoveLease(ctx, buildOwner))
			store.GC(t)
			require.True(t, snapshotPresent(t, store, snapshotID), "the retried owner keeps the snapshot")
			require.True(t, blobPresent(t, store, recorded))
			require.NoError(t, store.Manager.RemoveLease(ctx, owner))
			store.GC(t)
			require.False(t, snapshotPresent(t, store, snapshotID))
			require.False(t, blobPresent(t, store, recorded))
			require.False(t, blobPresent(t, store, variant))
		})
	}
}
