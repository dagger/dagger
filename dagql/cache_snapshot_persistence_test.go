package dagql

import (
	"context"
	"encoding/json"
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

func (v *persistSnapshotValue) EncodePersistedObject(ctx context.Context, cache PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	_ = cache
	return json.Marshal(struct {
		Name string `json:"name"`
	}{
		Name: v.Name,
	})
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

	cacheIface, err := NewCache(ctx, dbPath, snapshotManager)
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
	})
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
	cacheB, err := NewCache(ctx, dbPath, snapshotManagerB)
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
	assert.Equal(t, resultSnapshotLeaseID(resultID, "snapshot", ""), snapshotManagerB.attachCalls[0].LeaseID)
	assert.Equal(t, "snapshot-a", snapshotManagerB.attachCalls[0].SnapshotID)

	assert.Assert(t, snapshotManagerB.deleteStaleCallSeen)
	_, keepFound := snapshotManagerB.deleteStaleKeep[resultSnapshotLeaseID(resultID, "snapshot", "")]
	assert.Assert(t, keepFound)
}

var _ bkcache.SnapshotManager = (*fakeSnapshotManager)(nil)
var _ PersistedObject = (*persistSnapshotValue)(nil)
var _ PersistedSnapshotRefLinkProvider = (*persistSnapshotValue)(nil)
