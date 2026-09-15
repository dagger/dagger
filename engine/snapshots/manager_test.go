package snapshots

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/leases"
	ctdsnapshots "github.com/containerd/containerd/v2/core/snapshots"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/engine/snapshots/fsdiff"
	"github.com/moby/locker"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

type applySnapshotDiffTestLeaseManager struct {
	mu        sync.Mutex
	leases    map[string]leases.Lease
	resources map[string][]leases.Resource
}

func newApplySnapshotDiffTestLeaseManager() *applySnapshotDiffTestLeaseManager {
	return &applySnapshotDiffTestLeaseManager{
		leases:    map[string]leases.Lease{},
		resources: map[string][]leases.Resource{},
	}
}

func (lm *applySnapshotDiffTestLeaseManager) Create(_ context.Context, opts ...leases.Opt) (leases.Lease, error) {
	l := leases.Lease{}
	for _, opt := range opts {
		if err := opt(&l); err != nil {
			return leases.Lease{}, err
		}
	}
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.leases[l.ID] = l
	return l, nil
}

func (lm *applySnapshotDiffTestLeaseManager) Delete(_ context.Context, lease leases.Lease, _ ...leases.DeleteOpt) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	delete(lm.leases, lease.ID)
	delete(lm.resources, lease.ID)
	return nil
}

func (lm *applySnapshotDiffTestLeaseManager) List(_ context.Context, _ ...string) ([]leases.Lease, error) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	out := make([]leases.Lease, 0, len(lm.leases))
	for _, lease := range lm.leases {
		out = append(out, lease)
	}
	return out, nil
}

func (lm *applySnapshotDiffTestLeaseManager) AddResource(_ context.Context, lease leases.Lease, resource leases.Resource) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.resources[lease.ID] = append(lm.resources[lease.ID], resource)
	return nil
}

func (lm *applySnapshotDiffTestLeaseManager) DeleteResource(_ context.Context, lease leases.Lease, resource leases.Resource) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	resources := lm.resources[lease.ID]
	for i, candidate := range resources {
		if candidate == resource {
			lm.resources[lease.ID] = append(resources[:i], resources[i+1:]...)
			return nil
		}
	}
	return nil
}

func (lm *applySnapshotDiffTestLeaseManager) ListResources(_ context.Context, lease leases.Lease) ([]leases.Resource, error) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return append([]leases.Resource(nil), lm.resources[lease.ID]...), nil
}

type attachRemoveRaceLeaseManager struct {
	mu        sync.Mutex
	leases    map[string]leases.Lease
	resources map[string][]leases.Resource

	targetLease string
	blockDelete bool

	deleteStarted      chan struct{}
	attachCreatedLease chan struct{}
	allowDelete        chan struct{}
	deleteDone         chan struct{}

	deleteStartedOnce      sync.Once
	attachCreatedLeaseOnce sync.Once
	deleteDoneOnce         sync.Once
}

func newAttachRemoveRaceLeaseManager(targetLease string) *attachRemoveRaceLeaseManager {
	return &attachRemoveRaceLeaseManager{
		leases:             map[string]leases.Lease{},
		resources:          map[string][]leases.Resource{},
		targetLease:        targetLease,
		deleteStarted:      make(chan struct{}),
		attachCreatedLease: make(chan struct{}),
		allowDelete:        make(chan struct{}),
		deleteDone:         make(chan struct{}),
	}
}

func (lm *attachRemoveRaceLeaseManager) Create(_ context.Context, opts ...leases.Opt) (leases.Lease, error) {
	l := leases.Lease{}
	for _, opt := range opts {
		if err := opt(&l); err != nil {
			return leases.Lease{}, err
		}
	}

	lm.mu.Lock()
	lm.leases[l.ID] = l
	blockDelete := lm.blockDelete
	lm.mu.Unlock()

	if l.ID == lm.targetLease && blockDelete {
		lm.attachCreatedLeaseOnce.Do(func() {
			close(lm.attachCreatedLease)
		})
	}

	return l, nil
}

func (lm *attachRemoveRaceLeaseManager) Delete(ctx context.Context, lease leases.Lease, _ ...leases.DeleteOpt) error {
	lm.mu.Lock()
	blockDelete := lm.blockDelete && lease.ID == lm.targetLease
	lm.mu.Unlock()

	if blockDelete {
		lm.deleteStartedOnce.Do(func() {
			close(lm.deleteStarted)
		})
		select {
		case <-lm.allowDelete:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}

	lm.mu.Lock()
	delete(lm.leases, lease.ID)
	delete(lm.resources, lease.ID)
	lm.mu.Unlock()

	if blockDelete {
		lm.deleteDoneOnce.Do(func() {
			close(lm.deleteDone)
		})
	}

	return nil
}

func (lm *attachRemoveRaceLeaseManager) List(context.Context, ...string) ([]leases.Lease, error) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	out := make([]leases.Lease, 0, len(lm.leases))
	for _, lease := range lm.leases {
		out = append(out, lease)
	}
	return out, nil
}

func (lm *attachRemoveRaceLeaseManager) AddResource(ctx context.Context, lease leases.Lease, resource leases.Resource) error {
	if lease.ID == lm.targetLease {
		select {
		case <-lm.deleteDone:
		default:
			select {
			case <-lm.deleteStarted:
				select {
				case <-lm.deleteDone:
				case <-ctx.Done():
					return context.Cause(ctx)
				}
			default:
			}
		}
	}

	lm.mu.Lock()
	defer lm.mu.Unlock()
	if _, ok := lm.leases[lease.ID]; !ok {
		return fmt.Errorf("lease %q: not found", lease.ID)
	}
	lm.resources[lease.ID] = append(lm.resources[lease.ID], resource)
	return nil
}

func (lm *attachRemoveRaceLeaseManager) DeleteResource(context.Context, leases.Lease, leases.Resource) error {
	return nil
}

func (lm *attachRemoveRaceLeaseManager) ListResources(_ context.Context, lease leases.Lease) ([]leases.Resource, error) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return append([]leases.Resource(nil), lm.resources[lease.ID]...), nil
}

type applySnapshotDiffTestSnapshotter struct {
	mu             sync.Mutex
	snapshots      map[string]ctdsnapshots.Info
	prepareLeaseID []string
	commitLeaseID  []string
	mergeLeaseID   []string
	mergeCalls     [][]Diff
}

func newApplySnapshotDiffTestSnapshotter() *applySnapshotDiffTestSnapshotter {
	return &applySnapshotDiffTestSnapshotter{
		snapshots: map[string]ctdsnapshots.Info{},
	}
}

func (sn *applySnapshotDiffTestSnapshotter) Name() string { return "test" }

func (sn *applySnapshotDiffTestSnapshotter) Mounts(context.Context, string) (MountableRef, error) {
	panic("unexpected Mounts call")
}

func (sn *applySnapshotDiffTestSnapshotter) Prepare(ctx context.Context, key, parent string, _ ...ctdsnapshots.Opt) error {
	sn.mu.Lock()
	defer sn.mu.Unlock()
	leaseID, _ := leases.FromContext(ctx)
	sn.prepareLeaseID = append(sn.prepareLeaseID, leaseID)
	sn.snapshots[key] = ctdsnapshots.Info{Name: key, Parent: parent}
	return nil
}

func (sn *applySnapshotDiffTestSnapshotter) View(context.Context, string, string, ...ctdsnapshots.Opt) (MountableRef, error) {
	panic("unexpected View call")
}

func (sn *applySnapshotDiffTestSnapshotter) Stat(_ context.Context, key string) (ctdsnapshots.Info, error) {
	sn.mu.Lock()
	defer sn.mu.Unlock()
	info, ok := sn.snapshots[key]
	if !ok {
		return ctdsnapshots.Info{}, cerrdefs.ErrNotFound
	}
	return info, nil
}

func (sn *applySnapshotDiffTestSnapshotter) Update(_ context.Context, info ctdsnapshots.Info, _ ...string) (ctdsnapshots.Info, error) {
	sn.mu.Lock()
	defer sn.mu.Unlock()
	sn.snapshots[info.Name] = info
	return info, nil
}

func (sn *applySnapshotDiffTestSnapshotter) Usage(context.Context, string) (ctdsnapshots.Usage, error) {
	return ctdsnapshots.Usage{}, nil
}

func (sn *applySnapshotDiffTestSnapshotter) Commit(ctx context.Context, name, key string, _ ...ctdsnapshots.Opt) error {
	sn.mu.Lock()
	defer sn.mu.Unlock()
	leaseID, _ := leases.FromContext(ctx)
	sn.commitLeaseID = append(sn.commitLeaseID, leaseID)
	info, ok := sn.snapshots[key]
	if !ok {
		return cerrdefs.ErrNotFound
	}
	delete(sn.snapshots, key)
	info.Name = name
	sn.snapshots[name] = info
	return nil
}

func (sn *applySnapshotDiffTestSnapshotter) Remove(_ context.Context, key string) error {
	sn.mu.Lock()
	defer sn.mu.Unlock()
	delete(sn.snapshots, key)
	return nil
}

func (sn *applySnapshotDiffTestSnapshotter) Walk(context.Context, ctdsnapshots.WalkFunc, ...string) error {
	return nil
}

func (sn *applySnapshotDiffTestSnapshotter) Close() error { return nil }

func (sn *applySnapshotDiffTestSnapshotter) Merge(ctx context.Context, key string, diffs []Diff, _ ...ctdsnapshots.Opt) error {
	sn.mu.Lock()
	defer sn.mu.Unlock()
	leaseID, _ := leases.FromContext(ctx)
	sn.mergeLeaseID = append(sn.mergeLeaseID, leaseID)
	sn.mergeCalls = append(sn.mergeCalls, append([]Diff(nil), diffs...))
	sn.snapshots[key] = ctdsnapshots.Info{Name: key}
	return nil
}

func newApplySnapshotDiffTestManager(t testing.TB) *snapshotManager {
	t.Helper()

	sn := newApplySnapshotDiffTestSnapshotter()
	lm := newApplySnapshotDiffTestLeaseManager()
	return &snapshotManager{
		records:                map[string]*cacheRecord{},
		Snapshotter:            sn,
		LeaseManager:           lm,
		metadataStore:          newMetadataStore(),
		snapshotContentDigests: map[string]map[digest.Digest]struct{}{},
		importedLayerByBlob:    map[ImportedLayerBlobKey]string{},
		importedLayerByDiff:    map[ImportedLayerDiffKey]string{},
		snapshotOwnerLeases:    map[string]map[string]struct{}{},
		ownerLeaseSnapshots:    map[string]map[string]struct{}{},
		importLayerLocker:      locker.New(),
		ownerLeaseLocker:       locker.New(),
	}
}

func addApplySnapshotDiffTestImmutable(t *testing.T, cm *snapshotManager, snapshotID string) *immutableRef {
	t.Helper()

	cm.mu.Lock()
	defer cm.mu.Unlock()

	md := cm.ensureMetadata(snapshotID)
	require.NoError(t, md.queueSnapshotID(snapshotID))
	require.NoError(t, md.queueCommitted(true))
	require.NoError(t, md.commitMetadata())
	cm.records[snapshotID] = &cacheRecord{
		cm: cm,
		md: md,
	}
	cm.Snapshotter.(*applySnapshotDiffTestSnapshotter).snapshots[snapshotID] = ctdsnapshots.Info{Name: snapshotID}
	return &immutableRef{
		cm:          cm,
		refMetadata: refMetadata{snapshotID: snapshotID, md: md},
	}
}

func TestSnapshotManagerEnsuresLazyLeaseForCreateBoundaries(t *testing.T) {
	t.Run("new", func(t *testing.T) {
		cm := newApplySnapshotDiffTestManager(t)
		lm := cm.LeaseManager.(*applySnapshotDiffTestLeaseManager)
		ctx, release, err := WithLazyLease(context.Background(), lm)
		require.NoError(t, err)
		defer release(context.Background())

		ref, err := cm.New(ctx, nil)
		require.NoError(t, err)
		require.NotNil(t, ref)
		require.NoError(t, ref.Release(context.Background()))

		sn := cm.Snapshotter.(*applySnapshotDiffTestSnapshotter)
		require.Len(t, sn.prepareLeaseID, 1)
		require.NotEmpty(t, sn.prepareLeaseID[0])
	})

	t.Run("merge", func(t *testing.T) {
		cm := newApplySnapshotDiffTestManager(t)
		lm := cm.LeaseManager.(*applySnapshotDiffTestLeaseManager)
		ctx, release, err := WithLazyLease(context.Background(), lm)
		require.NoError(t, err)
		defer release(context.Background())
		lower := addApplySnapshotDiffTestImmutable(t, cm, "lower-snapshot")
		upper := addApplySnapshotDiffTestImmutable(t, cm, "upper-snapshot")

		ref, err := cm.Merge(ctx, []ImmutableRef{lower, upper})
		require.NoError(t, err)
		require.NotNil(t, ref)
		require.NoError(t, ref.Release(context.Background()))

		sn := cm.Snapshotter.(*applySnapshotDiffTestSnapshotter)
		require.Len(t, sn.mergeLeaseID, 1)
		require.NotEmpty(t, sn.mergeLeaseID[0])
	})

	t.Run("commit", func(t *testing.T) {
		cm := newApplySnapshotDiffTestManager(t)
		lm := cm.LeaseManager.(*applySnapshotDiffTestLeaseManager)
		mut, err := cm.New(context.Background(), nil)
		require.NoError(t, err)
		ctx, release, err := WithLazyLease(context.Background(), lm)
		require.NoError(t, err)
		defer release(context.Background())

		ref, err := mut.Commit(ctx)
		require.NoError(t, err)
		require.NotNil(t, ref)
		require.NoError(t, ref.Release(context.Background()))

		sn := cm.Snapshotter.(*applySnapshotDiffTestSnapshotter)
		require.Len(t, sn.commitLeaseID, 1)
		require.NotEmpty(t, sn.commitLeaseID[0])
	})
}

func TestSnapshotManagerRemoveLeaseDoesNotDeleteDuringAttachLease(t *testing.T) {
	const (
		leaseID     = "dagql/result/123/fs"
		oldSnapshot = "snapshot-old"
		newSnapshot = "snapshot-new"
	)

	cm := newApplySnapshotDiffTestManager(t)
	lm := newAttachRemoveRaceLeaseManager(leaseID)
	cm.LeaseManager = lm
	sn := cm.Snapshotter.(*applySnapshotDiffTestSnapshotter)
	sn.snapshots[oldSnapshot] = ctdsnapshots.Info{Name: oldSnapshot}
	sn.snapshots[newSnapshot] = ctdsnapshots.Info{Name: newSnapshot}

	require.NoError(t, cm.AttachLease(context.Background(), leaseID, oldSnapshot))

	lm.mu.Lock()
	lm.blockDelete = true
	lm.mu.Unlock()

	removeErrCh := make(chan error, 1)
	go func() {
		removeErrCh <- cm.RemoveLease(context.Background(), leaseID)
	}()

	select {
	case <-lm.deleteStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for RemoveLease to start deleting the owner lease")
	}

	attachErrCh := make(chan error, 1)
	attachAttempted := make(chan struct{})
	go func() {
		close(attachAttempted)
		attachErrCh <- cm.AttachLease(context.Background(), leaseID, newSnapshot)
	}()

	select {
	case <-attachAttempted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for AttachLease goroutine to start")
	}

	select {
	case <-lm.attachCreatedLease:
	case <-time.After(time.Second):
	}

	close(lm.allowDelete)

	require.NoError(t, <-removeErrCh)
	require.NoError(t, <-attachErrCh)
}

func TestSnapshotManagerRemoveLeaseReverseIndexConsistency(t *testing.T) {
	cm := newApplySnapshotDiffTestManager(t)
	sn := cm.Snapshotter.(*applySnapshotDiffTestSnapshotter)
	sn.snapshots["snapshot-old"] = ctdsnapshots.Info{Name: "snapshot-old"}
	sn.snapshots["snapshot-new"] = ctdsnapshots.Info{Name: "snapshot-new", Parent: "snapshot-old"}

	require.NoError(t, cm.AttachLease(context.Background(), "lease-1", "snapshot-new"))

	// Attaching walks the whole chain, so both snapshots show up in the
	// forward map and the reverse index.
	require.Equal(t, map[string]map[string]struct{}{
		"snapshot-old": {"lease-1": {}},
		"snapshot-new": {"lease-1": {}},
	}, cm.snapshotOwnerLeases)
	require.Equal(t, map[string]map[string]struct{}{
		"lease-1": {"snapshot-old": {}, "snapshot-new": {}},
	}, cm.ownerLeaseSnapshots)

	require.NoError(t, cm.RemoveLease(context.Background(), "lease-1"))
	require.Empty(t, cm.snapshotOwnerLeases)
	require.Empty(t, cm.ownerLeaseSnapshots)

	// Re-attach after a remove must rebuild both maps without residue.
	require.NoError(t, cm.AttachLease(context.Background(), "lease-1", "snapshot-new"))
	require.NoError(t, cm.AttachLease(context.Background(), "lease-2", "snapshot-old"))
	require.NoError(t, cm.RemoveLease(context.Background(), "lease-1"))

	require.Equal(t, map[string]map[string]struct{}{
		"snapshot-old": {"lease-2": {}},
	}, cm.snapshotOwnerLeases)
	require.Equal(t, map[string]map[string]struct{}{
		"lease-2": {"snapshot-old": {}},
	}, cm.ownerLeaseSnapshots)

	// Removing a lease that has no snapshot mappings is a no-op.
	require.NoError(t, cm.RemoveLease(context.Background(), "lease-unknown"))
	require.Len(t, cm.snapshotOwnerLeases, 1)
	require.Len(t, cm.ownerLeaseSnapshots, 1)
}

func TestSnapshotManagerRemoveLeaseLeavesUnrelatedLeasesIntact(t *testing.T) {
	cm := newApplySnapshotDiffTestManager(t)
	sn := cm.Snapshotter.(*applySnapshotDiffTestSnapshotter)

	const numSnapshots = 2000
	for i := 0; i < numSnapshots; i++ {
		snapshotID := fmt.Sprintf("snapshot-%d", i)
		sn.snapshots[snapshotID] = ctdsnapshots.Info{Name: snapshotID}
		require.NoError(t, cm.AttachLease(context.Background(), fmt.Sprintf("lease-%d", i), snapshotID))
	}
	require.Len(t, cm.snapshotOwnerLeases, numSnapshots)
	require.Len(t, cm.ownerLeaseSnapshots, numSnapshots)

	require.NoError(t, cm.RemoveLease(context.Background(), "lease-1234"))

	require.NotContains(t, cm.snapshotOwnerLeases, "snapshot-1234")
	require.NotContains(t, cm.ownerLeaseSnapshots, "lease-1234")
	require.Len(t, cm.snapshotOwnerLeases, numSnapshots-1)
	require.Len(t, cm.ownerLeaseSnapshots, numSnapshots-1)
	for i := 0; i < numSnapshots; i++ {
		if i == 1234 {
			continue
		}
		snapshotID := fmt.Sprintf("snapshot-%d", i)
		leaseID := fmt.Sprintf("lease-%d", i)
		require.Contains(t, cm.snapshotOwnerLeases[snapshotID], leaseID)
		require.Contains(t, cm.ownerLeaseSnapshots[leaseID], snapshotID)
	}
}

func BenchmarkSnapshotManagerRemoveLease(b *testing.B) {
	cm := newApplySnapshotDiffTestManager(b)
	sn := cm.Snapshotter.(*applySnapshotDiffTestSnapshotter)

	const numSnapshots = 5000
	for i := 0; i < numSnapshots; i++ {
		snapshotID := fmt.Sprintf("snapshot-%d", i)
		sn.snapshots[snapshotID] = ctdsnapshots.Info{Name: snapshotID}
		if err := cm.AttachLease(context.Background(), fmt.Sprintf("lease-%d", i), snapshotID); err != nil {
			b.Fatal(err)
		}
	}

	const (
		leaseID    = "bench-lease"
		snapshotID = "bench-snapshot"
	)
	sn.snapshots[snapshotID] = ctdsnapshots.Info{Name: snapshotID}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := cm.AttachLease(context.Background(), leaseID, snapshotID); err != nil {
			b.Fatal(err)
		}
		if err := cm.RemoveLease(context.Background(), leaseID); err != nil {
			b.Fatal(err)
		}
	}
}

func TestApplySnapshotDiffNilContract(t *testing.T) {
	t.Run("nil nil returns nil", func(t *testing.T) {
		cm := newApplySnapshotDiffTestManager(t)
		ref, err := cm.ApplySnapshotDiff(context.Background(), nil, nil)
		require.NoError(t, err)
		require.Nil(t, ref)
		require.Empty(t, cm.Snapshotter.(*applySnapshotDiffTestSnapshotter).mergeCalls)
	})

	t.Run("nil lower reopens upper directly", func(t *testing.T) {
		cm := newApplySnapshotDiffTestManager(t)
		upper := addApplySnapshotDiffTestImmutable(t, cm, "upper-snapshot")

		ref, err := cm.ApplySnapshotDiff(context.Background(), nil, upper)
		require.NoError(t, err)
		require.NotNil(t, ref)
		require.Equal(t, upper.SnapshotID(), ref.SnapshotID())
		require.NotSame(t, upper, ref)
		require.Empty(t, cm.Snapshotter.(*applySnapshotDiffTestSnapshotter).mergeCalls)
	})

	t.Run("equivalent snapshots produce an explicit empty diff", func(t *testing.T) {
		cm := newApplySnapshotDiffTestManager(t)
		lower := addApplySnapshotDiffTestImmutable(t, cm, "same-snapshot")

		ref, err := cm.ApplySnapshotDiff(context.Background(), lower, lower)
		require.NoError(t, err)
		require.NotNil(t, ref)
		require.NotEqual(t, lower.SnapshotID(), ref.SnapshotID())
		require.Len(t, cm.Snapshotter.(*applySnapshotDiffTestSnapshotter).mergeCalls, 1)
		require.Empty(t, cm.Snapshotter.(*applySnapshotDiffTestSnapshotter).mergeCalls[0])
	})

	t.Run("uses content-aware comparison", func(t *testing.T) {
		cm := newApplySnapshotDiffTestManager(t)
		lower := addApplySnapshotDiffTestImmutable(t, cm, "lower-snapshot")
		upper := addApplySnapshotDiffTestImmutable(t, cm, "upper-snapshot")

		ref, err := cm.ApplySnapshotDiff(context.Background(), lower, upper)
		require.NoError(t, err)
		require.NotNil(t, ref)
		require.Len(t, cm.Snapshotter.(*applySnapshotDiffTestSnapshotter).mergeCalls, 1)
		require.Equal(t, []Diff{{
			Lower:      "lower-snapshot",
			Upper:      "upper-snapshot",
			Comparison: fsdiff.CompareContentOnMetadataMatch,
		}}, cm.Snapshotter.(*applySnapshotDiffTestSnapshotter).mergeCalls[0])
	})
}

func TestMergeContract(t *testing.T) {
	t.Run("nil parents returns nil", func(t *testing.T) {
		cm := newApplySnapshotDiffTestManager(t)

		ref, err := cm.Merge(context.Background(), nil)
		require.NoError(t, err)
		require.Nil(t, ref)
		require.Empty(t, cm.Snapshotter.(*applySnapshotDiffTestSnapshotter).mergeCalls)
	})

	t.Run("single parent reopens directly", func(t *testing.T) {
		cm := newApplySnapshotDiffTestManager(t)
		parent := addApplySnapshotDiffTestImmutable(t, cm, "parent-snapshot")

		ref, err := cm.Merge(context.Background(), []ImmutableRef{parent})
		require.NoError(t, err)
		require.NotNil(t, ref)
		require.Equal(t, parent.SnapshotID(), ref.SnapshotID())
		require.NotSame(t, parent, ref)
		require.Empty(t, cm.Snapshotter.(*applySnapshotDiffTestSnapshotter).mergeCalls)
	})

	t.Run("nil parents are ignored", func(t *testing.T) {
		cm := newApplySnapshotDiffTestManager(t)
		parent := addApplySnapshotDiffTestImmutable(t, cm, "parent-snapshot")

		ref, err := cm.Merge(context.Background(), []ImmutableRef{nil, parent, nil})
		require.NoError(t, err)
		require.NotNil(t, ref)
		require.Equal(t, parent.SnapshotID(), ref.SnapshotID())
		require.Empty(t, cm.Snapshotter.(*applySnapshotDiffTestSnapshotter).mergeCalls)
	})

	t.Run("multiple parents materialize ordered full snapshot diffs", func(t *testing.T) {
		cm := newApplySnapshotDiffTestManager(t)
		lower := addApplySnapshotDiffTestImmutable(t, cm, "lower-snapshot")
		upper := addApplySnapshotDiffTestImmutable(t, cm, "upper-snapshot")

		ref, err := cm.Merge(context.Background(), []ImmutableRef{lower, upper})
		require.NoError(t, err)
		require.NotNil(t, ref)
		require.NotEqual(t, lower.SnapshotID(), ref.SnapshotID())
		require.NotEqual(t, upper.SnapshotID(), ref.SnapshotID())
		require.Len(t, cm.Snapshotter.(*applySnapshotDiffTestSnapshotter).mergeCalls, 1)
		require.Equal(t, []Diff{
			{Upper: "lower-snapshot"},
			{Upper: "upper-snapshot"},
		}, cm.Snapshotter.(*applySnapshotDiffTestSnapshotter).mergeCalls[0])
	})
}
