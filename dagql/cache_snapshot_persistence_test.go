package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"gotest.tools/v3/assert"

	persistdb "github.com/dagger/dagger/dagql/persistdb"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/docker/docker/pkg/idtools"
)

type persistSnapshotValue struct {
	Name       string
	SnapshotID string
}

func (*persistSnapshotValue) Type() *ast.Type {
	return &ast.Type{
		NamedType: "PersistSnapshotValue",
		NonNull:   true,
	}
}

func (v *persistSnapshotValue) EncodePersistedObject(ctx context.Context, cache PersistedObjectCache) (PersistedObjectEncoding, error) {
	_ = ctx
	_ = cache
	payload, err := json.Marshal(struct {
		Name string `json:"name"`
	}{
		Name: v.Name,
	})
	if err != nil {
		return PersistedObjectEncoding{}, err
	}
	return PersistedObjectEncoding{
		JSON:          payload,
		SnapshotLinks: v.PersistedSnapshotRefLinks(),
	}, nil
}

func (v *persistSnapshotValue) PersistedSnapshotRefLinks() []PersistedSnapshotRefLink {
	if v == nil || v.SnapshotID == "" {
		return nil
	}
	return []PersistedSnapshotRefLink{{
		RefKey: v.SnapshotID,
		Role:   "snapshot",
	}}
}

type fakeSnapshotManager struct {
	persistentRows      bkcache.PersistentMetadataRows
	loadedRows          bkcache.PersistentMetadataRows
	snapshotSizes       map[string]int64
	snapshotMetadata    map[string]bkcache.SnapshotRecordMetadata
	snapshotSizeCalls   []string
	attachCalls         []struct{ LeaseID, SnapshotID string }
	removeCalls         []string
	deleteStaleKeep     map[string]struct{}
	deleteStaleCallSeen bool
}

func (*fakeSnapshotManager) Search(context.Context, string, bool) ([]bkcache.RefMetadata, error) {
	panic("unexpected Search call")
}

func (*fakeSnapshotManager) Get(context.Context, string, ...bkcache.RefOption) (bkcache.ImmutableRef, error) {
	panic("unexpected Get call")
}

func (*fakeSnapshotManager) GetBySnapshotID(context.Context, string, ...bkcache.RefOption) (bkcache.ImmutableRef, error) {
	panic("unexpected GetBySnapshotID call")
}

func (m *fakeSnapshotManager) SnapshotSize(ctx context.Context, snapshotID string) (int64, error) {
	_ = ctx
	if m.snapshotSizes == nil {
		panic("unexpected SnapshotSize call")
	}
	sizeBytes, ok := m.snapshotSizes[snapshotID]
	if !ok {
		return 0, fmt.Errorf("snapshot %q not found", snapshotID)
	}
	m.snapshotSizeCalls = append(m.snapshotSizeCalls, snapshotID)
	return sizeBytes, nil
}

func (m *fakeSnapshotManager) SnapshotRecordMetadata(ctx context.Context, snapshotID string) (bkcache.SnapshotRecordMetadata, bool, error) {
	_ = ctx
	if m.snapshotMetadata == nil {
		return bkcache.SnapshotRecordMetadata{}, false, nil
	}
	md, ok := m.snapshotMetadata[snapshotID]
	return md, ok, nil
}

func (*fakeSnapshotManager) New(context.Context, bkcache.ImmutableRef, ...bkcache.RefOption) (bkcache.MutableRef, error) {
	panic("unexpected New call")
}

func (*fakeSnapshotManager) GetMutable(context.Context, string, ...bkcache.RefOption) (bkcache.MutableRef, error) {
	panic("unexpected GetMutable call")
}

func (*fakeSnapshotManager) GetMutableBySnapshotID(context.Context, string, ...bkcache.RefOption) (bkcache.MutableRef, error) {
	panic("unexpected GetMutableBySnapshotID call")
}

func (*fakeSnapshotManager) ImportImage(context.Context, *bkcache.ImportedImage, bkcache.ImportImageOpts) (bkcache.ImmutableRef, error) {
	panic("unexpected ImportImage call")
}

func (*fakeSnapshotManager) ApplySnapshotDiff(context.Context, bkcache.ImmutableRef, bkcache.ImmutableRef, ...bkcache.RefOption) (bkcache.ImmutableRef, error) {
	panic("unexpected ApplySnapshotDiff call")
}

func (*fakeSnapshotManager) Merge(context.Context, []bkcache.ImmutableRef, ...bkcache.RefOption) (bkcache.ImmutableRef, error) {
	panic("unexpected Merge call")
}

func (*fakeSnapshotManager) IdentityMapping() *idtools.IdentityMapping {
	panic("unexpected IdentityMapping call")
}

func (m *fakeSnapshotManager) AttachLease(ctx context.Context, leaseID, snapshotID string) error {
	_ = ctx
	m.attachCalls = append(m.attachCalls, struct{ LeaseID, SnapshotID string }{
		LeaseID:    leaseID,
		SnapshotID: snapshotID,
	})
	return nil
}

func (m *fakeSnapshotManager) RemoveLease(ctx context.Context, leaseID string) error {
	_ = ctx
	m.removeCalls = append(m.removeCalls, leaseID)
	return nil
}

func (m *fakeSnapshotManager) LoadPersistentMetadata(rows bkcache.PersistentMetadataRows) error {
	m.loadedRows = rows
	return nil
}

func (m *fakeSnapshotManager) PersistentMetadataRows() bkcache.PersistentMetadataRows {
	return m.persistentRows
}

func (m *fakeSnapshotManager) DeleteStaleDaggerOwnerLeases(ctx context.Context, keep map[string]struct{}) error {
	_ = ctx
	m.deleteStaleCallSeen = true
	m.deleteStaleKeep = make(map[string]struct{}, len(keep))
	for leaseID := range keep {
		m.deleteStaleKeep[leaseID] = struct{}{}
	}
	return nil
}

func (*fakeSnapshotManager) Close() error {
	return nil
}

func TestCachePersistenceWorkerMirrorsSnapshotManagerMetadataRows(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	snapshotManager := &fakeSnapshotManager{
		persistentRows: bkcache.PersistentMetadataRows{
			SnapshotContent: []bkcache.SnapshotContentRow{{
				SnapshotID: "snap-root",
				Digest:     "sha256:1111111111111111111111111111111111111111111111111111111111111111",
			}},
			ImportedByBlob: []bkcache.ImportedLayerBlobRow{{
				ParentSnapshotID: "snap-parent",
				BlobDigest:       "sha256:2222222222222222222222222222222222222222222222222222222222222222",
				SnapshotID:       "snap-child-blob",
			}},
			ImportedByDiff: []bkcache.ImportedLayerDiffRow{{
				ParentSnapshotID: "snap-parent",
				DiffID:           "sha256:3333333333333333333333333333333333333333333333333333333333333333",
				SnapshotID:       "snap-child-diff",
			}},
		},
	}

	cacheIface, err := NewCache(ctx, dbPath, snapshotManager, nil)
	assert.NilError(t, err)
	c := cacheIface
	defer func() {
		assert.NilError(t, c.Close(context.Background()))
	}()

	assert.NilError(t, c.persistCurrentState(ctx))

	snapshotContentRows, err := c.pdb.ListMirrorSnapshotContentLinks(ctx)
	assert.NilError(t, err)
	assert.DeepEqual(t, snapshotContentRows, []persistdb.MirrorSnapshotContentLink{{
		SnapshotID: "snap-root",
		Digest:     "sha256:1111111111111111111111111111111111111111111111111111111111111111",
	}})

	importedByBlobRows, err := c.pdb.ListMirrorImportedLayerBlobIndex(ctx)
	assert.NilError(t, err)
	assert.DeepEqual(t, importedByBlobRows, []persistdb.MirrorImportedLayerBlobIndex{{
		ParentSnapshotID: "snap-parent",
		BlobDigest:       "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		SnapshotID:       "snap-child-blob",
	}})

	importedByDiffRows, err := c.pdb.ListMirrorImportedLayerDiffIndex(ctx)
	assert.NilError(t, err)
	assert.DeepEqual(t, importedByDiffRows, []persistdb.MirrorImportedLayerDiffIndex{{
		ParentSnapshotID: "snap-parent",
		DiffID:           "sha256:3333333333333333333333333333333333333333333333333333333333333333",
		SnapshotID:       "snap-child-diff",
	}})
}

func TestCachePersistenceImportHydratesSnapshotMetadataAndSyncsOwnerLeases(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	cacheA, err := NewCache(ctx, dbPath, &fakeSnapshotManager{
		persistentRows: bkcache.PersistentMetadataRows{
			SnapshotContent: []bkcache.SnapshotContentRow{{
				SnapshotID: "snapshot-a",
				Digest:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			}},
		},
	}, nil)
	assert.NilError(t, err)
	cA := cacheA

	key := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&persistSnapshotValue{}).Type()),
		Field: "persist-snapshot-owner",
	}

	resA, err := cA.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(&persistSnapshotValue{
			Name:       "x",
			SnapshotID: "snapshot-a",
		}), nil
	})
	assert.NilError(t, err)
	resultID := resA.cacheSharedResult().id

	cacheTestReleaseSession(t, cA, ctx)
	assert.NilError(t, cA.persistCurrentState(ctx))
	assert.NilError(t, cA.Close(context.Background()))

	snapshotManagerB := &fakeSnapshotManager{}
	cacheB, err := NewCache(ctx, dbPath, snapshotManagerB, nil)
	assert.NilError(t, err)
	cB := cacheB
	defer func() {
		assert.NilError(t, cB.Close(context.Background()))
	}()

	assert.DeepEqual(t, snapshotManagerB.loadedRows.SnapshotContent, []bkcache.SnapshotContentRow{{
		SnapshotID: "snapshot-a",
		Digest:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}})
	assert.Equal(t, 0, len(snapshotManagerB.loadedRows.ImportedByBlob))
	assert.Equal(t, 0, len(snapshotManagerB.loadedRows.ImportedByDiff))

	assert.Equal(t, 1, len(snapshotManagerB.attachCalls))
	assert.Equal(t, resultSnapshotLeaseID(resultID, "snapshot"), snapshotManagerB.attachCalls[0].LeaseID)
	assert.Equal(t, "snapshot-a", snapshotManagerB.attachCalls[0].SnapshotID)

	assert.Assert(t, snapshotManagerB.deleteStaleCallSeen)
	_, keepFound := snapshotManagerB.deleteStaleKeep[resultSnapshotLeaseID(resultID, "snapshot")]
	assert.Assert(t, keepFound)
}

func importedSnapshotLinkUsageTestCache(t *testing.T, ctx context.Context, snapshotID string, sizeBytes int64) (*Cache, *fakeSnapshotManager) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "cache.db")
	cacheA, err := NewCache(ctx, dbPath, &fakeSnapshotManager{}, nil)
	assert.NilError(t, err)
	cA := cacheA

	key := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&persistSnapshotValue{}).Type()),
		Field: "persist-snapshot-owner",
	}

	_, err = cA.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(&persistSnapshotValue{
			Name:       "x",
			SnapshotID: snapshotID,
		}), nil
	})
	assert.NilError(t, err)

	cacheTestReleaseSession(t, cA, ctx)
	assert.NilError(t, cA.persistCurrentState(ctx))
	assert.NilError(t, cA.Close(context.Background()))

	snapshotManagerB := &fakeSnapshotManager{
		snapshotSizes: map[string]int64{
			snapshotID: sizeBytes,
		},
	}
	cacheB, err := NewCache(ctx, dbPath, snapshotManagerB, nil)
	assert.NilError(t, err)
	return cacheB, snapshotManagerB
}

func TestCachePersistenceImportedSnapshotLinksContributeUsageBeforeDecode(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheB, snapshotManagerB := importedSnapshotLinkUsageTestCache(t, ctx, "snapshot-a", 1234)
	defer func() {
		assert.NilError(t, cacheB.Close(context.Background()))
	}()

	entries := cacheB.UsageEntriesAll(ctx)
	assert.Equal(t, 1, len(entries))
	assert.Equal(t, int64(1234), entries[0].SizeBytes)
	assert.DeepEqual(t, snapshotManagerB.snapshotSizeCalls, []string{"snapshot-a"})
}

func TestCachePruneThresholdUsesImportedSnapshotLinkUsage(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheB, snapshotManagerB := importedSnapshotLinkUsageTestCache(t, ctx, "snapshot-a", 1234)
	defer func() {
		assert.NilError(t, cacheB.Close(context.Background()))
	}()

	report, err := cacheB.Prune(ctx, []CachePrunePolicy{{
		All:          true,
		MaxUsedSpace: 100,
		TargetSpace:  1,
	}})
	assert.NilError(t, err)
	assert.Equal(t, 1, len(report.Entries))
	assert.Equal(t, int64(1234), report.Entries[0].SizeBytes)
	assert.Equal(t, int64(1234), report.ReclaimedBytes)
	assert.DeepEqual(t, snapshotManagerB.snapshotSizeCalls, []string{"snapshot-a"})

	entries := cacheB.UsageEntriesAll(ctx)
	assert.Equal(t, 0, len(entries))
}

func TestCachePersistenceWorkerUsesEncodedSnapshotLinks(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	cacheIface, err := NewCache(ctx, dbPath, &fakeSnapshotManager{}, nil)
	assert.NilError(t, err)
	c := cacheIface
	defer func() {
		assert.NilError(t, c.Close(context.Background()))
	}()

	key := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&persistSnapshotValue{}).Type()),
		Field: "persist-snapshot-owner",
	}

	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(&persistSnapshotValue{
			Name:       "x",
			SnapshotID: "snapshot-before",
		}), nil
	})
	assert.NilError(t, err)
	resultID := res.cacheSharedResult().id
	assert.DeepEqual(t, res.cacheSharedResult().loadSnapshotOwnerLinks(), []PersistedSnapshotRefLink{{
		RefKey: "snapshot-before",
		Role:   "snapshot",
	}})

	self := res.Unwrap().(*persistSnapshotValue)
	self.SnapshotID = "snapshot-after"

	assert.NilError(t, c.persistCurrentState(ctx))
	rows, err := c.pdb.ListMirrorResultSnapshotLinks(ctx)
	assert.NilError(t, err)
	assert.DeepEqual(t, rows, []persistdb.MirrorResultSnapshotLink{{
		ResultID: int64(resultID),
		RefKey:   "snapshot-after",
		Role:     "snapshot",
	}})
}

var _ bkcache.SnapshotManager = (*fakeSnapshotManager)(nil)
var _ PersistedObject = (*persistSnapshotValue)(nil)
var _ PersistedSnapshotRefLinkProvider = (*persistSnapshotValue)(nil)
