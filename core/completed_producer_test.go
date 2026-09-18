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

func TestRecordCompletedProducer(t *testing.T) {
	t.Run("Directory", testRecordCompletedProducerDirectory)
	t.Run("File", testRecordCompletedProducerFile)
	t.Run("existing completed kinds attach", testRecordCompletedProducerExistingKindsAttach)
}

func testRecordCompletedProducerDirectory(t *testing.T) {
	for _, invalid := range []string{"", "nil value", "nil producer", "typed nil producer", "pending", "recorded", "kind", "json", "path accessor", "snapshot accessor", "path", "snapshot", "nil snapshot", "restore"} {
		t.Run(invalid, func(t *testing.T) {
			dir := containerPersistenceTestDirectory("ready", "/selected")
			producer := Lazy[*Directory](&DirectorySubdirectoryLazy{LazyState: NewLazyState()})
			switch invalid {
			case "nil value":
				dir = nil
			case "nil producer":
				producer = nil
			case "typed nil producer":
				producer = (*DirectorySubdirectoryLazy)(nil)
			case "pending":
				dir.Lazy = &DirectorySubdirectoryLazy{LazyState: NewLazyState()}
			case "recorded":
				dir.completedRecipe = &DirectorySubdirectoryLazy{LazyState: NewLazyState()}
			case "kind":
				dir.completedRecipeKind = "saved"
			case "json":
				dir.completedRecipeJSON = []byte(`{}`)
			case "path accessor":
				dir.Dir = nil
			case "snapshot accessor":
				dir.Snapshot = nil
			case "path":
				dir.Dir = new(LazyAccessor[string, *Directory])
			case "snapshot":
				dir.Snapshot = new(LazyAccessor[bkcache.ImmutableRef, *Directory])
			case "nil snapshot":
				dir.Snapshot.setValue(nil)
			case "restore":
				producer = &DirectoryRestoreLazy{LazyState: NewLazyState()}
			}
			var original Directory
			if dir != nil {
				original = *dir
			}
			err := RecordCompletedProducer(dir, producer)
			if invalid != "" {
				require.Error(t, err)
				if dir != nil {
					require.Equal(t, original, *dir)
				}
				return
			}
			require.NoError(t, err)
			require.Same(t, producer, dir.completedRecipe)
			require.Nil(t, dir.Lazy)
			require.Same(t, original.Dir, dir.Dir)
			require.Same(t, original.Snapshot, dir.Snapshot)
			require.Error(t, RecordCompletedProducer(dir, producer))
			require.Same(t, producer, dir.completedRecipe)
		})
	}
}

func testRecordCompletedProducerFile(t *testing.T) {
	for _, invalid := range []string{"", "nil value", "nil producer", "typed nil producer", "pending", "recorded", "kind", "json", "path accessor", "snapshot accessor", "path", "snapshot", "nil snapshot", "restore"} {
		t.Run(invalid, func(t *testing.T) {
			file := freshProducerFile()
			file.File.setValue("data")
			file.Snapshot.setValue(&cacheVolumeTestImmutableRef{})
			producer := Lazy[*File](&FileBlobLazy{LazyState: NewLazyState()})
			switch invalid {
			case "nil value":
				file = nil
			case "nil producer":
				producer = nil
			case "typed nil producer":
				producer = (*FileBlobLazy)(nil)
			case "pending":
				file.Lazy = &FileBlobLazy{LazyState: NewLazyState()}
			case "recorded":
				file.completedRecipe = &FileBlobLazy{LazyState: NewLazyState()}
			case "kind":
				file.completedRecipeKind = "saved"
			case "json":
				file.completedRecipeJSON = []byte(`{}`)
			case "path accessor":
				file.File = nil
			case "snapshot accessor":
				file.Snapshot = nil
			case "path":
				file.File = new(LazyAccessor[string, *File])
			case "snapshot":
				file.Snapshot = new(LazyAccessor[bkcache.ImmutableRef, *File])
			case "nil snapshot":
				file.Snapshot.setValue(nil)
			case "restore":
				producer = &FileRestoreLazy{LazyState: NewLazyState()}
			}
			var original File
			if file != nil {
				original = *file
			}
			err := RecordCompletedProducer(file, producer)
			if invalid != "" {
				require.Error(t, err)
				if file != nil {
					require.Equal(t, original, *file)
				}
				return
			}
			require.NoError(t, err)
			require.Same(t, producer, file.completedRecipe)
			require.Nil(t, file.Lazy)
			require.Same(t, original.File, file.File)
			require.Same(t, original.Snapshot, file.Snapshot)
			require.Error(t, RecordCompletedProducer(file, producer))
			require.Same(t, producer, file.completedRecipe)
		})
	}
}

func testRecordCompletedProducerExistingKindsAttach(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "completed-attach")
	ctx, cache, srv := env.open(t)
	parent := env.attach(t, ctx, cache, srv, "parent", containerPersistenceTestDirectory("parent", "/")).(dagql.ObjectResult[*Directory])
	dir := containerPersistenceTestDirectory("child", "/selected")
	directoryProducer := &DirectorySubdirectoryLazy{LazyState: NewLazyState(), Parent: parent}
	dir.completedRecipe = directoryProducer
	file := &File{completedRecipe: &FileSubfileLazy{LazyState: NewLazyState(), Parent: parent}}
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
	require.Same(t, directoryProducer, dir.completedRecipe)
}

func TestCompletedProducerAttachmentBeforePublication(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "detached-completion")
	ctx, cache, srv := env.open(t)
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "selected"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "selected", "data"), []byte("data"), 0644))
	source := containerPersistenceTestDirectory("source-tree", "/")
	source.Snapshot.setValue(&producerTreeRef{cacheVolumeTestImmutableRef: &cacheVolumeTestImmutableRef{id: "source-tree", snapshotID: "source-tree"}, root: root})
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
	require.Nil(t, dir.Lazy)
	require.IsType(t, &DirectorySubdirectoryLazy{}, dir.completedRecipe)
	require.Nil(t, file.Lazy)
	require.IsType(t, &FileSubfileLazy{}, file.completedRecipe)
}

func TestMoveProducedOutputs(t *testing.T) {
	platform := Platform{OS: "linux", Architecture: "arm64"}
	releases := 0
	src := containerPersistenceTestDirectory("owned", "/selected")
	src.Services = ServiceBindings{{Hostname: "saved"}}
	src.Snapshot.setValue(&cacheVolumeTestImmutableRef{release: func(context.Context) error { releases++; return nil }})
	dst := &Directory{Platform: platform, Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory])}
	dst.Dir.setValue("preseeded")
	require.NoError(t, moveProducedDirectory(dst, src))
	require.Equal(t, platform, dst.Platform)
	path, _ := dst.Dir.Peek()
	require.Equal(t, "/selected", path)
	require.NoError(t, src.OnRelease(t.Context()))
	require.Zero(t, releases)
	src.Services[0].Hostname = "changed"
	require.Equal(t, "saved", dst.Services[0].Hostname)
	require.Error(t, moveProducedDirectory(dst, src))
	require.NoError(t, dst.OnRelease(t.Context()))
	require.Equal(t, 1, releases)
	file := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File])}
	file.File.setValue("saved")
	file.Snapshot.setValue(&cacheVolumeTestImmutableRef{release: func(context.Context) error { releases++; return nil }})
	output := &File{Platform: platform, File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File])}
	require.NoError(t, moveProducedFile(output, file))
	require.NoError(t, file.OnRelease(t.Context()))
	require.Equal(t, 1, releases)
	require.NoError(t, output.OnRelease(t.Context()))
	require.Equal(t, 2, releases)
}
