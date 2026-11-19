package snapshots_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/leases"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// requireKept collects garbage and requires that exactly the snapshots of
// refs at the indexes keep reports are present.
func requireKept(t *testing.T, store *testutil.Store, refs []bkcache.ImmutableRef, keep func(int) bool, msg string) {
	t.Helper()
	store.GC(t)
	for i, ref := range refs {
		require.Equal(t, keep(i), snapshotPresent(t, store, ref.SnapshotID()), "%s: snapshot %d", msg, i)
	}
}

// An engine restart opens a new manager on the same metadata, attaches the
// dagql owner leases it keeps again, and deletes the rest with the previous
// process's ancestry lease: the kept owners' chains survive the collector,
// the dropped owners' snapshots don't, and removing the kept owners then
// frees everything.
func TestOwnerAncestryAcrossRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	refs, build := buildChain(t, store, 10)
	for i, ref := range refs {
		require.NoError(t, store.Manager.AttachLease(ctx, resultLeaseID(i), ref.SnapshotID()))
	}
	releaseChain(t, store, refs, build)
	requireKept(t, store, refs, func(int) bool { return true }, "before the restart")

	store.Reload(t)
	// The restarted engine keeps the owners of snapshots 2 and 5 only.
	keep := map[string]struct{}{}
	for _, i := range []int{2, 5} {
		keep[resultLeaseID(i)] = struct{}{}
		require.NoError(t, store.Manager.AttachLease(ctx, resultLeaseID(i), refs[i].SnapshotID()))
	}
	requireKept(t, store, refs, func(int) bool { return true }, "the previous ancestry lease still holds the chain")
	require.NoError(t, store.Manager.DeleteStaleDaggerOwnerLeases(ctx, keep))
	all, err := store.Leases.List(ctx)
	require.NoError(t, err)
	ancestry := 0
	for _, l := range all {
		if strings.HasPrefix(l.ID, ancestryLeasePrefix) {
			ancestry++
		}
	}
	require.Equal(t, 1, ancestry, "the previous process's ancestry lease is gone")
	requireKept(t, store, refs, func(i int) bool { return i <= 5 }, "after the stale owners are deleted")

	require.NoError(t, store.Manager.RemoveLease(ctx, resultLeaseID(5)))
	requireKept(t, store, refs, func(i int) bool { return i <= 2 }, "with the owner of 2 left")
	require.NoError(t, store.Manager.RemoveLease(ctx, resultLeaseID(2)))
	requireKept(t, store, refs, func(int) bool { return false }, "with no owner left")
	require.Zero(t, leaseResourceCount(t, store, "dagql/result/", ancestryLeasePrefix))
}

// Owners attach to and are removed from snapshots at every depth of a chain
// at once. Afterwards, the collector keeps exactly the snapshots at or below
// the deepest remaining owner, and the ancestry lease names each of those
// below it once.
func TestOwnerAncestryConcurrentAttachRemove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	const depth, owners = 30, 120
	refs, build := buildChain(t, store, depth)
	rng := rand.New(rand.NewPCG(1, 2))
	at := make([]int, owners)
	removed := make([]bool, owners)
	for i := range owners {
		at[i] = rng.IntN(depth)
		removed[i] = rng.IntN(3) > 0
	}
	// Keep the deepest survivor away from the top, so that the top part of
	// the chain must go.
	top := -1
	for i := range owners {
		if at[i] > depth-5 {
			removed[i] = true
		}
		if !removed[i] {
			top = max(top, at[i])
		}
	}
	require.GreaterOrEqual(t, top, 0)

	var wg sync.WaitGroup
	errs := make(chan error, 2*owners)
	for i := range owners {
		wg.Go(func() {
			id := fmt.Sprintf("dagql/result/%d/concurrent", i)
			if err := store.Manager.AttachLease(ctx, id, refs[at[i]].SnapshotID()); err != nil {
				errs <- err
				return
			}
			if removed[i] {
				errs <- store.Manager.RemoveLease(ctx, id)
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	releaseChain(t, store, refs, build)
	requireKept(t, store, refs, func(i int) bool { return i <= top }, "with the survivors")
	snapshots, _ := ancestryHoldings(t, store)
	want := []string{}
	for i := range top {
		want = append(want, refs[i].SnapshotID())
	}
	require.ElementsMatch(t, want, snapshots, "the ancestry lease names the survivors' ancestors once")

	for i := range owners {
		if !removed[i] {
			require.NoError(t, store.Manager.RemoveLease(ctx, fmt.Sprintf("dagql/result/%d/concurrent", i)))
		}
	}
	requireKept(t, store, refs, func(int) bool { return false }, "with no owner left")
	require.Zero(t, leaseResourceCount(t, store, "dagql/result/", ancestryLeasePrefix))
}

// A lease other than a dagql owner lease, deleted from the lease manager
// directly as operation leases and pins are, never stands in for the
// ancestry of a dagql owner's snapshot: the dagql owner keeps its whole chain
// once that lease is gone.
func TestOwnerAncestryIgnoresOtherLeases(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	refs, build := buildChain(t, store, 6)
	const operation = "operation-holds-the-middle"
	require.NoError(t, store.Manager.AttachLease(ctx, operation, refs[3].SnapshotID()))
	require.NoError(t, store.Manager.AttachLease(ctx, resultLeaseID(5), refs[5].SnapshotID()))
	releaseChain(t, store, refs, build)
	require.NoError(t, store.Leases.Delete(ctx, leases.Lease{ID: operation}))
	requireKept(t, store, refs, func(int) bool { return true }, "with the operation lease deleted directly")
	require.NoError(t, store.Manager.RemoveLease(ctx, resultLeaseID(5)))
	requireKept(t, store, refs, func(int) bool { return false }, "with the owner removed")
}

// A blob recorded for an ancestor after its owners attached, as an export of
// the chain records each layer's, is held by the ancestry lease: the
// collector keeps it while the ancestor is kept, and takes it with the
// ancestor.
func TestOwnerAncestryHoldsBlobRecordedLater(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	parent, parentOwner := buildSnapshot(t, store, nil, "p.txt", "parent")
	child, childOwner := buildSnapshot(t, store, parent, "c.txt", "child")
	const owner = "dagql/result/blob-later"
	require.NoError(t, store.Manager.AttachLease(ctx, owner, child.SnapshotID()))
	_, contents := ancestryHoldings(t, store)
	require.Empty(t, contents, "no blob is recorded yet")

	chain, err := child.ExportChain(ctx, uncompressed)
	require.NoError(t, err)
	require.Len(t, chain.Layers, 2)
	parentBlob, childBlob := chain.Layers[0].Descriptor.Digest, chain.Layers[1].Descriptor.Digest
	require.NoError(t, chain.Release(ctx))
	_, contents = ancestryHoldings(t, store)
	require.Equal(t, []string{parentBlob.String()}, contents, "the export attached the ancestor's blob to the ancestry lease")
	_, _, contents = leaseResources(t, store, owner)
	require.Equal(t, []string{childBlob.String()}, contents, "and the owner's snapshot's blob to the owner")

	require.NoError(t, child.Release(ctx))
	require.NoError(t, parent.Release(ctx))
	require.NoError(t, store.Manager.RemoveLease(ctx, childOwner))
	require.NoError(t, store.Manager.RemoveLease(ctx, parentOwner))
	store.GC(t)
	require.True(t, snapshotPresent(t, store, parent.SnapshotID()))
	require.True(t, blobPresent(t, store, parentBlob), "the ancestry lease keeps the ancestor's blob")
	require.True(t, blobPresent(t, store, childBlob))

	require.NoError(t, store.Manager.RemoveLease(ctx, owner))
	store.GC(t)
	require.False(t, snapshotPresent(t, store, parent.SnapshotID()))
	require.False(t, snapshotPresent(t, store, child.SnapshotID()))
	require.False(t, blobPresent(t, store, parentBlob))
	require.False(t, blobPresent(t, store, childBlob))
}

// writeBlob writes data to the content store as a layer blob, which nothing
// keeps once it returns.
func writeBlob(t *testing.T, store *testutil.Store, data string) ocispecs.Descriptor {
	t.Helper()
	ctx := context.Background()
	write, err := store.Leases.Create(ctx, leases.WithRandomID())
	require.NoError(t, err)
	desc := ocispecs.Descriptor{MediaType: ocispecs.MediaTypeImageLayer, Digest: digest.FromString(data), Size: int64(len(data))}
	require.NoError(t, content.WriteBlob(leases.WithLease(ctx, write.ID), store.Content, desc.Digest.String(), strings.NewReader(data), desc))
	require.NoError(t, store.Leases.Delete(ctx, write))
	return desc
}

// holdUntilRemoved runs remove while hold is held, giving it a moment to
// finish, then lets hold go and waits for remove: the removal either ran in
// the middle of what hold interrupts, or waits for it.
func holdUntilRemoved(t *testing.T, entered <-chan struct{}, resume chan<- struct{}, held <-chan error, remove func() error) {
	t.Helper()
	<-entered
	removed := make(chan error, 1)
	go func() { removed <- remove() }()
	select {
	case err := <-removed:
		removed <- err
	case <-time.After(200 * time.Millisecond):
	}
	close(resume)
	require.NoError(t, <-held)
	require.NoError(t, <-removed)
}

// A blob recorded for an ancestor while the last owner above it is removed
// is either counted before the removal, which then deletes it from the
// ancestry lease with the ancestor, or not attached at all: it is never left
// in the ancestry lease with nothing counting it.
func TestOwnerAncestryBlobRecordedDuringRemoval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	refs, build := buildChain(t, store, 2)
	blob := writeBlob(t, store, "ancestor blob")
	owner := resultLeaseID(1)
	require.NoError(t, store.Manager.AttachLease(ctx, owner, refs[1].SnapshotID()))

	entered, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	store.BeforeAdd = func(_ context.Context, l leases.Lease, r leases.Resource) error {
		if strings.HasPrefix(l.ID, ancestryLeasePrefix) && r.ID == blob.Digest.String() {
			once.Do(func() {
				close(entered)
				<-resume
			})
		}
		return nil
	}
	recorded := make(chan error, 1)
	go func() { recorded <- bkcache.RecordSnapshotContent(store.Manager, refs[0].SnapshotID(), blob) }()
	holdUntilRemoved(t, entered, resume, recorded, func() error { return store.Manager.RemoveLease(ctx, owner) })
	store.BeforeAdd = nil

	releaseChain(t, store, refs, build)
	require.Zero(t, leaseResourceCount(t, store, ancestryLeasePrefix), "the ancestry lease names nothing")
	requireKept(t, store, refs, func(int) bool { return false }, "with the owner removed")
	require.False(t, blobPresent(t, store, blob.Digest), "the ancestor's blob goes with it")
}

// Two ancestors share a blob. Recording it for the second while the first is
// released never leaves the second, still kept, without its blob.
func TestOwnerAncestryBlobSharedWithReleasedAncestor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	first, firstBuild := buildChain(t, store, 2)
	second, secondBuild := buildChain(t, store, 2)
	blob := writeBlob(t, store, "shared blob")
	firstOwner, secondOwner := resultLeaseID(1), resultLeaseID(2)
	require.NoError(t, store.Manager.AttachLease(ctx, firstOwner, first[1].SnapshotID()))
	require.NoError(t, store.Manager.AttachLease(ctx, secondOwner, second[1].SnapshotID()))
	require.NoError(t, bkcache.RecordSnapshotContent(store.Manager, first[0].SnapshotID(), blob))

	entered, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	store.AfterAdd = func(_ context.Context, l leases.Lease, r leases.Resource) {
		if strings.HasPrefix(l.ID, ancestryLeasePrefix) && r.ID == blob.Digest.String() {
			once.Do(func() {
				close(entered)
				<-resume
			})
		}
	}
	recorded := make(chan error, 1)
	go func() { recorded <- bkcache.RecordSnapshotContent(store.Manager, second[0].SnapshotID(), blob) }()
	holdUntilRemoved(t, entered, resume, recorded, func() error { return store.Manager.RemoveLease(ctx, firstOwner) })
	store.AfterAdd = nil

	releaseChain(t, store, first, firstBuild)
	releaseChain(t, store, second, secondBuild)
	requireKept(t, store, second, func(int) bool { return true }, "the second chain's owner keeps it")
	require.True(t, blobPresent(t, store, blob.Digest), "and its ancestor's blob")
	require.NoError(t, store.Manager.RemoveLease(ctx, secondOwner))
	requireKept(t, store, second, func(int) bool { return false }, "with no owner left")
	require.False(t, blobPresent(t, store, blob.Digest))
}

// A blob recorded for an ancestor after an attach read the ancestor's blobs,
// and before the attach counted the ancestor, is attached to the ancestry
// lease by the attach.
func TestOwnerAncestryAttachesBlobRecordedDuringAttach(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	refs, build := buildChain(t, store, 2)
	blob := writeBlob(t, store, "ancestor blob")
	owner := resultLeaseID(1)
	var once sync.Once
	var recordErr error
	store.BeforeAdd = func(_ context.Context, l leases.Lease, r leases.Resource) error {
		if strings.HasPrefix(l.ID, ancestryLeasePrefix) && r.ID == refs[0].SnapshotID() {
			// Within the attach's metadata transaction. No owner lease names
			// the ancestor, so recording its blob writes nothing.
			once.Do(func() { recordErr = bkcache.RecordSnapshotContent(store.Manager, refs[0].SnapshotID(), blob) })
		}
		return nil
	}
	require.NoError(t, store.Manager.AttachLease(ctx, owner, refs[1].SnapshotID()))
	store.BeforeAdd = nil
	require.NoError(t, recordErr)
	_, contents := ancestryHoldings(t, store)
	require.Equal(t, []string{blob.Digest.String()}, contents)

	releaseChain(t, store, refs, build)
	requireKept(t, store, refs, func(int) bool { return true }, "with the owner")
	require.True(t, blobPresent(t, store, blob.Digest), "the ancestry lease keeps the ancestor's blob")
	require.NoError(t, store.Manager.RemoveLease(ctx, owner))
	requireKept(t, store, refs, func(int) bool { return false }, "with the owner removed")
	require.False(t, blobPresent(t, store, blob.Digest))
}

// A failed write to the ancestry lease fails AttachLease and records nothing;
// the retry attaches the chain, which the owner then keeps alone.
func TestOwnerAncestryWriteFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	refs, build := buildChain(t, store, 4)
	injected := errors.New("injected ancestry write failure")
	store.BeforeAdd = func(_ context.Context, l leases.Lease, _ leases.Resource) error {
		if strings.HasPrefix(l.ID, ancestryLeasePrefix) {
			return injected
		}
		return nil
	}
	owner := resultLeaseID(3)
	require.ErrorIs(t, store.Manager.AttachLease(ctx, owner, refs[3].SnapshotID()), injected)
	bySnapshot, _ := bkcache.OwnerRecordCounts(store.Manager)
	require.Zero(t, bySnapshot, "the failed attach is not recorded")
	store.BeforeAdd = nil
	require.NoError(t, store.Manager.AttachLease(ctx, owner, refs[3].SnapshotID()))
	releaseChain(t, store, refs, build)
	requireKept(t, store, refs, func(int) bool { return true }, "after the retry")
	require.NoError(t, store.Manager.RemoveLease(ctx, owner))
	requireKept(t, store, refs, func(int) bool { return false }, "with the owner removed")
}
