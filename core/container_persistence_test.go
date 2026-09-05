package core

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

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
	owners     map[string]string
	beforeOpen func(context.Context, string) error
}

func newContainerPersistenceTestSnapshots() *containerPersistenceTestSnapshots {
	return &containerPersistenceTestSnapshots{
		cacheVolumeTestSnapshotManager: &cacheVolumeTestSnapshotManager{},
		opens:                          map[string]int{}, releases: map[string]int{}, owners: map[string]string{},
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
	m.owners[leaseID] = snapshotID
	return m.cacheVolumeTestSnapshotManager.AttachLease(ctx, leaseID, snapshotID)
}

func (m *containerPersistenceTestSnapshots) RemoveLease(ctx context.Context, leaseID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.owners, leaseID)
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

func containerPersistenceSpanAttrs(attrs []attribute.KeyValue) map[string]attribute.Value {
	out := map[string]attribute.Value{}
	for _, attr := range attrs {
		out[string(attr.Key)] = attr.Value
	}
	return out
}

func TestContainerRestoreReportingAndBookkeepingRetry(t *testing.T) {
	for _, pendingSibling := range []bool{false, true} {
		t.Run(map[bool]string{false: "stored only", true: "pending sibling"}[pendingSibling], func(t *testing.T) {
			manager := newContainerPersistenceTestSnapshots()
			ctx, cache, srv := containerPersistenceTestCache(t, "", manager, "reporting")
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.WithoutCancel(t.Context()))) })
			tracer := provider.Tracer("stored-container-reporting")
			producerCtx, producer := tracer.Start(ctx, "produce container")
			stored := map[dagql.PartKey]containerStoredPart{
				ContainerPartFS: {Kind: containerPartDirectory, Role: "fs", SnapshotID: "saved", Path: "/"},
			}
			var recipe LazyContainerParts
			if pendingSibling {
				op := &containerPartsTestBaseOp{LazyState: NewLazyState()}
				op.seedConsumedGroups(ContainerLazyGroupMetadata, containerDelegationGroup(ContainerPartFS))
				recipe = op
			} else {
				stored[ContainerPartExecMeta] = containerStoredPart{Kind: containerPartAbsent}
			}
			ctr := containerPersistenceTestRestore(stored, recipe)
			res := attachContainerPartsTestResult(t, producerCtx, cache, srv, "reporting", "restored-reporting", ctr)
			producer.End()
			installCtx, install := tracer.Start(ctx, "return existing container")
			res = attachContainerPartsTestResult(t, installCtx, cache, srv, "reporting", "restored-reporting", ctr)
			install.End()
			require.True(t, dagql.HasPendingLazyEvaluation(res))
			require.Equal(t, pendingSibling, dagql.HasPendingLazyComputation(res))
			pending := &telemetryTestSpan{}
			recordPending(res, pending)
			require.Equal(t, pendingSibling, containerPersistenceSpanAttrs(pending.attrs)[telemetry.PendingAttr].AsBool())
			status := &telemetryTestSpan{}
			recordStatus(ctx, res, status, true, nil)
			require.Equal(t, !pendingSibling, containerPersistenceSpanAttrs(status.attrs)[telemetry.CachedAttr].AsBool())

			cleanupErr := errors.New("operation lease release failed")
			var releases atomic.Int32
			ctx = dagql.ContextWithOperationLeaseProvider(ctx, dagql.OperationLeaseProviderFunc(func(ctx context.Context) (context.Context, func(context.Context) error, error) {
				return ctx, func(context.Context) error {
					if releases.Add(1) == 1 {
						return cleanupErr
					}
					return nil
				}, nil
			}))
			consumerCtx, consumer := tracer.Start(ctx, "read saved filesystem")
			require.ErrorIs(t, cache.EvaluateParts(consumerCtx, res, ContainerPartFS), cleanupErr)
			require.Equal(t, 1, manager.openCount("saved"))
			require.Equal(t, pendingSibling, ctr.lazyOpForRouting() != nil)
			require.Equal(t, pendingSibling, dagql.HasPendingLazyComputation(res))
			require.True(t, dagql.HasPendingLazyEvaluation(res), "bookkeeping remains retryable after body consumption")
			require.NoError(t, cache.EvaluateParts(consumerCtx, res, ContainerPartFS))
			consumer.End()
			require.Equal(t, 1, manager.openCount("saved"), "bookkeeping retry must not reopen")
			var opens []sdktrace.ReadOnlySpan
			for _, span := range recorder.Ended() {
				if span.Name() == "open stored part (fs)" {
					opens = append(opens, span)
				}
			}
			require.Len(t, opens, 2, "purpose survives clearing the recipe pointer")
			require.Equal(t, codes.Error, opens[0].Status().Code)
			require.False(t, containerPersistenceSpanAttrs(opens[0].Attributes())[telemetry.CachedAttr].AsBool())
			attrs := containerPersistenceSpanAttrs(opens[1].Attributes())
			require.True(t, attrs[telemetry.CachedAttr].AsBool())
			require.Equal(t, pendingSibling, attrs[telemetryattrs.DagPartialAttr].AsBool())
			for _, span := range opens {
				foundCause := false
				for _, link := range span.Links() {
					if link.SpanContext.SpanID() == install.SpanContext().SpanID() && containerPersistenceSpanAttrs(link.Attributes)[telemetry.LinkPurposeAttr].AsString() == telemetry.LinkPurposeCause {
						foundCause = true
					}
				}
				require.True(t, foundCause, "stored attempts retain their install call's failure attribution")
			}
			if pendingSibling {
				require.NoError(t, cache.EvaluateParts(ctx, res, ContainerPartExecMeta))
			}
			require.False(t, dagql.HasPendingLazyComputation(res))
			require.False(t, dagql.HasPendingLazyEvaluation(res))
		})
	}
}

func TestContainerRestoreSnapshotOwnershipAndRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager := newContainerPersistenceTestSnapshots()
		ctx, cache, srv := containerPersistenceTestCache(t, "", manager, "owner-a")
		newOwner := func() *Container {
			return containerPersistenceTestRestore(map[dagql.PartKey]containerStoredPart{
				ContainerPartFS:       {Kind: containerPartDirectory, Role: "fs", SnapshotID: "shared", Path: "/"},
				ContainerPartExecMeta: {Kind: containerPartSnapshot, Role: "meta", SnapshotID: "closed-meta"},
			}, nil)
		}
		first, second := newOwner(), newOwner()
		firstRes := attachContainerPartsTestResult(t, ctx, cache, srv, "owner-a", "first-owner", first)
		secondCtx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "owner-b", SessionID: "owner-b"})
		secondRes := attachContainerPartsTestResult(t, secondCtx, cache, srv, "owner-b", "second-owner", second)
		counts := func(snapshotID string) (int, int) {
			manager.mu.Lock()
			defer manager.mu.Unlock()
			owners := 0
			for _, id := range manager.owners {
				if id == snapshotID {
					owners++
				}
			}
			return owners, manager.releases[snapshotID]
		}
		owners, released := counts("shared")
		require.Equal(t, 2, owners, "closed descriptors own snapshots independently")
		require.Zero(t, released)
		require.Zero(t, manager.openCount("shared"))
		require.NoError(t, cache.EvaluateParts(ctx, firstRes, ContainerPartFS))
		require.NoError(t, cache.ReleaseSession(ctx, "owner-a"))
		synctest.Wait()
		owners, released = counts("shared")
		require.Equal(t, 1, owners)
		require.Equal(t, 1, released, "only the opened handle is released")
		owners, released = counts("closed-meta")
		require.Equal(t, 1, owners)
		require.Zero(t, released)
		require.NoError(t, cache.EvaluateParts(secondCtx, secondRes, ContainerPartFS))
		require.Equal(t, 2, manager.openCount("shared"))
		require.NoError(t, cache.ReleaseSession(secondCtx, "owner-b"))
		synctest.Wait()
		owners, released = counts("shared")
		require.Zero(t, owners)
		require.Equal(t, 2, released)
		owners, released = counts("closed-meta")
		require.Zero(t, owners)
		require.Zero(t, released, "closed descriptors have no accessor handle to release")
		require.Zero(t, manager.openCount("closed-meta"))
	})
}

func TestContainerPersistedDetachedFileAndDirectoryPaths(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	ctx, cache, srv := containerPersistenceTestCache(t, dbPath, newContainerPersistenceTestSnapshots(), "paths")
	platform := Platform{OS: "linux", Architecture: "arm64"}
	original := NewContainer(platform)
	rootInput := containerPersistenceTestDirectory("root", "")
	rootInput.Dir.setValue("")
	original.FS.setValue(rootInput)
	file := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]), Platform: platform}
	file.File.setValue("/nested/source.txt")
	file.Snapshot.setValue(&cacheVolumeTestImmutableRef{id: "file", snapshotID: "file"})
	fileSource := new(LazyAccessor[*File, *Container])
	fileSource.setValue(file)
	dirSource := new(LazyAccessor[*Directory, *Container])
	dirSource.setValue(containerPersistenceTestDirectory("dir", "/nested/tree"))
	original.Mounts = ContainerMounts{
		{Target: "/tmp", TmpfsSource: &TmpfsMountSource{}},
		{Target: "/file", FileSource: fileSource},
		{Target: "/dir", DirectorySource: dirSource},
	}
	encoded, err := original.EncodePersistedObject(ctx, cache)
	require.NoError(t, err)
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "container", Type: dagql.NewResultCallType(original.Type())}
	res, err := cache.GetOrInitCall(ctx, "paths", srv, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewObjectResultForCall(original, srv, frame)
	})
	require.NoError(t, err)
	id, err := cache.PersistedResultID(res)
	require.NoError(t, err)
	require.NoError(t, cache.ReleaseSession(ctx, "paths"))
	require.NoError(t, cache.Close(ctx))
	manager := newContainerPersistenceTestSnapshots()
	ctx, cache, srv = containerPersistenceTestCache(t, dbPath, manager, "restored-paths")
	res, err = cache.LoadResultByResultID(ctx, "restored-paths", srv, id)
	require.NoError(t, err)
	restored, ok := dagql.UnwrapAs[*Container](res)
	require.True(t, ok)
	require.ElementsMatch(t, []string{"root", "file", "dir"}, restored.CacheUsageIdentities())
	manager.snapshotSizes = map[string]int64{"root": 10, "file": 20, "dir": 30}
	for id, want := range manager.snapshotSizes {
		size, found, err := restored.CacheUsageSize(ctx, manager, id)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, want, size)
	}
	require.Zero(t, manager.openCount("root"))
	require.Zero(t, manager.openCount("file"))
	require.Zero(t, manager.openCount("dir"))
	require.NoError(t, cache.EvaluateParts(ctx, res, ContainerPartMount("/file")))
	openedFile, ok := restored.Mounts[1].FileSource.Peek()
	require.True(t, ok)
	path, ok := openedFile.File.Peek()
	require.True(t, ok)
	require.Equal(t, "/nested/source.txt", path)
	require.Equal(t, platform, openedFile.Platform)
	require.Nil(t, openedFile.Lazy)
	require.Zero(t, manager.openCount("dir"))
	require.Zero(t, manager.openCount("root"))
	require.NoError(t, cache.Evaluate(ctx, res))
	openedDir, ok := restored.Mounts[2].DirectorySource.Peek()
	require.True(t, ok)
	path, ok = openedDir.Dir.Peek()
	require.True(t, ok)
	require.Equal(t, "/nested/tree", path)
	root, ok := restored.FS.Peek()
	require.True(t, ok)
	path, ok = root.Dir.Peek()
	require.True(t, ok, "a recorded empty path is an available value")
	require.Empty(t, path)
	require.Empty(t, restored.storedParts[ContainerPartFS].Path)
	reencoded, err := restored.EncodePersistedObject(ctx, cache)
	require.NoError(t, err)
	require.JSONEq(t, string(encoded.JSON), string(reencoded.JSON))
	require.ElementsMatch(t, encoded.SnapshotLinks, reencoded.SnapshotLinks)
}

func TestContainerRestoreAttemptLifetime(t *testing.T) {
	for _, releaseSession := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel leader with healthy waiter", true: "release during open"}[releaseSession], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				manager := newContainerPersistenceTestSnapshots()
				started, allow := make(chan struct{}), make(chan struct{})
				manager.beforeOpen = func(ctx context.Context, _ string) error {
					close(started)
					select {
					case <-allow:
						return nil
					case <-ctx.Done():
						return context.Cause(ctx)
					}
				}
				ctx, cache, srv := containerPersistenceTestCache(t, "", manager, "lifetime")
				recorder := tracetest.NewSpanRecorder()
				provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
				t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.WithoutCancel(t.Context()))) })
				tracer := provider.Tracer("stored-container-lifetime")
				ctx, producer := tracer.Start(ctx, "produce stored container")
				ctr := containerPersistenceTestRestore(map[dagql.PartKey]containerStoredPart{
					ContainerPartFS:       {Kind: containerPartDirectory, Role: "fs", SnapshotID: "saved", Path: "/"},
					ContainerPartExecMeta: {Kind: containerPartAbsent},
				}, nil)
				res := attachContainerPartsTestResult(t, ctx, cache, srv, "lifetime", "stored-lifetime", ctr)
				producer.End()
				leaderCtx, leader := tracer.Start(ctx, "lead saved filesystem demand")
				defer leader.End()
				leaderCtx, cancel := context.WithCancelCause(leaderCtx)
				defer cancel(nil)
				leaderDone := make(chan error, 1)
				go func() { leaderDone <- cache.EvaluateParts(leaderCtx, res, ContainerPartFS) }()
				<-started
				var waiterDone chan error
				waiterCtx, waiter := tracer.Start(ctx, "consume saved filesystem")
				if releaseSession {
					require.NoError(t, cache.ReleaseSession(ctx, "lifetime"))
					synctest.Wait()
					require.Greater(t, cache.Size(), 0, "attempt retains the owner while opening")
				} else {
					waiterDone = make(chan error, 1)
					go func() { waiterDone <- cache.EvaluateParts(waiterCtx, res, ContainerPartFS) }()
					synctest.Wait()
					cause := errors.New("leader stopped waiting")
					cancel(cause)
					require.ErrorIs(t, <-leaderDone, cause)
				}
				close(allow)
				if releaseSession {
					require.ErrorIs(t, <-leaderDone, dagql.ErrCacheSessionReleased)
				} else {
					require.NoError(t, <-waiterDone)
				}
				synctest.Wait()
				waiter.End()
				require.Equal(t, 1, manager.openCount("saved"))
				if releaseSession {
					require.Zero(t, cache.Size())
					manager.mu.Lock()
					require.Equal(t, 1, manager.releases["saved"])
					require.Empty(t, manager.owners)
					manager.mu.Unlock()
				} else {
					var open, waiting sdktrace.ReadOnlySpan
					for _, span := range recorder.Ended() {
						if span.Name() == "open stored part (fs)" {
							open = span
						}
						if span.Name() == "consume saved filesystem" {
							waiting = span
						}
					}
					require.NotNil(t, open)
					require.NotNil(t, waiting)
					found := false
					for _, link := range waiting.Links() {
						attrs := containerPersistenceSpanAttrs(link.Attributes)
						if attrs[telemetry.LinkPurposeAttr].AsString() == telemetryattrs.LinkPurposeWait && link.SpanContext.SpanID() == open.SpanContext().SpanID() {
							found = true
						}
					}
					require.True(t, found, "healthy waiter targets the shared stored-open attempt")
				}
			})
		})
	}
}
