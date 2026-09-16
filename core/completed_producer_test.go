package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/stretchr/testify/require"
)

func TestRecordCompletedProducer(t *testing.T) {
	t.Run("Directory", func(t *testing.T) {
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
	})
	t.Run("File", func(t *testing.T) {
		file := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File])}
		file.File.setValue("data")
		snapshot := &cacheVolumeTestImmutableRef{}
		file.Snapshot.setValue(snapshot)
		producer := &FileBlobLazy{LazyState: NewLazyState(), Filename: "data"}
		require.NoError(t, RecordCompletedProducer(file, producer))
		require.Same(t, producer, file.completedRecipe)
		require.Nil(t, file.Lazy)
		require.Error(t, RecordCompletedProducer(file, producer))
		require.Same(t, producer, file.completedRecipe)
		got, ok := file.Snapshot.Peek()
		require.True(t, ok)
		require.Same(t, snapshot, got)
	})
	t.Run("existing completed kinds attach", func(t *testing.T) {
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
	})
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
