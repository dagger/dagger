package core

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/stretchr/testify/require"
)

type snapshotOwnerReadResult struct {
	revision dagql.OutputRevision
	links    []dagql.PersistedSnapshotRefLink
	err      error
}

func startSnapshotOwnerRead(value dagql.SnapshotOwnerReader) <-chan snapshotOwnerReadResult {
	done := make(chan snapshotOwnerReadResult, 1)
	go func() {
		revision, links, err := value.ReadSnapshotOwner()
		done <- snapshotOwnerReadResult{revision, links, err}
	}()
	return done
}

func awaitSnapshotOwnerLatch(t *testing.T, done <-chan snapshotOwnerReadResult) {
	t.Helper()
	// Mutex waits are not durable synctest waits. Observe the blocked reader,
	// without relying on a scheduling delay to establish the contention.
	require.Eventually(t, func() bool {
		select {
		case result := <-done:
			t.Fatalf("ownership read returned before latch release: %+v", result)
		default:
		}
		buf := make([]byte, 256<<10)
		for _, stack := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
			if strings.Contains(stack, "[sync.Mutex.Lock]") && strings.Contains(stack, "lockSnapshotOwnerRead(") {
				return true
			}
		}
		return false
	}, 5*time.Second, time.Millisecond)
}

func TestSnapshotOwnerCompletedReaders(t *testing.T) {
	for _, family := range []string{"Container", "File"} {
		t.Run(family, func(t *testing.T) {
			var value dagql.SnapshotOwnerReader
			var guard func() (func(), error)
			var nonblocking dagql.PersistedOutputVersion
			if family == "Container" {
				op := &ContainerBuiltinLazy{LazyState: NewLazyState()}
				ctr := NewContainer(Platform{})
				ctr.Lazy = op
				ctr.FS.setValue(containerPersistenceTestDirectory("owned", "/"))
				require.NoError(t, op.LazyState.Evaluate(t.Context(), "completed", nil))
				value, guard, nonblocking = ctr, ctr.lockSnapshotOwnerRead, ctr
			} else {
				op := &FileBlobLazy{LazyState: NewLazyState()}
				file := &File{Lazy: op, File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File])}
				require.NoError(t, file.evaluateLazy(t.Context(), &op.LazyState, "completed", func(context.Context) error {
					file.SetPath("/file")
					file.SetSnapshot(&cacheVolumeTestImmutableRef{id: "owned", snapshotID: "owned"})
					return nil
				}))
				value, nonblocking = file, file
				guard = func() (func(), error) { return file.lockSnapshotOwnerRead(func() any { return file.Lazy }) }
			}
			before, links, err := value.ReadSnapshotOwner()
			require.NoError(t, err)
			require.Len(t, links, 1)
			unlock, err := guard()
			require.NoError(t, err)
			release := sync.OnceFunc(unlock)
			defer release()
			done := startSnapshotOwnerRead(value)
			awaitSnapshotOwnerLatch(t, done)
			_, err = nonblocking.PersistedOutputRevision()
			require.ErrorIs(t, err, dagql.ErrPersistStateNotReady, "capture and Commit remain nonblocking")
			release()
			result := <-done
			require.NoError(t, result.err)
			require.Equal(t, before, result.revision)
			require.Equal(t, links, result.links)
		})
	}
}

func TestSnapshotOwnerWaitsForBody(t *testing.T) {
	for _, family := range []string{"Container", "File"} {
		t.Run(family, func(t *testing.T) {
			entered, allow := make(chan struct{}), make(chan struct{})
			release := sync.OnceFunc(func() { close(allow) })
			defer release()
			bodyDone := make(chan error, 1)
			var value dagql.SnapshotOwnerReader
			if family == "Container" {
				op := &ContainerFromImageRefLazy{LazyState: NewLazyState()}
				ctr := NewContainer(Platform{})
				ctr.Lazy = op
				require.NoError(t, op.EvaluateGroup(t.Context(), "metadata", ContainerLazyGroupMetadata, nil))
				value = ctr
				go func() {
					bodyDone <- op.EvaluateGroup(t.Context(), "fs", ContainerLazyGroupWrite, func(context.Context) error {
						close(entered)
						<-allow
						// Both must be available while the ownership reader waits.
						_ = ctr.lazyOpForRouting()
						op.LazyMu.Lock()
						op.LazyMu.Unlock()
						ctr.FS.setValue(containerPersistenceTestDirectory("owned", "/"))
						return nil
					})
				}()
			} else {
				op := &FileBlobLazy{LazyState: NewLazyState()}
				file := &File{Lazy: op, File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File])}
				value = file
				go func() {
					bodyDone <- file.evaluateLazy(t.Context(), &op.LazyState, "file", func(context.Context) error {
						close(entered)
						<-allow
						// The waiting reader must not retain outputMu.
						file.SetPath("/file")
						file.SetSnapshot(&cacheVolumeTestImmutableRef{id: "owned", snapshotID: "owned"})
						return nil
					})
				}()
			}
			<-entered
			done := startSnapshotOwnerRead(value)
			awaitSnapshotOwnerLatch(t, done)
			release()
			require.NoError(t, <-bodyDone)
			result := <-done
			require.NoError(t, result.err)
			require.Len(t, result.links, 1)
			require.Equal(t, "owned", result.links[0].RefKey)
			again, links, err := value.ReadSnapshotOwner()
			require.NoError(t, err)
			require.Equal(t, result.revision, again)
			require.Equal(t, result.links, links)
		})
	}
}

type snapshotOwnerEncodingOp struct {
	ContainerBuiltinLazy
	encode func()
}

func (op *snapshotOwnerEncodingOp) EncodePersisted(context.Context, *dagql.PersistEncodeContext) (json.RawMessage, error) {
	op.encode()
	return json.RawMessage(`{}`), nil
}

func TestSnapshotOwnerWaitsForEncoder(t *testing.T) {
	entered, allow := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(allow) })
	defer release()
	op := &snapshotOwnerEncodingOp{ContainerBuiltinLazy: ContainerBuiltinLazy{LazyState: NewLazyState()}, encode: func() { close(entered); <-allow }}
	ctr := NewContainer(Platform{})
	ctr.Lazy = op
	ctr.FS.setValue(containerPersistenceTestDirectory("owned", "/"))
	require.NoError(t, op.LazyState.Evaluate(t.Context(), "completed", nil))
	encoded := make(chan error, 1)
	go func() {
		_, err := ctr.EncodePersistedObject(t.Context(), dagql.NewPersistEncodeContext(nil, 1, nil))
		encoded <- err
	}()
	<-entered
	done := startSnapshotOwnerRead(ctr)
	awaitSnapshotOwnerLatch(t, done)
	release()
	require.NoError(t, <-encoded)
	result := <-done
	require.NoError(t, result.err)
	require.Len(t, result.links, 1)
}
