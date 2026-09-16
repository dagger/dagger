package core

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/stretchr/testify/require"
)

func TestLazyFilesystemCompletion(t *testing.T) {
	for _, kind := range []string{"Directory", "File"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			state := NewLazyState()
			var runs atomic.Int32
			var output *filesystemOutput
			var evaluate func() error
			var setPath func(string)
			var version dagql.PersistedOutputVersion
			if kind == "Directory" {
				dir := &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory])}
				output, version, setPath = &dir.filesystemOutput, dir, dir.SetPath
				evaluate = func() error {
					return dir.evaluateLazy(ctx, &state, "test.directory", func(context.Context) error {
						runs.Add(1)
						dir.SetPath("/")
						dir.SetSnapshot(nil)
						return nil
					})
				}
			} else {
				file := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File])}
				output, version, setPath = &file.filesystemOutput, file, file.SetPath
				evaluate = func() error {
					return file.evaluateLazy(ctx, &state, "test.file", func(context.Context) error {
						runs.Add(1)
						file.SetPath("/file")
						file.SetSnapshot(nil)
						return nil
					})
				}
			}
			var wg sync.WaitGroup
			errs := make([]error, 64)
			for i := range errs {
				wg.Go(func() { errs[i] = evaluate() })
			}
			wg.Wait()
			for _, err := range errs {
				require.NoError(t, err)
			}
			require.True(t, state.IsEvaluated())
			require.Equal(t, int32(1), runs.Load())
			require.Equal(t, dagql.OutputRevision(3), output.OutputRev, "two setters and one completion")
			for range 10 {
				require.NoError(t, evaluate())
				rev, err := version.PersistedOutputRevision()
				require.NoError(t, err)
				require.Equal(t, dagql.OutputRevision(3), rev)
			}
			setPath("/later")
			require.Equal(t, dagql.OutputRevision(4), output.OutputRev)
		})
	}
}

func TestLazyFilesystemRequiresInstalledOutput(t *testing.T) {
	ctx := context.Background()
	dir := &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory])}
	state := NewLazyState()
	require.ErrorContains(t, dir.evaluateLazy(ctx, &state, "empty", nil), "path is unset")
	require.False(t, state.IsEvaluated())
	dir.SetPath("/")
	require.ErrorContains(t, dir.evaluateLazy(ctx, &state, "empty", nil), "snapshot is unset")
	require.False(t, state.IsEvaluated())
	dir.SetSnapshot(nil)
	require.NoError(t, dir.evaluateLazy(ctx, &state, "empty", nil))
	require.True(t, state.IsEvaluated())

	file := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File])}
	fileState := NewLazyState()
	require.ErrorContains(t, file.evaluateLazy(ctx, &fileState, "empty", nil), "path is unset")
	file.SetPath("/file")
	require.ErrorContains(t, file.evaluateLazy(ctx, &fileState, "empty", nil), "snapshot is unset")
	require.False(t, fileState.IsEvaluated())
	file.SetSnapshot(nil)
	require.NoError(t, file.evaluateLazy(ctx, &fileState, "empty", nil))
	require.True(t, fileState.IsEvaluated())
}

func TestLazyWholeContainerRetainsOperation(t *testing.T) {
	previous := containerPartDiagnosticsEnabled
	containerPartDiagnosticsEnabled = true
	defer func() { containerPartDiagnosticsEnabled = previous }()
	ctx := context.Background()
	ctr := NewContainer(Platform{OS: "linux", Architecture: "amd64"})
	var runs int
	op := &captureContainerWholeOp{LazyState: NewLazyState(), body: func(*Container) { runs++ }}
	ctr.Lazy = op
	require.True(t, ctr.HasPendingLazyComputation())
	require.NoError(t, ctr.Evaluate(ctx))
	require.Same(t, op, ctr.Lazy)
	require.True(t, op.IsEvaluated())
	require.False(t, ctr.HasPendingLazyComputation())
	require.Nil(t, ctr.LazyEvalFunc())
	for _, part := range []dagql.PartKey{ContainerPartMetadata, ContainerPartFS, ContainerPartExecMeta} {
		require.True(t, ctr.containerPartComputed(ctx, op, part))
		final, err := containerParentPartFinal(ctx, ctr, part)
		require.NoError(t, err)
		require.True(t, final)
	}
	data, err := json.Marshal(ctr.CacheDebugValue())
	require.NoError(t, err)
	var debug struct {
		Parts map[string]struct {
			Consumed bool `json:"consumed"`
		} `json:"parts"`
	}
	require.NoError(t, json.Unmarshal(data, &debug))
	require.Len(t, debug.Parts, 3)
	for _, part := range debug.Parts {
		require.True(t, part.Consumed)
	}
	require.NoError(t, ctr.Evaluate(ctx))
	require.Equal(t, 1, runs)
}

func TestLazyEvaluatedFilesystemClones(t *testing.T) {
	ctx, store, cache, srv, _ := executionFixture(t)
	ref, _ := store.Build(t, nil, "nested/file", "saved bytes")
	source := &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory])}
	source.SetPath("/")
	source.SetSnapshot(ref)
	parent := attachTransferObject(t, ctx, cache, srv, "evaluated-clones", "source", source)
	dir, err := source.Subdirectory(ctx, parent, "nested")
	require.NoError(t, err)
	dirOp := dir.Lazy
	require.NoError(t, dir.LazyEvalFunc()(ctx))
	require.Same(t, dirOp, dir.Lazy)
	require.True(t, dirOp.IsEvaluated())
	require.False(t, dir.HasPendingLazyComputation())
	_, path, err := materializedDirectorySnapshotAndPath(dir)
	require.NoError(t, err)
	require.Equal(t, "/nested", path)
	clone, err := cloneDetachedDirectoryForContainerResult(ctx, dir)
	require.NoError(t, err)
	clonedRef, ok := clone.Snapshot.Peek()
	require.True(t, ok)
	originalRef, _ := dir.Snapshot.Peek()
	require.NotSame(t, originalRef, clonedRef)
	require.Equal(t, originalRef.SnapshotID(), clonedRef.SnapshotID())
	require.Nil(t, clone.Lazy)
	require.NoError(t, clone.OnRelease(ctx))
	require.NoError(t, dir.OnRelease(ctx))

	file, err := source.Subfile(ctx, parent, "nested/file")
	require.NoError(t, err)
	fileOp := file.Lazy
	require.NoError(t, file.LazyEvalFunc()(ctx))
	require.Same(t, fileOp, file.Lazy)
	require.True(t, fileOp.IsEvaluated())
	require.False(t, file.HasPendingLazyComputation())
	fileClone, err := cloneDetachedFileForContainerResult(ctx, file)
	require.NoError(t, err)
	clonedRef, ok = fileClone.Snapshot.Peek()
	require.True(t, ok)
	originalRef, _ = file.Snapshot.Peek()
	require.NotSame(t, originalRef, clonedRef)
	require.Equal(t, originalRef.SnapshotID(), clonedRef.SnapshotID())
	require.Nil(t, fileClone.Lazy)
	require.NoError(t, fileClone.OnRelease(ctx))
	require.NoError(t, file.OnRelease(ctx))

	_, err = cloneDetachedDirectoryForContainerResult(ctx, &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory])})
	require.ErrorContains(t, err, "not installed")
	_, err = cloneDetachedFileForContainerResult(ctx, &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File])})
	require.ErrorContains(t, err, "not installed")
}
