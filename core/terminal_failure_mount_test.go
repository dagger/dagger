package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
)

// When an exec fails and a terminal opens on its output, the terminal must not
// change the cached container the exec ran from. The failed output is held only
// by the failed operation, so once that ends and garbage collection runs, a
// cached container pointing at it can no longer run an exec: preparing its
// mount fails with "parent snapshot ... does not exist".
func TestTerminalFailureMountLeavesCachedInputMount(t *testing.T) {
	for _, kind := range []string{"directory", "file"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			store := testutil.NewStore(t)

			// The cached container's mount, held by its result's owner lease.
			inputRef, _ := store.Build(t, nil, "foo", "FOO")
			inputMount := terminalTestMount(kind, inputRef)

			// The failed exec's output for that mount, held only by the
			// failed operation.
			outputRef, failedOperation := store.Build(t, inputRef, "foo", "FOOFOO")

			terminalMount, ok := terminalFailureMount(terminalTestMount(kind, inputRef), inputMount, outputRef)
			require.True(t, ok)
			require.Equal(t, outputRef.SnapshotID(), terminalTestMountSnapshot(t, kind, terminalMount).SnapshotID(),
				"the terminal shows the failed output")

			// The failed operation ends and a garbage collection pass runs.
			require.NoError(t, outputRef.Release(ctx))
			require.NoError(t, store.Manager.RemoveLease(ctx, failedOperation))
			store.GC(t)

			// The next exec from the cached container prepares a new snapshot
			// on top of its mount.
			cached := terminalTestMountSnapshot(t, kind, inputMount)
			opCtx, releaseOperation, err := bkcache.WithLazyLease(ctx, store.Leases, bkcache.MakeTemporary)
			require.NoError(t, err)
			defer func() { require.NoError(t, releaseOperation(context.Background())) }()
			next, err := store.Manager.New(opCtx, cached)
			require.NoError(t, err)
			require.NoError(t, next.Release(ctx))
			require.Equal(t, inputRef.SnapshotID(), cached.SnapshotID(), "the cached container's mount changed")
		})
	}
}

func terminalTestMount(kind string, ref bkcache.ImmutableRef) ContainerMount {
	mount := ContainerMount{Target: "/mnt"}
	switch kind {
	case "directory":
		dir := &Directory{
			Dir:      new(LazyAccessor[string, *Directory]),
			Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
		}
		dir.SetPath("/")
		dir.SetSnapshot(ref)
		mount.DirectorySource = new(LazyAccessor[*Directory, *Container])
		mount.DirectorySource.setValue(dir)
	case "file":
		file := &File{
			File:     new(LazyAccessor[string, *File]),
			Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]),
		}
		file.SetPath("/foo")
		file.SetSnapshot(ref)
		mount.FileSource = new(LazyAccessor[*File, *Container])
		mount.FileSource.setValue(file)
	}
	return mount
}

func terminalTestMountSnapshot(t *testing.T, kind string, mount ContainerMount) bkcache.ImmutableRef {
	t.Helper()
	var (
		ref bkcache.ImmutableRef
		ok  bool
	)
	switch kind {
	case "directory":
		dir, dirOK := mount.DirectorySource.Peek()
		require.True(t, dirOK)
		ref, ok = dir.Snapshot.Peek()
	case "file":
		file, fileOK := mount.FileSource.Peek()
		require.True(t, fileOK)
		ref, ok = file.Snapshot.Peek()
	}
	require.True(t, ok)
	require.NotNil(t, ref)
	return ref
}
