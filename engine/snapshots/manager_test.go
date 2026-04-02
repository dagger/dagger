package snapshots

import (
	"context"
	"sync"
	"testing"

	"github.com/containerd/containerd/v2/core/leases"
	ctdsnapshots "github.com/containerd/containerd/v2/core/snapshots"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/internal/buildkit/snapshot"
	"github.com/docker/docker/pkg/idtools"
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

type applySnapshotDiffTestSnapshotter struct {
	mu         sync.Mutex
	snapshots  map[string]ctdsnapshots.Info
	mergeCalls [][]snapshot.Diff
}

func newApplySnapshotDiffTestSnapshotter() *applySnapshotDiffTestSnapshotter {
	return &applySnapshotDiffTestSnapshotter{
		snapshots: map[string]ctdsnapshots.Info{},
	}
}

func (sn *applySnapshotDiffTestSnapshotter) Name() string { return "test" }

func (sn *applySnapshotDiffTestSnapshotter) Mounts(context.Context, string) (snapshot.Mountable, error) {
	panic("unexpected Mounts call")
}

func (sn *applySnapshotDiffTestSnapshotter) Prepare(_ context.Context, key, parent string, _ ...ctdsnapshots.Opt) error {
	sn.mu.Lock()
	defer sn.mu.Unlock()
	sn.snapshots[key] = ctdsnapshots.Info{Name: key, Parent: parent}
	return nil
}

func (sn *applySnapshotDiffTestSnapshotter) View(context.Context, string, string, ...ctdsnapshots.Opt) (snapshot.Mountable, error) {
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

func (sn *applySnapshotDiffTestSnapshotter) Commit(_ context.Context, name, key string, _ ...ctdsnapshots.Opt) error {
	sn.mu.Lock()
	defer sn.mu.Unlock()
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

func (sn *applySnapshotDiffTestSnapshotter) IdentityMapping() *idtools.IdentityMapping { return nil }

func (sn *applySnapshotDiffTestSnapshotter) Merge(_ context.Context, key string, diffs []snapshot.Diff, _ ...ctdsnapshots.Opt) error {
	sn.mu.Lock()
	defer sn.mu.Unlock()
	sn.mergeCalls = append(sn.mergeCalls, append([]snapshot.Diff(nil), diffs...))
	sn.snapshots[key] = ctdsnapshots.Info{Name: key}
	return nil
}

func newApplySnapshotDiffTestManager(t *testing.T) *snapshotManager {
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
		importLayerLocker:      locker.New(),
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
		leaseID:     "lease-" + snapshotID,
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
}
