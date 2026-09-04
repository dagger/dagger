package core

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

type containerPartsTestSuccessfulExecOp struct {
	*ContainerExecLazy
}

func (op *containerPartsTestSuccessfulExecOp) EvaluateContainerGroup(ctx context.Context, ctr *Container, group dagql.LazyGroupKey) error {
	if group != ContainerLazyGroupExecOutputs {
		return op.ContainerExecLazy.EvaluateContainerGroup(ctx, ctr, group)
	}
	return op.State.LazyState.EvaluateGroup(ctx, "test.containerExec", group, func(ctx context.Context) error {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return err
		}
		parts := []dagql.PartKey{ContainerPartFS}
		for _, mnt := range ctr.Mounts {
			if mnt.DirectorySource != nil || mnt.FileSource != nil {
				parts = append(parts, ContainerPartMount(mnt.Target))
			}
		}
		return cache.EvaluateParts(ctx, op.State.Parent, parts...)
	})
}

func TestContainerExecSuccessConsumesFinalReadOnlyMount(t *testing.T) {
	metaParent := &cacheVolumeTestImmutableRef{id: "exec-ro-parent", snapshotID: "exec-ro"}
	metaChild := &cacheVolumeTestImmutableRef{id: "exec-ro-child", snapshotID: "exec-ro"}
	snapshotManager := &cacheVolumeTestSnapshotManager{
		immutableBySnapshotID: map[string]bkcache.ImmutableRef{"exec-ro": metaChild},
	}
	queryServer := &cacheVolumeTestQueryServer{
		mockServer:   &mockServer{},
		cacheManager: snapshotManager,
	}
	ctx, cache, srv, sessionID := newContainerPartsTestCtxWithQueryServer(t, queryServer)
	ctx = ContextWithQuery(ctx, &Query{Server: queryServer})

	baseOp := &containerPartsTestBaseOp{
		LazyState:      NewLazyState(),
		mountTargets:   []string{"/ro"},
		mountSnapshots: map[string]bkcache.ImmutableRef{"/ro": metaParent},
	}
	base := &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
		Mounts: ContainerMounts{{
			Target:          "/ro",
			Readonly:        true,
			DirectorySource: new(LazyAccessor[*Directory, *Container]),
		}},
		Lazy: baseOp,
	}
	baseRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "exec-final-base", base)
	child := newExecPartsTestChild(t, baseRes, base)
	child.Lazy = &containerPartsTestSuccessfulExecOp{ContainerExecLazy: child.Lazy.(*ContainerExecLazy)}
	childRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "exec-final-child", child)

	require.NoError(t, cache.EvaluateParts(ctx, childRes, ContainerPartExecMeta))
	require.Equal(t, 1, baseOp.mountRunsFor("/ro"))
	mountedDir, ok := child.mountAt("/ro").DirectorySource.Peek()
	require.True(t, ok)
	mountedSnapshot, ok := mountedDir.Snapshot.Peek()
	require.True(t, ok)
	require.Same(t, metaChild, mountedSnapshot)
	require.Nil(t, child.lazyOpForRouting())
	require.False(t, dagql.HasPendingLazyEvaluation(childRes))

	encoded, err := child.EncodePersistedObject(ctx, cache)
	require.NoError(t, err)
	var persisted struct {
		Form string `json:"form"`
	}
	require.NoError(t, json.Unmarshal(encoded.JSON, &persisted))
	require.Equal(t, persistedContainerFormReady, persisted.Form)
}

func newExecPartsTestChild(t *testing.T, baseRes dagql.ObjectResult[*Container], base *Container) *Container {
	t.Helper()
	clonedMounts, err := CloneContainerMounts(t.Context(), base.Mounts)
	require.NoError(t, err)
	child := &Container{
		Config:       CloneContainerImageConfig(base.Config),
		Mounts:       clonedMounts,
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
	}
	require.NoError(t, child.WithExec(t.Context(), baseRes, ContainerExecOpts{
		Args: []string{"/bin/false"},
	}, nil, dagql.ObjectResult[*Module]{}, nil))
	return child
}

// The user-visible headline: a metadata read on an exec child settles
// metadata by delegation and runs nothing, while demanding an exec
// output attempts the run. (In a real engine the failing exec surfaces
// at the first output demand; here the run's attempt fails at the query
// lookup, which proves the same routing without an engine.)
func TestContainerExecMetadataReadRunsNothing(t *testing.T) {
	t.Parallel()
	ctx, cache, srv, sessionID := newContainerPartsTestCtx(t)

	baseOp := &containerPartsTestBaseOp{
		LazyState: NewLazyState(),
		workdir:   "/base",
		env:       []string{"FOO=bar"},
	}
	base := &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
		Lazy:         baseOp,
	}
	baseRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "exec-parts-base", base)

	child := newExecPartsTestChild(t, baseRes, base)
	childRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "exec-parts-child", child)

	// Metadata reads run nothing: no exec, no parent snapshot work.
	require.NoError(t, cache.EvaluateParts(ctx, childRes, ContainerPartMetadata))
	require.Equal(t, "/base", child.Config.WorkingDir)
	require.Equal(t, []string{"FOO=bar"}, child.Config.Env)
	require.Equal(t, int32(0), baseOp.fsRuns.Load())
	require.True(t, dagql.HasPendingLazyEvaluation(childRes))

	// Demanding an exec output attempts the run: the parent's rootfs is
	// materialized for mounting and the body proceeds to the process
	// setup, which fails without an engine. The failure leaves the group
	// retryable and the settled metadata untouched.
	err := cache.EvaluateParts(ctx, childRes, ContainerPartExecMeta)
	require.Error(t, err)
	require.Equal(t, int32(1), baseOp.fsRuns.Load())
	require.NoError(t, cache.EvaluateParts(ctx, childRes, ContainerPartMetadata))
	err = cache.EvaluateParts(ctx, childRes, ContainerPartExecMeta)
	require.Error(t, err)

	// Whole-result evaluation (sync) fails on the same run while the
	// metadata read above succeeded: the failing exec surfaces at output
	// demands, never at metadata reads.
	require.Error(t, cache.Evaluate(ctx, childRes))
}

// A read-only mount whose source is pending stays pending across
// metadata reads, and demanding that one mount part forces exactly the
// parent's mount group - not the rootfs, not the exec.
func TestContainerExecReadOnlyMountStaysPending(t *testing.T) {
	t.Parallel()
	ctx, cache, srv, sessionID := newContainerPartsTestCtx(t)

	baseOp := &containerPartsTestBaseOp{
		LazyState:    NewLazyState(),
		workdir:      "/base",
		mountTargets: []string{"/ro"},
	}
	base := &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
		Mounts: ContainerMounts{{
			Target:          "/ro",
			Readonly:        true,
			DirectorySource: new(LazyAccessor[*Directory, *Container]),
		}},
		Lazy: baseOp,
	}
	baseRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "exec-parts-ro-base", base)

	child := newExecPartsTestChild(t, baseRes, base)
	childRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "exec-parts-ro-child", child)

	// The metadata read settles the mount list shape and nothing else:
	// the read-only mount's source chain stays pending.
	require.NoError(t, cache.EvaluateParts(ctx, childRes, ContainerPartMetadata))
	require.Equal(t, 0, baseOp.mountRunsFor("/ro"))
	require.Equal(t, int32(0), baseOp.fsRuns.Load())
	roMnt := child.mountAt("/ro")
	require.NotNil(t, roMnt)
	_, roSet := roMnt.DirectorySource.Peek()
	require.False(t, roSet)

	// Demanding the mount part delegates exactly that part: the parent's
	// mount group runs, its fs group does not, and the exec never runs.
	require.NoError(t, cache.EvaluateParts(ctx, childRes, ContainerPartMount("/ro")))
	require.Equal(t, 1, baseOp.mountRunsFor("/ro"))
	require.Equal(t, int32(0), baseOp.fsRuns.Load())
	_, roSet = child.mountAt("/ro").DirectorySource.Peek()
	require.True(t, roSet)
	require.True(t, dagql.HasPendingLazyEvaluation(childRes))
}

func TestContainerExecEvaluatesParentMountsConcurrently(t *testing.T) {
	t.Parallel()
	ctx, cache, srv, sessionID := newContainerPartsTestCtx(t)

	entered := make(chan string, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseBodies := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseBodies()
	baseOp := &containerPartsTestBaseOp{
		LazyState:    NewLazyState(),
		mountTargets: []string{"/one", "/two"},
		mountBodyHook: func(target string) {
			entered <- target
			<-release
		},
	}
	base := &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
		Mounts: ContainerMounts{
			{Target: "/one", Readonly: true, DirectorySource: new(LazyAccessor[*Directory, *Container])},
			{Target: "/two", Readonly: true, DirectorySource: new(LazyAccessor[*Directory, *Container])},
		},
		Lazy: baseOp,
	}
	baseRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "exec-concurrent-mounts-base", base)
	child := newExecPartsTestChild(t, baseRes, base)
	childRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "exec-concurrent-mounts-child", child)

	eval := make(chan error, 1)
	go func() {
		eval <- cache.EvaluateParts(ctx, childRes, ContainerPartExecMeta)
	}()

	seen := map[string]bool{}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for len(seen) < 2 {
		select {
		case target := <-entered:
			seen[target] = true
		case <-timer.C:
			t.Fatal("parent mount bodies did not run concurrently")
		}
	}
	releaseBodies()
	select {
	case err := <-eval:
		require.Error(t, err)
	case <-timer.C:
		t.Fatal("exec evaluation did not return")
	}
	require.Equal(t, 1, baseOp.mountRunsFor("/one"))
	require.Equal(t, 1, baseOp.mountRunsFor("/two"))
}

// Deliberate pin of the council-ruled VolatileEnv corner: the exec's
// metadata delegates the parent's volatile names verbatim - the old
// whole-body evaluation additionally FILTERED the list to
// session-resolvable names as a side effect of resolving values, so an
// expand=true reference to a volatile name whose session binding is
// gone previously expanded to the empty string after an evaluated exec.
// It now errors explicitly. Resolved values stay local to the run (a
// snapshot body must not write metadata); consumers read names.
func TestContainerExecMetadataKeepsVolatileNamesAndExpandErrors(t *testing.T) {
	t.Parallel()
	ctx, cache, srv, sessionID := newContainerPartsTestCtx(t)

	baseOp := &containerPartsTestBaseOp{
		LazyState:   NewLazyState(),
		workdir:     "/base",
		volatileEnv: []string{"FOO=stale"},
	}
	base := &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
		Lazy:         baseOp,
	}
	baseRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "volatile-pin-base", base)

	child := newExecPartsTestChild(t, baseRes, base)
	childRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "volatile-pin-child", child)

	require.NoError(t, cache.EvaluateParts(ctx, childRes, ContainerPartMetadata))
	// The name survives the exec's metadata settle unfiltered.
	require.Equal(t, []string{"FOO=stale"}, child.VolatileEnv)

	// Expanding a volatile name errors explicitly instead of silently
	// expanding to empty.
	_, err := ExpandContainerInput(child, "$FOO", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), `expand cannot be used with volatile env variable "FOO"`)
}
