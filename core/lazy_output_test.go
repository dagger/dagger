package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/stretchr/testify/require"
)

// Codec fixtures start with their output accessors already populated. Run the
// state transition without I/O, then retain the typed operation on the value.
func evaluatedLazyFixture[T interface {
	dagql.Typed
	*Directory | *File | *Container
}](value T, op Lazy[T]) error {
	switch value := any(value).(type) {
	case *Directory:
		value.Lazy = any(op).(Lazy[*Directory])
	case *File:
		value.Lazy = any(op).(Lazy[*File])
	case *Container:
		value.Lazy = any(op).(Lazy[*Container])
	}
	return any(op).(interface{ ContainerLazyState() *LazyState }).ContainerLazyState().Evaluate(context.Background(), "codec fixture", nil)
}

var scratchLazyBenchmarkOutput *Directory

func BenchmarkScratchLazyConstruction(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		dir := &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]), Lazy: &DirectoryScratchLazy{LazyState: NewLazyState()}}
		dir.SetPath("/")
		scratchLazyBenchmarkOutput = dir
	}
}

func TestLazyInputAttachment(t *testing.T) {
	t.Run("existing completed kinds attach", func(t *testing.T) {
		env := newPersistedFamiliesTestEnv(t, "completed-attach")
		ctx, cache, srv := env.open(t)
		parent := env.attach(t, ctx, cache, srv, "parent", containerPersistenceTestDirectory("parent", "/")).(dagql.ObjectResult[*Directory])
		dir := containerPersistenceTestDirectory("child", "/selected")
		directoryLazyOperation := &DirectorySubdirectoryLazy{LazyState: NewLazyState(), Parent: parent}
		require.NoError(t, evaluatedLazyFixture(dir, directoryLazyOperation))
		file := &File{}
		require.NoError(t, evaluatedLazyFixture(file, &FileSubfileLazy{LazyState: NewLazyState(), Parent: parent}))
		for _, value := range []interface {
			AttachDependencyResultsKinds(context.Context, dagql.AnyResult, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.DependencyResult, error)
		}{dir, file} {
			calls := 0
			deps, err := value.AttachDependencyResultsKinds(ctx, nil, func(res dagql.AnyResult) (dagql.AnyResult, error) {
				calls++
				require.Same(t, parent.Self(), res.(dagql.ObjectResult[*Directory]).Self())
				return res, nil
			})
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			require.Len(t, deps, 1)
			require.False(t, deps[0].Owned)
			require.Same(t, parent.Self(), deps[0].Result.(dagql.ObjectResult[*Directory]).Self())
		}
		require.Same(t, directoryLazyOperation, dir.Lazy)
	})
}

func TestEvaluatedLazyOperationAttachmentBeforePublication(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "detached-completion")
	ctx, cache, srv := env.open(t)
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "selected"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "selected", "data"), []byte("data"), 0644))
	source := containerPersistenceTestDirectory("source-tree", "/")
	source.Snapshot.setValue(&operationTreeRef{cacheVolumeTestImmutableRef: &cacheVolumeTestImmutableRef{id: "source-tree", snapshotID: "source-tree"}, root: root})
	parent := env.attach(t, ctx, cache, srv, "source", source).(dagql.ObjectResult[*Directory])
	dir, err := source.Subdirectory(ctx, parent, "selected")
	require.NoError(t, err)
	file, err := source.Subfile(ctx, parent, "selected/data")
	require.NoError(t, err)
	for _, value := range []interface {
		LazyEvalFunc() dagql.LazyEvalFunc
		AttachDependencyResultsKinds(context.Context, dagql.AnyResult, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.DependencyResult, error)
	}{dir, file} {
		require.NoError(t, value.LazyEvalFunc()(ctx))
		deps, err := value.AttachDependencyResultsKinds(ctx, nil, func(res dagql.AnyResult) (dagql.AnyResult, error) { return res, nil })
		require.NoError(t, err)
		require.Len(t, deps, 1)
		require.False(t, deps[0].Owned)
		require.Equal(t, persistedRowID(t, cache, parent), persistedRowID(t, cache, deps[0].Result))
	}
	require.IsType(t, &DirectorySubdirectoryLazy{}, dir.Lazy)
	require.True(t, dir.Lazy.IsEvaluated())
	require.IsType(t, &FileSubfileLazy{}, file.Lazy)
	require.True(t, file.Lazy.IsEvaluated())
}

func TestMoveProducedOutputs(t *testing.T) {
	platform := Platform{OS: "linux", Architecture: "arm64"}
	releases := 0
	src := containerPersistenceTestDirectory("owned", "/selected")
	src.Services = ServiceBindings{{Hostname: "saved"}}
	src.Snapshot.setValue(&cacheVolumeTestImmutableRef{release: func(context.Context) error { releases++; return nil }})
	dst := &Directory{Platform: platform, Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory])}
	dst.Dir.setValue("preseeded")
	directoryRevisionBefore, err := dst.PersistedOutputRevision()
	require.NoError(t, err)
	require.NoError(t, moveDirectoryOutput(dst, src))
	directoryRevisionAfter, err := dst.PersistedOutputRevision()
	require.NoError(t, err)
	require.NotEqual(t, directoryRevisionBefore, directoryRevisionAfter)
	require.Equal(t, platform, dst.Platform)
	path, _ := dst.Dir.Peek()
	require.Equal(t, "/selected", path)
	require.NoError(t, src.OnRelease(t.Context()))
	require.Zero(t, releases)
	src.Services[0].Hostname = "changed"
	require.Equal(t, "saved", dst.Services[0].Hostname)
	require.Error(t, moveDirectoryOutput(dst, src))
	require.NoError(t, dst.OnRelease(t.Context()))
	require.Equal(t, 1, releases)
	file := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File])}
	file.File.setValue("saved")
	file.Snapshot.setValue(&cacheVolumeTestImmutableRef{release: func(context.Context) error { releases++; return nil }})
	output := &File{Platform: platform, File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File])}
	fileRevisionBefore, err := output.PersistedOutputRevision()
	require.NoError(t, err)
	require.NoError(t, moveFileOutput(output, file))
	fileRevisionAfter, err := output.PersistedOutputRevision()
	require.NoError(t, err)
	require.NotEqual(t, fileRevisionBefore, fileRevisionAfter)
	require.NoError(t, file.OnRelease(t.Context()))
	require.Equal(t, 1, releases)
	require.NoError(t, output.OnRelease(t.Context()))
	require.Equal(t, 2, releases)
}
