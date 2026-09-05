package core

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/stretchr/testify/require"
)

// Every open returns a separate handle. Counts are safe under concurrent group
// calls; the hooks provide deterministic rendezvous for exclusion and retry.
type containerPersistenceTestSnapshots struct {
	*cacheVolumeTestSnapshotManager
	mu         sync.Mutex
	opens      map[string]int
	releases   map[string]int
	beforeOpen func(context.Context, string) error
}

func newContainerPersistenceTestSnapshots() *containerPersistenceTestSnapshots {
	return &containerPersistenceTestSnapshots{
		cacheVolumeTestSnapshotManager: &cacheVolumeTestSnapshotManager{},
		opens:                          map[string]int{}, releases: map[string]int{},
	}
}

func (m *containerPersistenceTestSnapshots) GetBySnapshotID(ctx context.Context, id string, _ ...bkcache.RefOption) (bkcache.ImmutableRef, error) {
	m.mu.Lock()
	m.opens[id]++
	m.mu.Unlock()
	if m.beforeOpen != nil {
		if err := m.beforeOpen(ctx, id); err != nil {
			return nil, err
		}
	}
	return &cacheVolumeTestImmutableRef{
		id: id, snapshotID: id,
		release: func(context.Context) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.releases[id]++
			return nil
		},
	}, nil
}

func (m *containerPersistenceTestSnapshots) openCount(id string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.opens[id]
}

func (m *containerPersistenceTestSnapshots) AttachLease(ctx context.Context, leaseID, snapshotID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cacheVolumeTestSnapshotManager.AttachLease(ctx, leaseID, snapshotID)
}

func (m *containerPersistenceTestSnapshots) RemoveLease(ctx context.Context, leaseID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cacheVolumeTestSnapshotManager.RemoveLease(ctx, leaseID)
}

func (*containerPersistenceTestSnapshots) SnapshotRecordMetadata(context.Context, string) (bkcache.SnapshotRecordMetadata, bool, error) {
	return bkcache.SnapshotRecordMetadata{}, false, nil
}

func containerPersistenceTestCache(t *testing.T, dbPath string, manager *containerPersistenceTestSnapshots, session string) (context.Context, *dagql.Cache, *dagql.Server) {
	t.Helper()
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{ClientID: session, SessionID: session})
	cache, err := dagql.NewCache(ctx, dbPath, manager, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	query := &Query{Server: &cacheVolumeTestQueryServer{mockServer: &mockServer{}, cacheManager: manager}}
	ctx = ContextWithQuery(dagql.ContextWithCache(ctx, cache), query)
	srv := newCoreDagqlServerForTest(t, query)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Container]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Directory]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*File]{}))
	return ctx, cache, srv
}

func containerPersistenceTestDirectory(id, path string) *Directory {
	dir := &Directory{
		Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
		Platform: Platform{OS: "linux", Architecture: "amd64"},
	}
	if path != "" {
		dir.Dir.setValue(path)
	}
	dir.Snapshot.setValue(&cacheVolumeTestImmutableRef{id: id, snapshotID: id})
	return dir
}

func TestContainerPersistedUnsupportedTargetPreservesConsumedExecMeta(t *testing.T) {
	for _, targetKind := range []string{"tmpfs", "cache"} {
		t.Run(targetKind, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "cache.db")
			managerA := newContainerPersistenceTestSnapshots()
			ctxA, cacheA, srvA := containerPersistenceTestCache(t, dbPath, managerA, "first")
			parent := NewContainer(Platform{OS: "linux", Architecture: "amd64"})
			mount := ContainerMount{Target: "/unsupported", TmpfsSource: &TmpfsMountSource{}}
			if targetKind == "cache" {
				srvA.InstallObject(dagql.NewClass(srvA, dagql.ClassOpts[*CacheVolume]{}))
				volume := attachContainerPartsTestObject(t, ctxA, cacheA, srvA, "first", "unsupported-cache", NewCache("unsupported", "ns", dagql.Null[dagql.ObjectResult[*Directory]](), CacheSharingModeShared, ""))
				mount.TmpfsSource = nil
				mount.CacheSource = &CacheMountSource{Volume: volume}
			}
			parent.Mounts = ContainerMounts{mount}
			parent.MetaSnapshot.setValue(&cacheVolumeTestImmutableRef{id: "metadata", snapshotID: "metadata"})
			parentRes := attachContainerPartsTestResult(t, ctxA, cacheA, srvA, "first", "unsupported-parent", parent)
			source := attachContainerPartsTestObject(t, ctxA, cacheA, srvA, "first", "writer-input", containerPersistenceTestDirectory("input", "/"))
			op := &ContainerWithDirectoryLazy{LazyState: NewLazyState(), Parent: parentRes, Source: source, Path: "/unsupported/tree"}
			child := NewContainer(parent.Platform)
			child.Lazy = op
			frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "withDirectory", Type: dagql.NewResultCallType(child.Type())}
			res, err := cacheA.GetOrInitCall(ctxA, "first", srvA, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
				return dagql.NewObjectResultForCall(child, srvA, frame)
			})
			require.NoError(t, err)
			require.Error(t, cacheA.EvaluateParts(ctxA, res, ContainerPartMetadata))
			require.True(t, op.GroupConsumed(ContainerLazyGroupMetadata))
			require.Error(t, cacheA.EvaluateParts(ctxA, res, ContainerPartExecMeta))
			require.True(t, op.GroupConsumed(containerDelegationGroup(ContainerPartExecMeta)))
			require.False(t, op.GroupConsumed(ContainerLazyGroupWrite))
			fsErr := cacheA.EvaluateParts(ctxA, res, ContainerPartFS)
			wholeErr := cacheA.Evaluate(ctxA, res)
			require.Error(t, fsErr)
			require.Error(t, wholeErr)
			encoded, err := child.EncodePersistedObject(ctxA, cacheA)
			require.NoError(t, err)
			var payload persistedContainerPayload
			require.NoError(t, json.Unmarshal(encoded.JSON, &payload))
			require.True(t, payload.Metadata.Consumed)
			require.Equal(t, containerPartPending, payload.Parts[ContainerPartFS].Kind)
			require.Equal(t, containerPartSnapshot, payload.Parts[ContainerPartExecMeta].Kind)
			require.NotEmpty(t, payload.LazyJSON)
			id, err := cacheA.PersistedResultID(res)
			require.NoError(t, err)
			require.NoError(t, cacheA.ReleaseSession(ctxA, "first"))
			require.NoError(t, cacheA.Close(ctxA))

			managerB := newContainerPersistenceTestSnapshots()
			ctxB, cacheB, srvB := containerPersistenceTestCache(t, dbPath, managerB, "second")
			srvB.InstallObject(dagql.NewClass(srvB, dagql.ClassOpts[*CacheVolume]{}))
			loaded, err := cacheB.LoadResultByResultID(ctxB, "second", srvB, id)
			require.NoError(t, err)
			restored, ok := dagql.UnwrapAs[*Container](loaded)
			require.True(t, ok)
			require.Equal(t, "metadata", restored.storedParts[ContainerPartExecMeta].SnapshotID)
			require.Zero(t, managerB.openCount("metadata"))
			require.Equal(t, 1, managerB.openCount("input"), "standalone input Directory still opens eagerly")
			require.EqualError(t, cacheB.EvaluateParts(ctxB, loaded, ContainerPartFS), fsErr.Error())
			require.EqualError(t, cacheB.Evaluate(ctxB, loaded), wholeErr.Error())
			require.Error(t, cacheB.EvaluateParts(ctxB, loaded, ContainerPartExecMeta), "ordinary post-body scan still reports the target error")
			require.Equal(t, 1, managerB.openCount("metadata"))
			_, err = restored.EncodePersistedObject(ctxB, cacheB)
			require.NoError(t, err)
			require.NoError(t, cacheB.ReleaseSession(ctxB, "second"))
			require.NoError(t, cacheB.Close(ctxB))
		})
	}
}

func containerPersistenceTestRestore(stored map[dagql.PartKey]containerStoredPart, recipe LazyContainerParts) *Container {
	state := NewLazyState()
	statePtr := &state
	if recipe != nil {
		statePtr = recipe.ContainerLazyState()
	}
	statePtr.seedConsumedGroups(ContainerLazyGroupMetadata)
	for part, value := range stored {
		if value.Kind == containerPartAbsent {
			statePtr.seedConsumedGroups(containerStoredOpenGroup(part))
		}
	}
	return &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
		storedParts:  stored,
		Lazy:         &ContainerRestoreLazy{LazyState: statePtr, recipe: recipe},
	}
}

func TestContainerRestoreIndependentParts(t *testing.T) {
	manager := newContainerPersistenceTestSnapshots()
	server := &cacheVolumeTestQueryServer{mockServer: &mockServer{}, cacheManager: manager}
	ctx, cache, srv, session := newContainerPartsTestCtxWithQueryServer(t, server)
	ctx = ContextWithQuery(ctx, &Query{Server: server})
	ctr := containerPersistenceTestRestore(map[dagql.PartKey]containerStoredPart{
		ContainerPartFS:       {Kind: containerPartDirectory, Role: "fs", SnapshotID: "rootfs", Path: "/"},
		ContainerPartExecMeta: {Kind: containerPartSnapshot, Role: "meta", SnapshotID: "exec-meta"},
	}, nil)
	res := attachContainerPartsTestResult(t, ctx, cache, srv, session, "stored-independent", ctr)
	require.NoError(t, cache.EvaluateParts(ctx, res, ContainerPartMetadata))
	require.NoError(t, ctr.evaluatePartsDirect(ctx, ContainerPartMetadata))
	require.Zero(t, manager.openCount("rootfs"))
	require.Zero(t, manager.openCount("exec-meta"))
	require.NoError(t, cache.EvaluateParts(ctx, res, ContainerPartFS))
	require.Equal(t, 1, manager.openCount("rootfs"))
	require.Zero(t, manager.openCount("exec-meta"))
	_, set := ctr.MetaSnapshot.Peek()
	require.False(t, set)
	require.NotNil(t, ctr.lazyOpForRouting())
	require.NoError(t, ctr.evaluatePartsDirect(ctx, ContainerPartFS))
	require.NoError(t, cache.EvaluateParts(ctx, res, ContainerPartExecMeta))
	require.Equal(t, 1, manager.openCount("rootfs"))
	require.Equal(t, 1, manager.openCount("exec-meta"))
	require.Nil(t, ctr.lazyOpForRouting())
	require.Len(t, ctr.storedParts, 2)
}

func TestContainerRestoreDirectMetadataSweep(t *testing.T) {
	manager := newContainerPersistenceTestSnapshots()
	server := &cacheVolumeTestQueryServer{mockServer: &mockServer{}, cacheManager: manager}
	ctx, cache, srv, session := newContainerPartsTestCtxWithQueryServer(t, server)
	ctx = ContextWithQuery(ctx, &Query{Server: server})
	parent := containerPersistenceTestRestore(map[dagql.PartKey]containerStoredPart{
		ContainerPartFS:       {Kind: containerPartDirectory, Role: "fs", SnapshotID: "parent-fs", Path: "/"},
		ContainerPartExecMeta: {Kind: containerPartAbsent},
	}, nil)
	parentRes := attachContainerPartsTestResult(t, ctx, cache, srv, session, "stored-parent", parent)
	child := &Container{FS: new(LazyAccessor[*Directory, *Container]), MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container])}
	op := &ContainerWithLabelLazy{LazyState: NewLazyState(), Parent: parentRes}
	op.seedConsumedGroups(ContainerLazyGroupMetadata)
	child.Lazy = op
	childRes := attachContainerPartsTestResult(t, ctx, cache, srv, session, "stored-parent-child", child)
	require.NoError(t, cache.EvaluateParts(ctx, childRes, ContainerPartMetadata))
	require.NoError(t, child.evaluatePartsDirect(ctx, ContainerPartMetadata))
	require.Zero(t, manager.openCount("parent-fs"))
	require.False(t, op.GroupConsumed(containerDelegationGroup(ContainerPartFS)))
	require.NoError(t, cache.EvaluateParts(ctx, parentRes, ContainerPartFS))
	require.NoError(t, cache.EvaluateParts(ctx, childRes, ContainerPartMetadata))
	require.False(t, op.GroupConsumed(containerDelegationGroup(ContainerPartFS)))
	require.Equal(t, 1, manager.openCount("parent-fs"))
	require.NoError(t, child.evaluatePartsDirect(ctx, ContainerPartMetadata))
	require.True(t, op.GroupConsumed(containerDelegationGroup(ContainerPartFS)))
	require.True(t, child.containerPartComputed(ctx, op, ContainerPartFS))
	require.Equal(t, 2, manager.openCount("parent-fs")) // the available parent copy owns another handle
}

func TestContainerRestoreConcurrentParts(t *testing.T) {
	manager := newContainerPersistenceTestSnapshots()
	started, allow := make(chan struct{}), make(chan struct{})
	var once sync.Once
	manager.beforeOpen = func(ctx context.Context, id string) error {
		if id != "rootfs" {
			return nil
		}
		once.Do(func() { close(started) })
		select {
		case <-allow:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	server := &cacheVolumeTestQueryServer{mockServer: &mockServer{}, cacheManager: manager}
	ctx, cache, srv, session := newContainerPartsTestCtxWithQueryServer(t, server)
	ctx = ContextWithQuery(ctx, &Query{Server: server})
	ctr := containerPersistenceTestRestore(map[dagql.PartKey]containerStoredPart{
		ContainerPartFS:       {Kind: containerPartDirectory, Role: "fs", SnapshotID: "rootfs", Path: "/"},
		ContainerPartExecMeta: {Kind: containerPartSnapshot, Role: "meta", SnapshotID: "exec-meta"},
	}, nil)
	res := attachContainerPartsTestResult(t, ctx, cache, srv, session, "concurrent-stored", ctr)
	done := make(chan error, 2)
	go func() { done <- cache.EvaluateParts(ctx, res, ContainerPartFS) }()
	<-started
	go func() { done <- ctr.evaluatePartsDirect(ctx, ContainerPartFS) }()
	// Another saved group completes while the first group's open is blocked.
	require.NoError(t, cache.EvaluateParts(ctx, res, ContainerPartExecMeta))
	require.Equal(t, 1, manager.openCount("exec-meta"))
	close(allow)
	require.NoError(t, <-done)
	require.NoError(t, <-done)
	require.Equal(t, 1, manager.openCount("rootfs"))
	require.Nil(t, ctr.lazyOpForRouting())
}

func TestContainerRestoreOpenFailureKeepsRecipeConsumed(t *testing.T) {
	manager := newContainerPersistenceTestSnapshots()
	wantErr := errors.New("stored snapshot temporarily unavailable")
	var attempts atomic.Int32
	manager.beforeOpen = func(context.Context, string) error {
		if attempts.Add(1) == 1 {
			return wantErr
		}
		return nil
	}
	server := &cacheVolumeTestQueryServer{mockServer: &mockServer{}, cacheManager: manager}
	ctx, cache, srv, session := newContainerPartsTestCtxWithQueryServer(t, server)
	ctx = ContextWithQuery(ctx, &Query{Server: server})
	recipe := &containerPartsTestBaseOp{LazyState: NewLazyState()}
	recipe.seedConsumedGroups(ContainerLazyGroupMetadata, containerDelegationGroup(ContainerPartFS))
	ctr := containerPersistenceTestRestore(map[dagql.PartKey]containerStoredPart{
		ContainerPartFS: {Kind: containerPartDirectory, Role: "fs", SnapshotID: "rootfs", Path: "/"},
	}, recipe)
	res := attachContainerPartsTestResult(t, ctx, cache, srv, session, "retry-stored", ctr)
	require.ErrorIs(t, cache.EvaluateParts(ctx, res, ContainerPartFS), wantErr)
	require.Zero(t, recipe.fsRuns.Load())
	require.False(t, ctr.containerPartComputed(ctx, ctr.lazyOpForRouting(), ContainerPartExecMeta))
	require.NoError(t, cache.EvaluateParts(ctx, res, ContainerPartFS))
	require.NoError(t, cache.EvaluateParts(ctx, res, ContainerPartExecMeta))
	require.Zero(t, recipe.fsRuns.Load())
	require.EqualValues(t, 1, recipe.execMetaRuns.Load())
	require.Equal(t, 2, manager.openCount("rootfs"))
	require.Nil(t, ctr.lazyOpForRouting())
	require.Contains(t, ctr.storedParts, ContainerPartFS)
}

func TestContainerPersistedPartsIgnorePendingAccessorSeeds(t *testing.T) {
	op := &containerPartsTestBaseOp{LazyState: NewLazyState()}
	ctr := &Container{Lazy: op, MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container])}
	ctr.MetaSnapshot.setValue(&cacheVolumeTestImmutableRef{id: "input", snapshotID: "input"})
	pending, parts, links, err := ctr.encodeContainerParts(t.Context(), nil, op)
	require.NoError(t, err)
	require.True(t, pending)
	require.Empty(t, parts)
	require.Empty(t, links)
	require.NoError(t, op.EvaluateContainerGroup(t.Context(), ctr, ContainerLazyGroupMetadata))
	pending, parts, links, err = ctr.encodeContainerParts(t.Context(), nil, op)
	require.NoError(t, err)
	require.True(t, pending)
	require.Equal(t, containerPartPending, parts[ContainerPartExecMeta].Kind)
	require.Empty(t, links)
}

func TestContainerPersistedJointOutputsSeedOriginalAndOpenIndependently(t *testing.T) {
	manager := newContainerPersistenceTestSnapshots()
	server := &cacheVolumeTestQueryServer{mockServer: &mockServer{}, cacheManager: manager}
	ctx, cache, srv, session := newContainerPartsTestCtxWithQueryServer(t, server)
	ctx = ContextWithQuery(ctx, &Query{Server: server})
	parent := attachContainerPartsTestResult(t, ctx, cache, srv, session, "joint-parent", NewContainer(Platform{}))
	recipe := &ContainerExecLazy{State: &ContainerExecState{LazyState: NewLazyState(), Parent: parent}}
	ctr := &Container{FS: new(LazyAccessor[*Directory, *Container]), MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container])}
	parts := map[dagql.PartKey]persistedContainerPart{
		ContainerPartFS:       {Kind: containerPartDirectory, Role: "fs", Path: "/"},
		ContainerPartExecMeta: {Kind: containerPartSnapshot, Role: "meta"},
	}
	links := []dagql.PersistedSnapshotRefLink{{Role: "fs", RefKey: "joint-fs"}, {Role: "meta", RefKey: "joint-meta"}}
	require.NoError(t, ctr.installContainerParts(ctx, srv, true, parts, links, recipe))
	require.True(t, recipe.State.GroupConsumed(ContainerLazyGroupExecOutputs))
	require.Zero(t, manager.openCount("joint-fs"))
	require.Zero(t, manager.openCount("joint-meta"))
	res := attachContainerPartsTestResult(t, ctx, cache, srv, session, "joint-restored", ctr)
	require.NoError(t, cache.EvaluateParts(ctx, res, ContainerPartExecMeta))
	require.Equal(t, 1, manager.openCount("joint-meta"))
	require.Zero(t, manager.openCount("joint-fs"))
	_, set := ctr.FS.Peek()
	require.False(t, set)
	pending, encoded, encodedLinks, err := ctr.encodeContainerParts(ctx, cache, ctr.lazyOpForRouting())
	require.NoError(t, err)
	require.False(t, pending)
	require.Equal(t, parts, encoded)
	require.ElementsMatch(t, links, encodedLinks)
	// Whole demand must make every saved member ready before returning.
	require.NoError(t, cache.Evaluate(ctx, res))
	require.Equal(t, 1, manager.openCount("joint-fs"))
	require.Equal(t, 1, manager.openCount("joint-meta"))
	require.Nil(t, ctr.lazyOpForRouting())
}

func TestContainerPersistedPartsRejectMissingCompletedValue(t *testing.T) {
	ctr := &Container{FS: new(LazyAccessor[*Directory, *Container])}
	ctr.FS.setValue(&Directory{Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory])})
	_, _, _, err := ctr.encodeContainerParts(t.Context(), nil, nil)
	require.ErrorContains(t, err, "has no snapshot")

	parts := map[dagql.PartKey]persistedContainerPart{
		ContainerPartFS:       {Kind: containerPartDirectory, Role: "fs"},
		ContainerPartExecMeta: {Kind: containerPartAbsent},
	}
	err = ctr.installContainerParts(t.Context(), nil, false, parts, nil, &containerPartsTestBaseOp{LazyState: NewLazyState()})
	require.ErrorContains(t, err, "pending metadata")
	err = ctr.installContainerParts(t.Context(), nil, true, parts, nil, nil)
	require.ErrorContains(t, err, "no snapshot link")
	parts[ContainerPartFS] = persistedContainerPart{Kind: containerPartPending}
	err = ctr.installContainerParts(t.Context(), nil, true, parts, nil, nil)
	require.ErrorContains(t, err, "has no recipe")
}
