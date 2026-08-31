package core

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

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
