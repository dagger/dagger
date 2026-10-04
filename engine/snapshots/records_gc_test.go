package snapshots

import (
	"context"
	"errors"
	"testing"

	ctdsnapshots "github.com/containerd/containerd/v2/core/snapshots"
	"github.com/stretchr/testify/require"
)

// contenthash stores the marshalled path tree of a whole snapshot under this
// one key. It is by far the largest thing a cacheMetadata holds, so it is what
// these tests use to stand in for "the metadata is still resident".
const testContentHashKey = "buildkit.contenthash.v0"

func addTestRecord(t *testing.T, cm *snapshotManager, id, snapshotID string, committed, mutable bool) {
	t.Helper()

	cm.mu.Lock()
	defer cm.mu.Unlock()

	md := cm.ensureMetadata(id)
	require.NoError(t, md.queueSnapshotID(snapshotID))
	require.NoError(t, md.queueCommitted(committed))
	require.NoError(t, md.commitMetadata())
	require.NoError(t, md.SetExternal(testContentHashKey, []byte("path tree for "+snapshotID)))

	cm.records[id] = &cacheRecord{cm: cm, md: md, mutable: mutable, locked: mutable}
	cm.Snapshotter.(*applySnapshotDiffTestSnapshotter).snapshots[snapshotID] = ctdsnapshots.Info{Name: snapshotID}
}

func removeTestSnapshot(t *testing.T, cm *snapshotManager, snapshotID string) {
	t.Helper()
	require.NoError(t, cm.Snapshotter.Remove(context.Background(), snapshotID))
}

func requireResident(t *testing.T, cm *snapshotManager, id string) {
	t.Helper()
	md, ok := cm.metadataStore.get(id)
	require.True(t, ok, "metadata for %s should still be resident", id)
	dt, err := md.GetExternal(testContentHashKey)
	require.NoError(t, err)
	require.NotEmpty(t, dt)
}

func requireDropped(t *testing.T, cm *snapshotManager, id string) {
	t.Helper()
	_, ok := cm.metadataStore.get(id)
	require.False(t, ok, "metadata for %s should have been dropped", id)
	cm.mu.Lock()
	defer cm.mu.Unlock()
	_, ok = cm.records[id]
	require.False(t, ok, "record for %s should have been dropped", id)
}

// Releasing an immutable ref deliberately keeps its metadata: the snapshot is
// still on disk and the contenthash tree is still worth having. This is the
// behaviour that makes a reconcile necessary, so it is worth pinning.
func TestPruneStaleRecordsKeepsReleasedRefsWithLiveSnapshots(t *testing.T) {
	cm := newApplySnapshotDiffTestManager(t)
	ref := addApplySnapshotDiffTestImmutable(t, cm, "live")
	cm.mu.Lock()
	require.NoError(t, cm.records["live"].md.queueCommitted(true))
	require.NoError(t, cm.records["live"].md.commitMetadata())
	require.NoError(t, cm.records["live"].md.SetExternal(testContentHashKey, []byte("path tree")))
	cm.mu.Unlock()

	require.NoError(t, ref.Release(context.Background()))
	requireResident(t, cm, "live")

	removed, err := cm.PruneStaleRecords(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, removed)
	requireResident(t, cm, "live")
}

// Once containerd's lease GC has deleted the snapshot, nothing else ever
// reaches cacheRecord.remove for a committed record, so without this sweep the
// metadata — contenthash tree included — stays reachable for the lifetime of
// the process.
func TestPruneStaleRecordsDropsRecordsWhoseSnapshotIsGone(t *testing.T) {
	cm := newApplySnapshotDiffTestManager(t)
	addTestRecord(t, cm, "evicted", "evicted", true, false)
	addTestRecord(t, cm, "kept", "kept", true, false)

	removeTestSnapshot(t, cm, "evicted")

	removed, err := cm.PruneStaleRecords(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, removed)

	requireDropped(t, cm, "evicted")
	requireResident(t, cm, "kept")
}

// A record's id and its snapshot id are independent — ApplySnapshotDiff and
// Merge mint a fresh id and carry the snapshot id in metadata — so liveness
// has to be judged on the snapshot id.
func TestPruneStaleRecordsUsesSnapshotIDNotRecordID(t *testing.T) {
	cm := newApplySnapshotDiffTestManager(t)
	addTestRecord(t, cm, "record-id", "snapshot-id", true, false)

	removed, err := cm.PruneStaleRecords(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, removed, "the snapshot is live under its own id")
	requireResident(t, cm, "record-id")

	removeTestSnapshot(t, cm, "snapshot-id")

	removed, err = cm.PruneStaleRecords(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	requireDropped(t, cm, "record-id")
}

// Mutable and uncommitted entries may be mid-creation, with the snapshot not
// written yet, so a sweep must leave them alone however absent they look.
func TestPruneStaleRecordsSkipsMutableAndUncommitted(t *testing.T) {
	cm := newApplySnapshotDiffTestManager(t)
	addTestRecord(t, cm, "mutable", "mutable", true, true)
	addTestRecord(t, cm, "uncommitted", "uncommitted", false, false)

	removeTestSnapshot(t, cm, "mutable")
	removeTestSnapshot(t, cm, "uncommitted")

	removed, err := cm.PruneStaleRecords(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, removed)
	requireResident(t, cm, "mutable")
	requireResident(t, cm, "uncommitted")
}

// Metadata can outlive its record entirely — rehydrateSnapshotMetadataLocked
// creates entries with no record at all — and nothing clears those either.
func TestPruneStaleRecordsDropsRecordlessMetadata(t *testing.T) {
	cm := newApplySnapshotDiffTestManager(t)

	cm.mu.Lock()
	md := cm.ensureMetadata("orphan")
	require.NoError(t, md.queueSnapshotID("orphan"))
	require.NoError(t, md.queueCommitted(true))
	require.NoError(t, md.commitMetadata())
	require.NoError(t, md.SetExternal(testContentHashKey, []byte("path tree")))
	cm.mu.Unlock()

	requireResident(t, cm, "orphan")

	removed, err := cm.PruneStaleRecords(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	requireDropped(t, cm, "orphan")
}

// A walk that fails tells us nothing about what is live, so it must drop
// nothing rather than treat an unreadable snapshotter as an empty one.
func TestPruneStaleRecordsDropsNothingWhenTheWalkFails(t *testing.T) {
	cm := newApplySnapshotDiffTestManager(t)
	addTestRecord(t, cm, "evicted", "evicted", true, false)
	removeTestSnapshot(t, cm, "evicted")

	sn := cm.Snapshotter.(*applySnapshotDiffTestSnapshotter)
	cm.Snapshotter = &walkFailingSnapshotter{
		applySnapshotDiffTestSnapshotter: sn,
		err:                              errors.New("boom"),
	}

	removed, err := cm.PruneStaleRecords(context.Background())
	require.Error(t, err)
	require.Equal(t, 0, removed)
	requireResident(t, cm, "evicted")
}

// More stale records than one batch holds, to exercise the batching loop.
func TestPruneStaleRecordsDropsMoreThanOneBatch(t *testing.T) {
	cm := newApplySnapshotDiffTestManager(t)
	total := pruneStaleRecordsBatch*2 + 7
	for i := range total {
		id := "stale-" + string(rune('a'+i%26)) + "-" + itoa(i)
		addTestRecord(t, cm, id, id, true, false)
		removeTestSnapshot(t, cm, id)
	}

	removed, err := cm.PruneStaleRecords(context.Background())
	require.NoError(t, err)
	require.Equal(t, total, removed)

	cm.mu.Lock()
	require.Empty(t, cm.records)
	cm.mu.Unlock()
	require.Empty(t, cm.metadataStore.ids())
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

type walkFailingSnapshotter struct {
	*applySnapshotDiffTestSnapshotter
	err error
}

func (sn *walkFailingSnapshotter) Walk(context.Context, ctdsnapshots.WalkFunc, ...string) error {
	return sn.err
}
