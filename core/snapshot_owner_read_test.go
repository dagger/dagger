package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

type snapshotOwnerReadResult struct {
	revision dagql.OutputRevision
	links    []dagql.PersistedSnapshotRefLink
	err      error
}

type snapshotOwnerWholeOp struct {
	ContainerImportLazy
	diagnostic func()
}

func (op *snapshotOwnerWholeOp) IsEvaluated() bool {
	if op.diagnostic != nil {
		op.diagnostic()
	}
	return op.LazyState.IsEvaluated()
}

func TestSnapshotOwnerWaitsForWholeContainer(t *testing.T) {
	previousDiagnostics := containerPartDiagnosticsEnabled
	containerPartDiagnosticsEnabled = true
	t.Cleanup(func() { containerPartDiagnosticsEnabled = previousDiagnostics })
	env := newPersistedFamiliesTestEnv(t, "whole-owner-read")
	ctx, cache, srv := env.open(t)
	parent := env.attach(t, ctx, cache, srv, "parent", NewContainer(Platform{})).(dagql.ObjectResult[*Container])
	sourceValue := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File])}
	sourceValue.SetPath("/source")
	sourceValue.SetSnapshot(nil)
	source := env.attach(t, ctx, cache, srv, "source", sourceValue).(dagql.ObjectResult[*File])
	op := &snapshotOwnerWholeOp{ContainerImportLazy: ContainerImportLazy{LazyState: NewLazyState(), Parent: parent, Source: source}}
	ctr := NewContainer(Platform{})
	ctr.Lazy = op
	res := env.attach(t, ctx, cache, srv, "import", ctr)

	entered, allowBody := make(chan struct{}), make(chan struct{})
	finishBody := sync.OnceFunc(func() { close(allowBody) })
	defer finishBody()
	bodyDone := make(chan error, 1)
	go func() {
		// Use the import operation's whole-body latch and actual dependency
		// evaluation, stopping before unrelated archive/image I/O.
		bodyDone <- op.LazyState.Evaluate(ctx, "Container.import", func(ctx context.Context) error {
			close(entered)
			<-allowBody
			if err := cache.Evaluate(ctx, op.Parent, op.Source); err != nil {
				return err
			}
			ctr.FS.setValue(containerPersistenceTestDirectory("owned", "/"))
			return nil
		})
	}()
	<-entered
	ownerDone := make(chan snapshotOwnerReadResult, 1)
	go func() { ownerDone <- snapshotOwnerReadResult{err: cache.SyncResultSnapshotOwnerLeases(ctx, res)} }()
	awaitSnapshotOwnerLatch(t, ownerDone)

	encoded := make(chan error, 1)
	go func() {
		_, err := ctr.EncodePersistedObject(ctx, dagql.NewPersistEncodeContext(cache, 1, nil))
		encoded <- err
	}()
	select {
	case err := <-encoded:
		require.ErrorIs(t, err, dagql.ErrPersistStateNotReady, "live encoder must return before the body is released")
	case <-time.After(5 * time.Second):
		t.Fatal("encoder waited behind the owner reader's whole-body wait")
	}

	diagnosticEntered, allowDiagnostic := make(chan struct{}), make(chan struct{})
	finishDiagnostic := sync.OnceFunc(func() { close(allowDiagnostic) })
	defer finishDiagnostic()
	var once sync.Once
	op.diagnostic = func() { once.Do(func() { close(diagnosticEntered); <-allowDiagnostic }) }
	var snapshot bytes.Buffer
	diagnosticDone := make(chan error, 1)
	go func() { diagnosticDone <- cache.WriteDebugCacheSnapshot(&snapshot) }()
	select {
	case <-diagnosticEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("gated diagnostic could not read the operation pointer")
	}
	finishBody()
	// The real streamed diagnostic owns the graph read lock. Observe the
	// body's real dependency evaluation queued for the graph write lock.
	require.Eventually(t, func() bool {
		buf := make([]byte, 256<<10)
		for _, stack := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
			if strings.Contains(stack, "usesPartAcquisition(") && strings.Contains(stack, "sync.RWMutex") {
				return true
			}
		}
		return false
	}, 5*time.Second, time.Millisecond)
	finishDiagnostic()
	require.NoError(t, <-diagnosticDone)
	require.NoError(t, <-bodyDone)
	require.NoError(t, (<-ownerDone).err)
	require.True(t, json.Valid(snapshot.Bytes()))
	require.Contains(t, snapshot.String(), `"parts"`)
	revision, links, err := ctr.ReadSnapshotOwner()
	require.NoError(t, err)
	require.Equal(t, dagql.OutputRevision(1), revision)
	require.Equal(t, []dagql.PersistedSnapshotRefLink{{Role: "fs", RefKey: "owned"}}, links)
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
						if !op.LazyMu.TryLock() {
							return fmt.Errorf("LazyMu held while the ownership reader waits")
						}
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
