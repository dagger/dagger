package core

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkconfig "github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/internal/buildkit/client"
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
	snapshotSizes         map[string]int64
	lockMutableSnapshotID bool
	mutableSnapshotOpen   map[string]bool
	loadedRows            bkcache.PersistentMetadataRows
	attachCalls           []struct{ leaseID, snapshotID string }
	removeCalls           []string
	deleteStaleKeep       map[string]struct{}

	getBySnapshotIDCalls        []string
	getMutableBySnapshotIDCalls []string
	snapshotSizeCalls           []string
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

func (*cacheVolumeTestSnapshotManager) Scratch(context.Context) (bkcache.ImmutableRef, error) {
	panic("unexpected Scratch call")
}

func (m *cacheVolumeTestSnapshotManager) SnapshotSize(ctx context.Context, snapshotID string) (int64, error) {
	_ = ctx
	m.snapshotSizeCalls = append(m.snapshotSizeCalls, snapshotID)
	size, ok := m.snapshotSizes[snapshotID]
	if !ok {
		return 0, context.Canceled
	}
	return size, nil
}

func (*cacheVolumeTestSnapshotManager) SnapshotRecordMetadata(context.Context, string) (bkcache.SnapshotRecordMetadata, bool, error) {
	panic("unexpected SnapshotRecordMetadata call")
}

func (m *cacheVolumeTestSnapshotManager) New(_ context.Context, parent bkcache.ImmutableRef, _ ...bkcache.RefOption) (bkcache.MutableRef, error) {
	m.newCalls = append(m.newCalls, parent)
	if m.newResult == nil {
		return nil, context.Canceled
	}
	return m.newResult, nil
}

func (*cacheVolumeTestSnapshotManager) GetMutable(context.Context, string, ...bkcache.RefOption) (bkcache.MutableRef, error) {
	panic("unexpected GetMutable call")
}

func (m *cacheVolumeTestSnapshotManager) GetMutableBySnapshotID(ctx context.Context, snapshotID string, _ ...bkcache.RefOption) (bkcache.MutableRef, error) {
	m.getMutableBySnapshotIDCalls = append(m.getMutableBySnapshotIDCalls, snapshotID)
	ref, ok := m.mutableBySnapshotID[snapshotID]
	if !ok {
		return nil, context.Canceled
	}
	if m.lockMutableSnapshotID {
		if m.mutableSnapshotOpen == nil {
			m.mutableSnapshotOpen = map[string]bool{}
		}
		if m.mutableSnapshotOpen[snapshotID] {
			return nil, errors.New("locked")
		}
		m.mutableSnapshotOpen[snapshotID] = true

		size, err := ref.Size(ctx)
		if err != nil {
			return nil, err
		}
		ref = &cacheVolumeTestMutableRef{
			cacheVolumeTestImmutableRef: cacheVolumeTestImmutableRef{
				id:         ref.ID(),
				snapshotID: ref.SnapshotID(),
				size:       size,
				release: func(context.Context) error {
					m.mutableSnapshotOpen[snapshotID] = false
					return nil
				},
			},
		}
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

func (m *cacheVolumeTestSnapshotManager) AttachLease(ctx context.Context, leaseID, snapshotID string) error {
	_ = ctx
	m.attachCalls = append(m.attachCalls, struct{ leaseID, snapshotID string }{
		leaseID:    leaseID,
		snapshotID: snapshotID,
	})
	return nil
}

func (m *cacheVolumeTestSnapshotManager) RemoveLease(ctx context.Context, leaseID string) error {
	_ = ctx
	m.removeCalls = append(m.removeCalls, leaseID)
	return nil
}

func (m *cacheVolumeTestSnapshotManager) LoadPersistentMetadata(rows bkcache.PersistentMetadataRows) error {
	m.loadedRows = rows
	return nil
}

func (*cacheVolumeTestSnapshotManager) PersistentMetadataRows() bkcache.PersistentMetadataRows {
	return bkcache.PersistentMetadataRows{}
}

func (m *cacheVolumeTestSnapshotManager) DeleteStaleDaggerOwnerLeases(ctx context.Context, keep map[string]struct{}) error {
	_ = ctx
	m.deleteStaleKeep = make(map[string]struct{}, len(keep))
	for leaseID := range keep {
		m.deleteStaleKeep[leaseID] = struct{}{}
	}
	return nil
}

func (*cacheVolumeTestSnapshotManager) Close() error {
	return nil
}

type cacheVolumeTestImmutableRef struct {
	id         string
	snapshotID string
	size       int64
	release    func(context.Context) error
}

func (r *cacheVolumeTestImmutableRef) Mount(context.Context, bool) (bkcache.MountableRef, error) {
	panic("unexpected Mount call")
}

func (r *cacheVolumeTestImmutableRef) ID() string {
	return r.id
}

func (r *cacheVolumeTestImmutableRef) SnapshotID() string {
	return r.snapshotID
}

func (r *cacheVolumeTestImmutableRef) Release(ctx context.Context) error {
	if r.release != nil {
		return r.release(ctx)
	}
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

func (*cacheVolumeTestMutableRef) CommitWithUsage(context.Context, snapshots.Usage) (bkcache.ImmutableRef, error) {
	panic("unexpected CommitWithUsage call")
}

func (*cacheVolumeTestMutableRef) InvalidateSize(context.Context) error {
	return nil
}

type cacheVolumeTestPersistedObjectCache struct {
	resultID uint64
	seen     []dagql.AnyResult
}

func (c *cacheVolumeTestPersistedObjectCache) PersistedResultID(res dagql.AnyResult) (uint64, error) {
	c.seen = append(c.seen, res)
	return c.resultID, nil
}

func cacheVolumeTestDirectoryResult(t *testing.T, srv *dagql.Server, op string) dagql.ObjectResult[*Directory] {
	t.Helper()
	res, err := dagql.NewObjectResultForCall(&Directory{}, srv, &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: op,
		Type:        dagql.NewResultCallType((&Directory{}).Type()),
	})
	require.NoError(t, err)
	return res
}

func TestCacheVolumeUsageIdentityUsesLiveSnapshotID(t *testing.T) {
	t.Parallel()

	ref := &cacheVolumeTestMutableRef{
		cacheVolumeTestImmutableRef: cacheVolumeTestImmutableRef{
			id:         "mutable-1",
			snapshotID: "snapshot-123",
		},
	}
	cache := NewCache("cache-key", "ns", dagql.Null[dagql.ObjectResult[*Directory]](), CacheSharingModeShared, "")
	cache.snapshot = ref

	require.Equal(t, []string{"snapshot-123"}, cache.CacheUsageIdentities())
}

func TestCacheVolumeUsageSizeUsesLiveSnapshotID(t *testing.T) {
	t.Parallel()

	ref := &cacheVolumeTestImmutableRef{
		id:         "immutable-1",
		snapshotID: "snapshot-123",
		size:       42,
	}
	cache := NewCache("cache-key", "ns", dagql.Null[dagql.ObjectResult[*Directory]](), CacheSharingModeShared, "")
	cache.snapshot = &cacheVolumeTestMutableRef{cacheVolumeTestImmutableRef: *ref}

	size, ok, err := cache.CacheUsageSize(context.Background(), "snapshot-123")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(42), size)
}

func TestCacheVolumeUsageSizeUsesPersistedSnapshotID(t *testing.T) {
	t.Parallel()

	manager := &cacheVolumeTestSnapshotManager{
		snapshotSizes: map[string]int64{
			"snapshot-123": 42,
		},
	}
	query := &Query{
		Server: &cacheVolumeTestQueryServer{
			mockServer:   &mockServer{},
			cacheManager: manager,
		},
	}
	ctx := ContextWithQuery(context.Background(), query)

	cache := NewCache("cache-key", "ns", dagql.Null[dagql.ObjectResult[*Directory]](), CacheSharingModeShared, "")
	cache.snapshotID = "snapshot-123"

	size, ok, err := cache.CacheUsageSize(ctx, "snapshot-123")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(42), size)
	require.Equal(t, []string{"snapshot-123"}, manager.snapshotSizeCalls)
}

func TestCacheVolumeEncodePersistsSourceResultID(t *testing.T) {
	t.Parallel()

	query := &Query{Server: &cacheVolumeTestQueryServer{mockServer: &mockServer{}}}
	srv := newCoreDagqlServerForTest(t, query)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Directory]{}))
	source := cacheVolumeTestDirectoryResult(t, srv, "cache-volume-source")
	persisted := &cacheVolumeTestPersistedObjectCache{resultID: 17}

	cache := NewCache(
		"cache-key",
		"ns",
		dagql.NonNull(source),
		CacheSharingModeLocked,
		"1000:1000",
	)

	payload, err := cache.EncodePersistedObject(context.Background(), persisted)
	require.NoError(t, err)

	var raw persistedCacheVolumePayload
	require.NoError(t, json.Unmarshal(payload.JSON, &raw))
	require.Equal(t, uint64(17), raw.SourceResultID)
	require.Len(t, persisted.seen, 1)

	seenSource, ok := persisted.seen[0].(dagql.ObjectResult[*Directory])
	require.True(t, ok)
	require.Same(t, source.Self(), seenSource.Self())
}

func TestCacheVolumeAttachDependencyResultsAttachesSource(t *testing.T) {
	t.Parallel()

	query := &Query{Server: &cacheVolumeTestQueryServer{mockServer: &mockServer{}}}
	srv := newCoreDagqlServerForTest(t, query)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Directory]{}))
	source := cacheVolumeTestDirectoryResult(t, srv, "cache-volume-source")
	attachedSource := cacheVolumeTestDirectoryResult(t, srv, "cache-volume-attached-source")
	cache := NewCache("cache-key", "ns", dagql.NonNull(source), CacheSharingModeShared, "")

	deps, err := cache.AttachDependencyResults(context.Background(), nil, func(res dagql.AnyResult) (dagql.AnyResult, error) {
		seenSource, ok := res.(dagql.ObjectResult[*Directory])
		require.True(t, ok)
		require.Same(t, source.Self(), seenSource.Self())
		return attachedSource, nil
	})
	require.NoError(t, err)
	require.Len(t, deps, 1)
	require.Same(t, attachedSource.Self(), deps[0].(dagql.ObjectResult[*Directory]).Self())
	require.Same(t, attachedSource.Self(), cache.Source.Value.Self())
}

func TestCacheVolumeLockKeyUsesSourceID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	query := &Query{Server: &cacheVolumeTestQueryServer{mockServer: &mockServer{}}}
	srv := newCoreDagqlServerForTest(t, query)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Directory]{}))
	dagCache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, dagCache.Close(context.Background()))
	})
	ctx = ContextWithQuery(dagql.ContextWithCache(ctx, dagCache), query)

	sourceCall := &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: "cache-volume-source",
		Type:        dagql.NewResultCallType((&Directory{}).Type()),
	}
	initialSource := cacheVolumeTestDirectoryResult(t, srv, "cache-volume-source")
	sourceAny, err := dagCache.GetOrInitCall(ctx, "session", srv, &dagql.CallRequest{
		ResultCall: sourceCall,
	}, dagql.ValueFunc(initialSource))
	require.NoError(t, err)
	source, ok := sourceAny.(dagql.ObjectResult[*Directory])
	require.True(t, ok)
	sourceID, err := source.ID()
	require.NoError(t, err)
	encodedSourceID, err := sourceID.Encode()
	require.NoError(t, err)

	cache := NewCache("cache-key", "ns", dagql.NonNull(source), CacheSharingModeLocked, "")
	lockKey, err := cache.lockKey()
	require.NoError(t, err)
	require.Contains(t, lockKey, encodedSourceID)
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

	cache := NewCache("cache-key", "ns", dagql.Null[dagql.ObjectResult[*Directory]](), CacheSharingModeShared, "")

	require.NoError(t, cache.InitializeSnapshot(ctx))
	require.Len(t, manager.newCalls, 1)
	require.Nil(t, manager.newCalls[0])
	require.Equal(t, ref, cache.getSnapshot())
	require.Equal(t, "/", cache.getSnapshotSelector())
}

func TestCacheVolumeDecodeDistinctPersistedResultsSharingSnapshot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	const snapshotID = "cache-volume-snapshot"

	cacheCall := func(field string) *dagql.ResultCall {
		return &dagql.ResultCall{
			Kind:  dagql.ResultCallKindField,
			Type:  dagql.NewResultCallType((&CacheVolume{}).Type()),
			Field: field,
		}
	}
	cacheResult := func(t *testing.T, srv *dagql.Server, call *dagql.ResultCall, key string) dagql.ObjectResult[*CacheVolume] {
		t.Helper()
		cache := NewCache(key, "ns", dagql.Null[dagql.ObjectResult[*Directory]](), CacheSharingModeLocked, "")
		cache.snapshot = &cacheVolumeTestMutableRef{
			cacheVolumeTestImmutableRef: cacheVolumeTestImmutableRef{
				id:         key + "-mutable",
				snapshotID: snapshotID,
			},
		}
		res, err := dagql.NewObjectResultForCall(cache, srv, call)
		require.NoError(t, err)
		return res
	}
	cacheServer := func(manager bkcache.SnapshotManager) (*dagql.Server, *Query) {
		query := &Query{
			Server: &cacheVolumeTestQueryServer{
				mockServer:   &mockServer{},
				cacheManager: manager,
			},
		}
		srv := newCoreDagqlServerForTest(t, query)
		srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*CacheVolume]{}))
		return srv, query
	}

	callA := cacheCall("cache-volume-a")
	callB := cacheCall("cache-volume-b")

	cacheA, err := dagql.NewCache(ctx, dbPath, nil, nil)
	require.NoError(t, err)
	srvA, _ := cacheServer(nil)
	ctxA := dagql.ContextWithCache(ctx, cacheA)

	resA, err := cacheA.GetOrInitCall(ctxA, "session-a", srvA, &dagql.CallRequest{
		ResultCall:    callA,
		IsPersistable: true,
	}, dagql.ValueFunc(cacheResult(t, srvA, callA, "cache-a")))
	require.NoError(t, err)
	resB, err := cacheA.GetOrInitCall(ctxA, "session-a", srvA, &dagql.CallRequest{
		ResultCall:    callB,
		IsPersistable: true,
	}, dagql.ValueFunc(cacheResult(t, srvA, callB, "cache-b")))
	require.NoError(t, err)

	resultIDA, err := cacheA.PersistedResultID(resA)
	require.NoError(t, err)
	resultIDB, err := cacheA.PersistedResultID(resB)
	require.NoError(t, err)
	require.NotEqual(t, resultIDA, resultIDB)

	require.NoError(t, cacheA.ReleaseSession(ctxA, "session-a"))
	require.NoError(t, cacheA.Close(ctx))

	managerB := &cacheVolumeTestSnapshotManager{
		mutableBySnapshotID: map[string]bkcache.MutableRef{
			snapshotID: &cacheVolumeTestMutableRef{
				cacheVolumeTestImmutableRef: cacheVolumeTestImmutableRef{
					id:         "cache-volume-mutable",
					snapshotID: snapshotID,
				},
			},
		},
		lockMutableSnapshotID: true,
	}
	cacheB, err := dagql.NewCache(ctx, dbPath, managerB, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cacheB.Close(context.Background()))
	})
	srvB, queryB := cacheServer(managerB)
	ctxB := ContextWithQuery(dagql.ContextWithCache(ctx, cacheB), queryB)

	loadedA, err := cacheB.GetOrInitCall(ctxB, "session-b", srvB, &dagql.CallRequest{
		ResultCall:    callA,
		IsPersistable: true,
	}, func(context.Context) (dagql.AnyResult, error) {
		return nil, errors.New("unexpected initializer for first persisted cache volume")
	})
	require.NoError(t, err)

	loadedB, err := cacheB.GetOrInitCall(ctxB, "session-b", srvB, &dagql.CallRequest{
		ResultCall:    callB,
		IsPersistable: true,
	}, func(context.Context) (dagql.AnyResult, error) {
		return nil, errors.New("unexpected initializer for second persisted cache volume")
	})
	require.NoError(t, err)
	require.Empty(t, managerB.getMutableBySnapshotIDCalls)

	loadedCacheA, ok := loadedA.(dagql.ObjectResult[*CacheVolume])
	require.True(t, ok)
	loadedCacheB, ok := loadedB.(dagql.ObjectResult[*CacheVolume])
	require.True(t, ok)

	require.NoError(t, loadedCacheA.Self().InitializeSnapshot(ctxB))
	require.NoError(t, loadedCacheB.Self().InitializeSnapshot(ctxB))
	require.Equal(t, []string{snapshotID}, managerB.getMutableBySnapshotIDCalls)
	require.True(t, managerB.mutableSnapshotOpen[snapshotID])

	require.NoError(t, loadedCacheA.Self().OnRelease(ctxB))
	require.True(t, managerB.mutableSnapshotOpen[snapshotID])
	require.NoError(t, loadedCacheB.Self().OnRelease(ctxB))
	require.False(t, managerB.mutableSnapshotOpen[snapshotID])
}

var _ bkcache.ImmutableRef = (*cacheVolumeTestImmutableRef)(nil)
var _ bkcache.MutableRef = (*cacheVolumeTestMutableRef)(nil)
var _ bkcache.SnapshotManager = (*cacheVolumeTestSnapshotManager)(nil)
var _ dagql.PersistedObject = (*CacheVolume)(nil)
