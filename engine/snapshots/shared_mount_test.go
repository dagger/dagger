package snapshots_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/leases"
	cerrdefs "github.com/containerd/errdefs"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

func mountShared(t *testing.T, ref bkcache.ImmutableRef) (string, func()) {
	t.Helper()
	shared, ok := ref.(bkcache.SharedMounter)
	require.True(t, ok, "an immutable ref shares its read-only mounts")
	root, _, release, err := shared.MountShared(context.Background())
	require.NoError(t, err)
	return root, release
}

func readShared(t *testing.T, ref bkcache.ImmutableRef, name string) string {
	t.Helper()
	root, release := mountShared(t, ref)
	defer release()
	data, err := os.ReadFile(filepath.Join(root, name))
	require.NoError(t, err)
	return string(data)
}

func mountReleaser(t *testing.T, store *testutil.Store) bkcache.ReadOnlyMountReleaser {
	t.Helper()
	releaser, ok := store.Manager.(bkcache.ReadOnlyMountReleaser)
	require.True(t, ok)
	return releaser
}

func snapshotExists(t *testing.T, store *testutil.Store, id string) bool {
	t.Helper()
	_, err := store.Snapshots.Stat(context.Background(), id)
	if cerrdefs.IsNotFound(err) {
		return false
	}
	require.NoError(t, err)
	return true
}

// Every reader of a snapshot, through any ref to it, shares one view and one
// local mount, which is kept after its readers are done; another snapshot
// gets its own.
func TestSharedMountOneViewPerSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	a, aOwner := buildSnapshot(t, store, nil, "a.txt", "a")
	b, bOwner := buildSnapshot(t, store, nil, "b.txt", "b")
	aAgain, err := store.Manager.GetBySnapshotID(ctx, a.SnapshotID())
	require.NoError(t, err)

	root, release := mountShared(t, a)
	rootAgain, releaseAgain := mountShared(t, aAgain)
	require.Equal(t, root, rootAgain)
	require.Len(t, viewLeases(t, store), 1)
	release()
	releaseAgain()
	require.Equal(t, "a", readShared(t, a, "a.txt"))
	require.Equal(t, "a", readShared(t, aAgain, "a.txt"))
	require.Len(t, viewLeases(t, store), 1, "the idle mount is kept for its next reader")
	require.EqualValues(t, 1, store.LocalMounts.Load())

	require.Equal(t, "b", readShared(t, b, "b.txt"))
	require.Len(t, viewLeases(t, store), 2)
	require.EqualValues(t, 2, store.LocalMounts.Load())

	require.NoError(t, mountReleaser(t, store).ReleaseIdleSharedMounts())
	require.Empty(t, viewLeases(t, store))
	require.EqualValues(t, 2, store.LocalUnmounts.Load())

	for _, ref := range []bkcache.ImmutableRef{a, aAgain, b} {
		require.NoError(t, ref.Release(ctx))
	}
	require.NoError(t, store.Manager.RemoveLease(ctx, aOwner))
	require.NoError(t, store.Manager.RemoveLease(ctx, bOwner))
}

// An idle shared mount's view holds its snapshot, so garbage collection
// cannot reclaim the snapshot until the mount is released: when its linger
// runs out, or when idle mounts are released, as each collection pass does
// first.
func TestSharedMountHoldsSnapshotUntilReleased(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	store.WithSharedMounts(t, time.Hour, 0)

	held, heldOwner := buildSnapshot(t, store, nil, "a.txt", "a")
	require.Equal(t, "a", readShared(t, held, "a.txt"))
	require.NoError(t, held.Release(ctx))
	require.NoError(t, store.Manager.RemoveLease(ctx, heldOwner))
	store.GC(t)
	require.True(t, snapshotExists(t, store, held.SnapshotID()), "the idle mount's view holds the snapshot")
	require.NoError(t, mountReleaser(t, store).ReleaseIdleSharedMounts())
	store.GC(t)
	require.False(t, snapshotExists(t, store, held.SnapshotID()), "released, the snapshot is collected")

	store.WithSharedMounts(t, 20*time.Millisecond, 0)
	lingered, lingeredOwner := buildSnapshot(t, store, nil, "b.txt", "b")
	require.Equal(t, "b", readShared(t, lingered, "b.txt"))
	require.NoError(t, lingered.Release(ctx))
	require.NoError(t, store.Manager.RemoveLease(ctx, lingeredOwner))
	require.Eventually(t, func() bool { return len(viewLeases(t, store)) == 0 }, 10*time.Second, 10*time.Millisecond,
		"the idle mount is released once its linger runs out")
	store.GC(t)
	require.False(t, snapshotExists(t, store, lingered.SnapshotID()))
}

// At most the cap of idle shared mounts is kept; the least recently used is
// released first.
func TestSharedMountIdleCap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	store.WithSharedMounts(t, time.Hour, 1)
	a, aOwner := buildSnapshot(t, store, nil, "a.txt", "a")
	b, bOwner := buildSnapshot(t, store, nil, "b.txt", "b")

	require.Equal(t, "a", readShared(t, a, "a.txt"))
	require.Equal(t, "b", readShared(t, b, "b.txt"))
	require.Eventually(t, func() bool { return len(viewLeases(t, store)) == 1 }, 10*time.Second, 10*time.Millisecond)
	require.Equal(t, "b", readShared(t, b, "b.txt"))
	require.EqualValues(t, 2, store.LocalMounts.Load(), "the most recently used mount is kept")
	require.Equal(t, "a", readShared(t, a, "a.txt"))
	require.EqualValues(t, 3, store.LocalMounts.Load(), "the least recently used mount was released")

	require.NoError(t, a.Release(ctx))
	require.NoError(t, b.Release(ctx))
	require.NoError(t, store.Manager.RemoveLease(ctx, aOwner))
	require.NoError(t, store.Manager.RemoveLease(ctx, bOwner))
}

// Readers acquiring and releasing shared mounts while lingers run out, the
// cap evicts and idle mounts are released all read their files, and every
// mount made is unmounted once the manager waits for its releases.
func TestSharedMountConcurrentEviction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	store.WithSharedMounts(t, time.Millisecond, 2)
	names := []string{"a", "b", "c"}
	refs := make([]bkcache.ImmutableRef, len(names))
	owners := make([]string, len(names))
	for i, name := range names {
		refs[i], owners[i] = buildSnapshot(t, store, nil, name+".txt", name)
	}

	releaser := mountReleaser(t, store)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for g := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 50 {
				n := (g + i) % len(names)
				if i%10 == 0 {
					if err := releaser.ReleaseIdleSharedMounts(); err != nil {
						errs <- err
						return
					}
				}
				root, _, release, err := refs[n].(bkcache.SharedMounter).MountShared(ctx)
				if err != nil {
					errs <- err
					return
				}
				data, err := os.ReadFile(filepath.Join(root, names[n]+".txt"))
				release()
				if err != nil || string(data) != names[n] {
					errs <- errors.Join(err, errors.New("read "+string(data)))
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	releaser.WaitForBackgroundReleases()
	require.Empty(t, viewLeases(t, store))
	require.Equal(t, store.LocalMounts.Load(), store.LocalUnmounts.Load())
	for i, ref := range refs {
		require.NoError(t, ref.Release(ctx))
		require.NoError(t, store.Manager.RemoveLease(ctx, owners[i]))
	}
}

// Waiting for background releases, as the engine does before it shuts down,
// releases the idle shared mounts; a mount still in use is released when its
// reader is done, and later readers each get, and release, their own.
func TestSharedMountShutdownDrain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	inUse, inUseOwner := buildSnapshot(t, store, nil, "a.txt", "a")
	idle, idleOwner := buildSnapshot(t, store, nil, "b.txt", "b")

	_, release := mountShared(t, inUse)
	require.Equal(t, "b", readShared(t, idle, "b.txt"))
	require.Len(t, viewLeases(t, store), 2)

	mountReleaser(t, store).WaitForBackgroundReleases()
	require.Len(t, viewLeases(t, store), 1, "the idle mount is released")
	release()
	require.Empty(t, viewLeases(t, store), "the mount in use is released when its reader is done")

	require.Equal(t, "b", readShared(t, idle, "b.txt"))
	require.Empty(t, viewLeases(t, store), "a later reader's own mount is released with it")
	require.Equal(t, store.LocalMounts.Load(), store.LocalUnmounts.Load())

	require.NoError(t, inUse.Release(ctx))
	require.NoError(t, idle.Release(ctx))
	require.NoError(t, store.Manager.RemoveLease(ctx, inUseOwner))
	require.NoError(t, store.Manager.RemoveLease(ctx, idleOwner))
}

// Releasing the idle shared mounts, as each collection pass does first, and
// waiting for background releases, as shutdown does, both wait for a mount
// evicted earlier whose release is still running: until it finishes, its
// view holds its snapshot.
func TestSharedMountDrainWaitsForEvictions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	store.WithSharedMounts(t, time.Millisecond, 0)
	entered, unblock := make(chan struct{}), make(chan struct{})
	var once sync.Once
	store.BeforeLocalUnmount = func() {
		once.Do(func() {
			close(entered)
			<-unblock
		})
	}
	ref, owner := buildSnapshot(t, store, nil, "a.txt", "a")
	require.Equal(t, "a", readShared(t, ref, "a.txt"))
	<-entered // the linger ran out and the eviction's unmount is running

	releaser := mountReleaser(t, store)
	idleReleased := make(chan error, 1)
	go func() { idleReleased <- releaser.ReleaseIdleSharedMounts() }()
	drained := make(chan struct{})
	go func() {
		releaser.WaitForBackgroundReleases()
		close(drained)
	}()
	select {
	case err := <-idleReleased:
		close(unblock)
		t.Fatalf("idle mounts released while an eviction was still running: %v", err)
	case <-drained:
		close(unblock)
		t.Fatal("background releases drained while an eviction was still running")
	case <-time.After(100 * time.Millisecond):
	}
	close(unblock)
	require.NoError(t, <-idleReleased)
	<-drained
	require.Empty(t, viewLeases(t, store))

	require.NoError(t, ref.Release(ctx))
	require.NoError(t, store.Manager.RemoveLease(ctx, owner))
}

// A failed shared mount is not kept: its reader gets the error, and the next
// reader mounts again.
func TestSharedMountFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	ref, owner := buildSnapshot(t, store, nil, "a.txt", "a")
	injected := errors.New("injected view lease failure")

	store.BeforeAdd = func(_ context.Context, _ leases.Lease, r leases.Resource) error {
		if strings.HasSuffix(r.ID, "-view") {
			return injected
		}
		return nil
	}
	root, _, release, err := ref.(bkcache.SharedMounter).MountShared(ctx)
	store.BeforeAdd = nil
	require.ErrorIs(t, err, injected)
	require.Empty(t, root)
	require.Nil(t, release)
	require.Empty(t, viewLeases(t, store))

	require.Equal(t, "a", readShared(t, ref, "a.txt"))
	require.Len(t, viewLeases(t, store), 1)

	require.NoError(t, ref.Release(ctx))
	require.NoError(t, store.Manager.RemoveLease(ctx, owner))
}
