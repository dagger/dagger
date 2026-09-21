package core

import (
	"context"
	"errors"
	"fmt"
	"testing"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

type mountLazyRef struct {
	bkcache.ImmutableRef
	releases int
	canceled bool
	err      error
}

func (r *mountLazyRef) Release(ctx context.Context) error {
	r.releases++
	r.canceled = ctx.Err() != nil
	return errors.Join(r.ImmutableRef.Release(ctx), r.err)
}

type mountLazyManager struct {
	bkcache.SnapshotManager
	refs      []*mountLazyRef
	err       error
	afterOpen func()
}

func (m *mountLazyManager) GetBySnapshotID(ctx context.Context, id string, opts ...bkcache.RefOption) (bkcache.ImmutableRef, error) {
	r, err := m.SnapshotManager.GetBySnapshotID(ctx, id, opts...)
	if err != nil {
		return nil, err
	}
	ref := &mountLazyRef{ImmutableRef: r, err: m.err}
	m.refs = append(m.refs, ref)
	if m.afterOpen != nil {
		m.afterOpen()
	}
	return ref, nil
}

func TestMountLazyDetachedOwnership(t *testing.T) {
	for _, file := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("file=%t/failure=%t", file, fail), func(t *testing.T) {
				ctx, store, cache, srv, server := executionFixture(t)
				ref, _ := store.Build(t, nil, "payload", "owned bytes")
				parent := attachTransferObject(t, ctx, cache, srv, "mount-ownership", "parent", NewContainer(Platform{}))
				ctr := NewContainer(Platform{})
				var op LazyContainerParts
				if file {
					source := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File])}
					source.SetPath("/payload")
					source.SetSnapshot(ref)
					input := attachTransferObject(t, ctx, cache, srv, "mount-ownership", "file", source)
					op = &ContainerWithMountedFileLazy{LazyState: NewLazyState(), Parent: parent, Source: input, Target: "/target"}
					if !fail {
						ctr.Mounts = ContainerMounts{{Target: "/target", FileSource: new(LazyAccessor[*File, *Container])}}
					}
				} else {
					input := attachTransferObject(t, ctx, cache, srv, "mount-ownership", "directory", partTestDirectory(ref, "/"))
					op = &ContainerWithMountedDirectoryLazy{LazyState: NewLazyState(), Parent: parent, Source: input, Target: "/target"}
					if !fail {
						ctr.Mounts = ContainerMounts{{Target: "/target", DirectorySource: new(LazyAccessor[*Directory, *Container])}}
					}
				}
				observed := &mountLazyManager{SnapshotManager: store.Manager}
				server.cacheManager = observed
				evalCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				var releaseErr error
				if fail {
					releaseErr = errors.New("injected detached release failure")
					observed.err = releaseErr
					observed.afterOpen = cancel
				}
				err := op.EvaluateContainerGroup(evalCtx, ctr, ContainerLazyGroupWrite)
				require.Len(t, observed.refs, 1)
				opened := observed.refs[0]
				if fail {
					require.ErrorContains(t, err, "mount at target")
					require.ErrorIs(t, err, releaseErr)
					require.Equal(t, 1, opened.releases)
					require.False(t, opened.canceled, "detached cleanup must ignore caller cancellation")
				} else {
					require.NoError(t, err)
					require.Zero(t, opened.releases)
					testutil.CheckFile(t, opened, "payload", "owned bytes")
					require.NoError(t, ctr.OnRelease(ctx))
					require.Equal(t, 1, opened.releases)
				}
				testutil.CheckFile(t, ref, "payload", "owned bytes")
			})
		}
	}
}
