package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkconfig "github.com/dagger/dagger/engine/snapshots/config"
	snapshot "github.com/dagger/dagger/engine/snapshots/snapshotter"
	"github.com/dagger/dagger/internal/buildkit/client"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

type cacheVolumeTestQueryServer struct {
	*mockServer
	cacheManager bkcache.SnapshotManager
}

func (s *cacheVolumeTestQueryServer) SnapshotManager() bkcache.SnapshotManager {
	return s.cacheManager
}

type cacheVolumeTestSnapshotManager struct {
	immutableBySnapshotID map[string]bkcache.ImmutableRef
	mutableBySnapshotID   map[string]bkcache.MutableRef
	newResult             bkcache.MutableRef
	newResults            []bkcache.MutableRef

	getBySnapshotIDCalls        []string
	getMutableBySnapshotIDCalls []string
	newCalls                    []bkcache.ImmutableRef
}

func (*cacheVolumeTestSnapshotManager) Search(context.Context, string, bool) ([]bkcache.RefMetadata, error) {
	panic("unexpected Search call")
}

func (*cacheVolumeTestSnapshotManager) GetByBlob(context.Context, ocispecs.Descriptor, bkcache.ImmutableRef, ...bkcache.RefOption) (bkcache.ImmutableRef, error) {
	panic("unexpected GetByBlob call")
}

func (*cacheVolumeTestSnapshotManager) Get(context.Context, string, ...bkcache.RefOption) (bkcache.ImmutableRef, error) {
	panic("unexpected Get call")
}

func (m *cacheVolumeTestSnapshotManager) GetBySnapshotID(ctx context.Context, snapshotID string, _ ...bkcache.RefOption) (bkcache.ImmutableRef, error) {
	_ = ctx
	m.getBySnapshotIDCalls = append(m.getBySnapshotIDCalls, snapshotID)
	ref, ok := m.immutableBySnapshotID[snapshotID]
	if !ok {
		return nil, context.Canceled
	}
	return ref, nil
}

func (m *cacheVolumeTestSnapshotManager) New(_ context.Context, parent bkcache.ImmutableRef, _ ...bkcache.RefOption) (bkcache.MutableRef, error) {
	m.newCalls = append(m.newCalls, parent)
	if len(m.newResults) > 0 {
		ref := m.newResults[0]
		m.newResults = m.newResults[1:]
		return ref, nil
	}
	if m.newResult == nil {
		return nil, context.Canceled
	}
	return m.newResult, nil
}

func (*cacheVolumeTestSnapshotManager) GetMutable(context.Context, string, ...bkcache.RefOption) (bkcache.MutableRef, error) {
	panic("unexpected GetMutable call")
}

func (m *cacheVolumeTestSnapshotManager) GetMutableBySnapshotID(ctx context.Context, snapshotID string, _ ...bkcache.RefOption) (bkcache.MutableRef, error) {
	_ = ctx
	m.getMutableBySnapshotIDCalls = append(m.getMutableBySnapshotIDCalls, snapshotID)
	ref, ok := m.mutableBySnapshotID[snapshotID]
	if !ok {
		return nil, context.Canceled
	}
	return ref, nil
}

func (*cacheVolumeTestSnapshotManager) ImportImage(context.Context, *bkcache.ImportedImage, bkcache.ImportImageOpts) (bkcache.ImmutableRef, error) {
	panic("unexpected ImportImage call")
}

func (*cacheVolumeTestSnapshotManager) ApplySnapshotDiff(context.Context, bkcache.ImmutableRef, bkcache.ImmutableRef, ...bkcache.RefOption) (bkcache.ImmutableRef, error) {
	panic("unexpected ApplySnapshotDiff call")
}

func (*cacheVolumeTestSnapshotManager) Merge(context.Context, []bkcache.ImmutableRef, ...bkcache.RefOption) (bkcache.ImmutableRef, error) {
	panic("unexpected Merge call")
}

func (*cacheVolumeTestSnapshotManager) Diff(context.Context, bkcache.ImmutableRef, bkcache.ImmutableRef, ...bkcache.RefOption) (bkcache.ImmutableRef, error) {
	panic("unexpected Diff call")
}

func (*cacheVolumeTestSnapshotManager) AttachLease(context.Context, string, string) error {
	panic("unexpected AttachLease call")
}

func (*cacheVolumeTestSnapshotManager) RemoveLease(context.Context, string) error {
	panic("unexpected RemoveLease call")
}

func (*cacheVolumeTestSnapshotManager) LoadPersistentMetadata(bkcache.PersistentMetadataRows) error {
	panic("unexpected LoadPersistentMetadata call")
}

func (*cacheVolumeTestSnapshotManager) PersistentMetadataRows() bkcache.PersistentMetadataRows {
	return bkcache.PersistentMetadataRows{}
}

func (*cacheVolumeTestSnapshotManager) DeleteStaleDaggerOwnerLeases(context.Context, map[string]struct{}) error {
	panic("unexpected DeleteStaleDaggerOwnerLeases call")
}

func (*cacheVolumeTestSnapshotManager) Close() error {
	return nil
}

type cacheVolumeTestImmutableRef struct {
	id         string
	snapshotID string
	size       int64
}

func (r *cacheVolumeTestImmutableRef) Mount(context.Context, bool) (snapshot.Mountable, error) {
	panic("unexpected Mount call")
}

func (r *cacheVolumeTestImmutableRef) ID() string {
	return r.id
}

func (r *cacheVolumeTestImmutableRef) SnapshotID() string {
	return r.snapshotID
}

func (*cacheVolumeTestImmutableRef) Release(context.Context) error {
	return nil
}

func (r *cacheVolumeTestImmutableRef) Size(context.Context) (int64, error) {
	return r.size, nil
}

func (*cacheVolumeTestImmutableRef) Clone() bkcache.ImmutableRef {
	panic("unexpected Clone call")
}

func (*cacheVolumeTestImmutableRef) ExportChain(context.Context, bkconfig.RefConfig) (*bkcache.ExportChain, error) {
	panic("unexpected ExportChain call")
}

func (*cacheVolumeTestImmutableRef) Finalize(context.Context) error {
	panic("unexpected Finalize call")
}

func (*cacheVolumeTestImmutableRef) Extract(context.Context, bksession.Group) error {
	panic("unexpected Extract call")
}

func (*cacheVolumeTestImmutableRef) FileList(context.Context, bksession.Group) ([]string, error) {
	panic("unexpected FileList call")
}

func (*cacheVolumeTestImmutableRef) GetDescription() string {
	return ""
}

func (*cacheVolumeTestImmutableRef) SetDescription(string) error {
	return nil
}

func (*cacheVolumeTestImmutableRef) GetCreatedAt() time.Time {
	return time.Time{}
}

func (*cacheVolumeTestImmutableRef) SetCreatedAt(time.Time) error {
	return nil
}

func (*cacheVolumeTestImmutableRef) HasCachePolicyDefault() bool {
	return false
}

func (*cacheVolumeTestImmutableRef) SetCachePolicyDefault() error {
	return nil
}

func (*cacheVolumeTestImmutableRef) HasCachePolicyRetain() bool {
	return false
}

func (*cacheVolumeTestImmutableRef) SetCachePolicyRetain() error {
	return nil
}

func (*cacheVolumeTestImmutableRef) GetLayerType() string {
	return ""
}

func (*cacheVolumeTestImmutableRef) SetLayerType(string) error {
	return nil
}

func (*cacheVolumeTestImmutableRef) GetRecordType() client.UsageRecordType {
	return ""
}

func (*cacheVolumeTestImmutableRef) SetRecordType(client.UsageRecordType) error {
	return nil
}

func (*cacheVolumeTestImmutableRef) GetEqualMutable() (bkcache.RefMetadata, bool) {
	return nil, false
}

func (*cacheVolumeTestImmutableRef) GetString(string) string {
	return ""
}

func (*cacheVolumeTestImmutableRef) Get(string) *bkcache.Value {
	return nil
}

func (*cacheVolumeTestImmutableRef) SetString(string, string, string) error {
	return nil
}

func (*cacheVolumeTestImmutableRef) GetExternal(string) ([]byte, error) {
	return nil, nil
}

func (*cacheVolumeTestImmutableRef) SetExternal(string, []byte) error {
	return nil
}

func (*cacheVolumeTestImmutableRef) ClearValueAndIndex(string, string) error {
	return nil
}

type cacheVolumeTestMutableRef struct {
	cacheVolumeTestImmutableRef
}

func (*cacheVolumeTestMutableRef) Commit(context.Context) (bkcache.ImmutableRef, error) {
	panic("unexpected Commit call")
}

func (*cacheVolumeTestMutableRef) InvalidateSize(context.Context) error {
	return nil
}

func TestCacheVolumeUsageIdentityUsesLiveSnapshotID(t *testing.T) {
	t.Parallel()

	ref := &cacheVolumeTestMutableRef{
		cacheVolumeTestImmutableRef: cacheVolumeTestImmutableRef{
			id:         "mutable-1",
			snapshotID: "snapshot-123",
		},
	}
	cache := NewCache("cache-key", "ns", dagql.Optional[DirectoryID]{}, CacheSharingModeShared, "")
	cache.snapshots = []bkcache.MutableRef{ref}

	require.Equal(t, []string{"snapshot-123"}, cache.CacheUsageIdentities())
}

func TestCacheVolumeUsageSizeUsesLiveSnapshotID(t *testing.T) {
	t.Parallel()

	ref := &cacheVolumeTestImmutableRef{
		id:         "immutable-1",
		snapshotID: "snapshot-123",
		size:       42,
	}
	cache := NewCache("cache-key", "ns", dagql.Optional[DirectoryID]{}, CacheSharingModeShared, "")
	cache.snapshots = []bkcache.MutableRef{&cacheVolumeTestMutableRef{cacheVolumeTestImmutableRef: *ref}}

	size, ok, err := cache.CacheUsageSize(context.Background(), "snapshot-123")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(42), size)
}

func TestCacheVolumeEncodeDecodePersistsSourceID(t *testing.T) {
	t.Parallel()

	sourceCallID := call.NewEngineResultID(17, call.NewType((&Directory{}).Type()))
	sourceID := dagql.NewID[*Directory](sourceCallID)

	cache := NewCache(
		"cache-key",
		"ns",
		dagql.Optional[DirectoryID]{
			Valid: true,
			Value: sourceID,
		},
		CacheSharingModeLocked,
		"1000:1000",
	)

	payload, err := cache.EncodePersistedObject(context.Background(), nil)
	require.NoError(t, err)

	var raw persistedCacheVolumePayload
	require.NoError(t, json.Unmarshal(payload, &raw))
	require.NotEmpty(t, raw.SourceID)

	decodedAny, err := (&CacheVolume{}).DecodePersistedObject(context.Background(), nil, 0, nil, payload)
	require.NoError(t, err)
	decoded := decodedAny.(*CacheVolume)
	require.True(t, decoded.Source.Valid)

	encodedSource, err := decoded.Source.Value.Encode()
	require.NoError(t, err)
	require.Equal(t, raw.SourceID, encodedSource)
}

func TestCacheVolumeInitializeSnapshotCreatesMutableSnapshot(t *testing.T) {
	t.Parallel()

	ref := &cacheVolumeTestMutableRef{
		cacheVolumeTestImmutableRef: cacheVolumeTestImmutableRef{
			id:         "mutable-1",
			snapshotID: "snapshot-123",
		},
	}
	manager := &cacheVolumeTestSnapshotManager{
		newResult: ref,
	}
	query := &Query{
		Server: &cacheVolumeTestQueryServer{
			mockServer:   &mockServer{},
			cacheManager: manager,
		},
	}
	ctx := ContextWithQuery(context.Background(), query)

	cache := NewCache("cache-key", "ns", dagql.Optional[DirectoryID]{}, CacheSharingModeShared, "")

	require.NoError(t, cache.InitializeSnapshot(ctx))
	require.Len(t, manager.newCalls, 1)
	require.Nil(t, manager.newCalls[0])
	require.Equal(t, ref, cache.getSnapshot())
	require.Equal(t, "/", cache.getSnapshotSelector())
}

func TestPrivateCacheVolumeAcquireMountReusesIdleSnapshot(t *testing.T) {
	t.Parallel()

	ref := &cacheVolumeTestMutableRef{
		cacheVolumeTestImmutableRef: cacheVolumeTestImmutableRef{
			id:         "mutable-1",
			snapshotID: "snapshot-1",
		},
	}
	manager := &cacheVolumeTestSnapshotManager{
		newResult: ref,
	}
	query := &Query{
		Server: &cacheVolumeTestQueryServer{
			mockServer:   &mockServer{},
			cacheManager: manager,
		},
	}
	ctx := ContextWithQuery(context.Background(), query)

	cache := NewCache("cache-key", "ns", dagql.Optional[DirectoryID]{}, CacheSharingModePrivate, "")

	mount1, err := cache.acquireMount(ctx)
	require.NoError(t, err)
	require.Equal(t, ref, mount1.ref)
	require.NoError(t, mount1.release(ctx))

	mount2, err := cache.acquireMount(ctx)
	require.NoError(t, err)
	require.Equal(t, ref, mount2.ref)
	require.NoError(t, mount2.release(ctx))
	require.Len(t, manager.newCalls, 1)
}

func TestPrivateCacheVolumeAcquireMountCreatesSnapshotWhenActive(t *testing.T) {
	t.Parallel()

	ref1 := &cacheVolumeTestMutableRef{
		cacheVolumeTestImmutableRef: cacheVolumeTestImmutableRef{
			id:         "mutable-1",
			snapshotID: "snapshot-1",
		},
	}
	ref2 := &cacheVolumeTestMutableRef{
		cacheVolumeTestImmutableRef: cacheVolumeTestImmutableRef{
			id:         "mutable-2",
			snapshotID: "snapshot-2",
		},
	}
	manager := &cacheVolumeTestSnapshotManager{
		newResults: []bkcache.MutableRef{ref1, ref2},
	}
	query := &Query{
		Server: &cacheVolumeTestQueryServer{
			mockServer:   &mockServer{},
			cacheManager: manager,
		},
	}
	ctx := ContextWithQuery(context.Background(), query)

	cache := NewCache("cache-key", "ns", dagql.Optional[DirectoryID]{}, CacheSharingModePrivate, "")

	mount1, err := cache.acquireMount(ctx)
	require.NoError(t, err)
	require.Equal(t, ref1, mount1.ref)

	mount2, err := cache.acquireMount(ctx)
	require.NoError(t, err)
	require.Equal(t, ref2, mount2.ref)
	require.Len(t, manager.newCalls, 2)

	require.NoError(t, mount1.release(ctx))
	require.NoError(t, mount2.release(ctx))

	mount3, err := cache.acquireMount(ctx)
	require.NoError(t, err)
	require.Equal(t, ref1, mount3.ref)
	require.NoError(t, mount3.release(ctx))
	require.Len(t, manager.newCalls, 2)
}

var _ bkcache.ImmutableRef = (*cacheVolumeTestImmutableRef)(nil)
var _ bkcache.MutableRef = (*cacheVolumeTestMutableRef)(nil)
var _ bkcache.SnapshotManager = (*cacheVolumeTestSnapshotManager)(nil)
var _ dagql.PersistedObject = (*CacheVolume)(nil)
