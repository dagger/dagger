package core

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

type containerSelectorPartsTestServer struct {
	*mockServer
	dag *dagql.Server
}

func (srv *containerSelectorPartsTestServer) Server(context.Context) (*dagql.Server, error) {
	return srv.dag, nil
}

func TestContainerDirectorySelectorReturnsRootFSErrorBeforeSelection(t *testing.T) {
	t.Parallel()
	queryServer := &containerSelectorPartsTestServer{mockServer: &mockServer{}}
	ctx, cache, srv, sessionID := newContainerPartsTestCtxWithQueryServer(t, queryServer)
	queryServer.dag = srv
	ctx = ContextWithQuery(ctx, &Query{Server: queryServer})

	injected := errors.New("injected rootfs failure")
	baseOp := &containerPartsTestBaseOp{
		LazyState: NewLazyState(),
		fsErr:     injected,
	}
	base := &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
		Lazy:         baseOp,
	}
	baseRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "selector-rootfs-error-base", base)
	dir := &Directory{
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
		Lazy: &ContainerDirectoryLazy{
			LazyState: NewLazyState(),
			Parent:    baseRes,
			Path:      "/x",
		},
	}

	err := dir.LazyEvalFunc()(ctx)
	require.ErrorIs(t, err, injected)
	require.Equal(t, int32(1), baseOp.fsRuns.Load())
}

// ctr.directory("/a") on a container with several mounted inputs
// evaluates metadata plus the part /a lives in: the unrelated mount's
// pending source and the rootfs are never materialized.
func TestContainerDirectorySelectorLeavesSiblingMountsPending(t *testing.T) {
	t.Parallel()
	ctx, cache, srv, sessionID := newContainerPartsTestCtx(t)
	// The selector body resolves a query/server lazily only on the rootfs
	// branch; the mount branch needs neither, but the lookup at the top
	// must find a Query.
	ctx = ContextWithQuery(ctx, &Query{Server: &mockServer{}})

	baseOp := &containerPartsTestBaseOp{
		LazyState:    NewLazyState(),
		workdir:      "/base",
		mountTargets: []string{"/a", "/b"},
	}
	base := &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
		Mounts: ContainerMounts{
			{
				Target:          "/a",
				Readonly:        true,
				DirectorySource: new(LazyAccessor[*Directory, *Container]),
			},
			{
				Target:          "/b",
				Readonly:        true,
				DirectorySource: new(LazyAccessor[*Directory, *Container]),
			},
		},
		Lazy: baseOp,
	}
	baseRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "selector-parts-base", base)

	dir := &Directory{
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	dir.Lazy = &ContainerDirectoryLazy{
		LazyState: NewLazyState(),
		Parent:    baseRes,
		Path:      "/a",
	}
	require.NoError(t, dir.LazyEvalFunc()(ctx))

	require.Equal(t, 1, baseOp.mountRunsFor("/a"))
	require.Equal(t, 0, baseOp.mountRunsFor("/b"))
	require.Equal(t, int32(0), baseOp.fsRuns.Load())
	require.Equal(t, int32(1), baseOp.metaRuns.Load())
	require.True(t, dagql.HasPendingLazyEvaluation(baseRes))

	dirPath, ok := dir.Dir.Peek()
	require.True(t, ok)
	require.Equal(t, "/", dirPath)
	require.Nil(t, dir.Lazy)
}

// The rootfs view evaluates exactly the parent's fs part: mounted
// sources stay pending.
func TestContainerRootFSSelectorLeavesMountsPending(t *testing.T) {
	t.Parallel()
	ctx, cache, srv, sessionID := newContainerPartsTestCtx(t)

	baseOp := &containerPartsTestBaseOp{
		LazyState:    NewLazyState(),
		workdir:      "/base",
		mountTargets: []string{"/a"},
	}
	base := &Container{
		FS:           new(LazyAccessor[*Directory, *Container]),
		MetaSnapshot: new(LazyAccessor[bkcache.ImmutableRef, *Container]),
		Mounts: ContainerMounts{{
			Target:          "/a",
			Readonly:        true,
			DirectorySource: new(LazyAccessor[*Directory, *Container]),
		}},
		Lazy: baseOp,
	}
	baseRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "rootfs-parts-base", base)

	dir := &Directory{
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	dir.Lazy = &ContainerRootFSLazy{
		LazyState: NewLazyState(),
		Parent:    baseRes,
	}
	require.NoError(t, dir.LazyEvalFunc()(ctx))

	require.Equal(t, int32(1), baseOp.fsRuns.Load())
	require.Equal(t, 0, baseOp.mountRunsFor("/a"))
	require.True(t, dagql.HasPendingLazyEvaluation(baseRes))
}

// stdout on an exec attempts the run (which needs the parent's parts
// mounted) but never runs the child's read-only mount delegation: the
// child's own copy of an untouched mount stays unset.
func TestContainerStdoutSkipsReadOnlyMountDelegation(t *testing.T) {
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
	baseRes := attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "stdout-parts-base", base)

	child := newExecPartsTestChild(t, baseRes, base)
	attachContainerPartsTestResult(t, ctx, cache, srv, sessionID, "stdout-parts-child", child)

	// The internal force behind Stdout is scoped to the exec-meta part:
	// the run is attempted (and fails without an engine) after the
	// parent's fs and mount parts materialize for mounting, but the
	// child's own read-only mount delegation never runs.
	_, err := child.Stdout(ctx)
	require.Error(t, err)
	require.Equal(t, int32(1), baseOp.fsRuns.Load())
	require.Equal(t, 1, baseOp.mountRunsFor("/ro"))
	_, roSet := child.mountAt("/ro").DirectorySource.Peek()
	require.False(t, roSet)
}
