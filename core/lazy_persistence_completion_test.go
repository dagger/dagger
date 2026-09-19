package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestLazyPersistenceCompletedBodyGuards(t *testing.T) {
	for _, family := range []string{"File", "Directory"} {
		t.Run(family, func(t *testing.T) {
			var state *LazyState
			var guard func() (func(), error)
			var version dagql.PersistedOutputVersion
			switch family {
			case "File":
				op := &FileBlobLazy{LazyState: NewLazyState()}
				file := &File{Lazy: op}
				state, guard, version = &op.LazyState, file.lockForPersistence, file
			case "Directory":
				op := &DirectoryScratchLazy{LazyState: NewLazyState()}
				dir := &Directory{Lazy: op}
				state, guard, version = &op.LazyState, dir.lockForPersistence, dir
			}
			state.LazyMu.Lock()
			_, err := guard()
			state.LazyMu.Unlock()
			require.ErrorIs(t, err, dagql.ErrPersistStateNotReady, "pending bodies stay excluded")
			require.NoError(t, state.Evaluate(t.Context(), family, nil))
			state.LazyMu.Lock()
			defer state.LazyMu.Unlock()
			unlock, err := guard()
			require.NoError(t, err, "a completed retained operation has no active body")
			unlock()
			_, err = version.PersistedOutputRevision()
			require.NoError(t, err)
			require.Equal(t, uint64(1), state.outputRevision.Load())
		})
	}
}

func TestLazyCompletedContainerPublicationExclusion(t *testing.T) {
	op := &ContainerBuiltinLazy{LazyState: NewLazyState()}
	ctr := NewContainer(Platform{})
	ctr.Lazy = op
	require.NoError(t, op.LazyState.Evaluate(t.Context(), "builtin", nil))
	// The state latch also excludes acquisition publication, even when no
	// operation body can run again. Both readers and publication must keep it.
	unlock, err := ctr.lockForPersistence(false)
	require.NoError(t, err)
	_, err = ctr.tryPartPublicationGuard()
	unlock()
	require.ErrorIs(t, err, dagql.ErrPersistStateNotReady)
	unlock, err = ctr.tryPartPublicationGuard()
	require.NoError(t, err)
	locked := op.LazyMu.TryLock()
	if locked {
		op.LazyMu.Unlock()
	}
	unlock()
	require.False(t, locked)
	unlock, err = ctr.lockForPersistence(false)
	require.NoError(t, err)
	unlock()
}

func TestLazyPersistenceCompletedGroupGuards(t *testing.T) {
	op := &ContainerFromImageRefLazy{LazyState: NewLazyState()}
	ctr := NewContainer(Platform{})
	ctr.Lazy = op
	require.NoError(t, op.EvaluateGroup(t.Context(), "image metadata", ContainerLazyGroupMetadata, nil))
	group := op.groups[ContainerLazyGroupMetadata]
	group.mu.Lock()
	defer group.mu.Unlock()
	for _, guard := range []func() (func(), error){ctr.tryPartPublicationGuard, func() (func(), error) { return ctr.lockForPersistence(false) }} {
		unlock, err := guard()
		require.NoError(t, err, "completed groups do not exclude ownership or persistence reads")
		unlock()
	}
	entered, finish, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- op.EvaluateGroup(t.Context(), "image fs", ContainerLazyGroupWrite, func(context.Context) error {
			close(entered)
			<-finish
			return nil
		})
	}()
	<-entered
	_, err := ctr.tryPartPublicationGuard()
	close(finish)
	require.NoError(t, <-done)
	require.ErrorIs(t, err, dagql.ErrPersistStateNotReady, "the pending fs group still excludes publication")
	require.False(t, op.IsEvaluated(), "group completion does not set whole-operation completion")
	require.Equal(t, uint64(2), op.outputRevision.Load())
}
