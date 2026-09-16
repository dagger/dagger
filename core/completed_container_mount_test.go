package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

func TestRecordCompletedContainerMountProducer(t *testing.T) {
	for _, file := range []bool{false, true} {
		t.Run(map[bool]string{false: "Directory", true: "File"}[file], func(t *testing.T) {
			ctx, store, cache, srv, _ := executionFixture(t)
			ref, _ := store.Build(t, nil, "selected/payload", "mount bytes")
			parent := attachTransferObject(t, ctx, cache, srv, "producer-execution", "parent", NewContainer(Platform{OS: "linux", Architecture: "amd64"}))
			dir := partTestDirectory(ref, "/selected")
			sourceDir := attachTransferObject(t, ctx, cache, srv, "producer-execution", "sourceDirectory", dir)
			freshFile := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]), Platform: dir.Platform}
			freshFile.SetPath("/selected/payload")
			fileRef, err := store.Manager.GetBySnapshotID(ctx, ref.SnapshotID())
			require.NoError(t, err)
			freshFile.SetSnapshot(fileRef)
			sourceFile := attachTransferObject(t, ctx, cache, srv, "producer-execution", "sourceFile", freshFile)
			makeValue := func() (*Container, Lazy[*Container], string) {
				ctr := NewContainer(dir.Platform)
				if file {
					_, err := ctr.WithMountedFile(ctx, parent, "/target", sourceFile, "", false)
					require.NoError(t, err)
					return ctr, &ContainerWithMountedFileLazy{LazyState: NewLazyState(), Parent: parent, Source: sourceFile, Target: "/target"}, "withMountedFile"
				}
				_, err := ctr.WithMountedDirectory(ctx, parent, "/target", sourceDir, "", true)
				require.NoError(t, err)
				return ctr, &ContainerWithMountedDirectoryLazy{LazyState: NewLazyState(), Parent: parent, Source: sourceDir, Target: "/target", Readonly: true}, "withMountedDirectory"
			}
			for _, mode := range []string{"nil", "typed-nil", "wrong-kind", "missing-parent", "missing-source", "missing-target", "pending", "imported", "encoded", "success"} {
				t.Run(mode, func(t *testing.T) {
					ctr, producer, field := makeValue()
					defer func() { require.NoError(t, ctr.OnRelease(context.WithoutCancel(ctx))) }()
					before := ctr.Mounts[0]
					switch mode {
					case "nil":
						producer = nil
					case "typed-nil":
						producer = (*ContainerWithMountedFileLazy)(nil)
					case "wrong-kind":
						producer = &ContainerBuiltinLazy{LazyState: NewLazyState()}
					case "missing-parent":
						if p, ok := producer.(*ContainerWithMountedFileLazy); ok {
							p.Parent = dagql.ObjectResult[*Container]{}
						} else {
							producer.(*ContainerWithMountedDirectoryLazy).Parent = dagql.ObjectResult[*Container]{}
						}
					case "missing-source":
						if p, ok := producer.(*ContainerWithMountedFileLazy); ok {
							p.Source = dagql.ObjectResult[*File]{}
						} else {
							producer.(*ContainerWithMountedDirectoryLazy).Source = dagql.ObjectResult[*Directory]{}
						}
					case "missing-target":
						if p, ok := producer.(*ContainerWithMountedFileLazy); ok {
							p.Target = "/absent"
						} else {
							producer.(*ContainerWithMountedDirectoryLazy).Target = "/absent"
						}
					case "pending":
						ctr.Lazy = producer
					case "imported":
						ctr.acquiredOutput.Store(&containerAcquiredOutput{})
					case "encoded":
						ctr.completedRecipeJSON = json.RawMessage(`{}`)
					}
					err := RecordCompletedContainerMountProducer(ctr, producer)
					if mode != "success" {
						require.Error(t, err)
						require.Nil(t, ctr.completedRecipe)
						require.Equal(t, before, ctr.Mounts[0])
						return
					}
					require.NoError(t, err)
					require.Same(t, producer, ctr.completedRecipe)
					require.Nil(t, ctr.Lazy)
					require.Equal(t, before, ctr.Mounts[0])
					require.Error(t, RecordCompletedContainerMountProducer(ctr, producer))
					// The synthetic frame has no receiver or args: attachment must own
					// both exact inputs through the completed-recipe fallback.
					attached := attachTransferObject(t, ctx, cache, srv, "producer-execution", field, ctr)
					record, err := cache.CapturePersistedRecord(ctx, attached)
					require.NoError(t, err)
					var payload persistedContainerPayload
					require.NoError(t, json.Unmarshal(record.Envelope.ObjectJSON, &payload))
					var saved persistedContainerWithMountedDirectoryLazy
					require.NoError(t, json.Unmarshal(payload.LazyJSON, &saved))
					require.Equal(t, "/target", saved.Target)
					require.Equal(t, !file, saved.Readonly)
					parentID, _ := cache.PersistedResultID(parent)
					sourceID, _ := cache.PersistedResultID(sourceDir)
					if file {
						sourceID, _ = cache.PersistedResultID(sourceFile)
					}
					require.Equal(t, parentID, saved.ParentResultID)
					require.Equal(t, sourceID, saved.SourceResultID)
					for _, row := range cache.DebugEGraphSnapshot().Results {
						if row.SharedResultID == record.ResultID {
							require.ElementsMatch(t, []uint64{parentID, sourceID}, row.ExplicitDeps)
						}
					}
					for range 2 {
						fresh, err := decodePersistedContainerRecipe(ctx, dagql.NewPersistDecodeContext(srv, record.ResultID, record.Call), record.Call, payload.LazyJSON)
						require.NoError(t, err)
						private := NewContainer(dir.Platform)
						private.Lazy = fresh
						require.NoError(t, private.runLazyGroup(ctx, fresh.(LazyContainerParts), ContainerLazyGroupMetadata))
						require.NoError(t, private.runLazyGroup(ctx, fresh.(LazyContainerParts), ContainerLazyGroupWrite))
						mount := private.mountAt("/target")
						require.NotNil(t, mount)
						require.Equal(t, !file, mount.Readonly)
						var snapshot bkcache.ImmutableRef
						if file {
							value, _ := mount.FileSource.Peek()
							snapshot, _ = value.Snapshot.Peek()
							path, _ := value.File.Peek()
							require.Equal(t, "/selected/payload", path)
						} else {
							value, _ := mount.DirectorySource.Peek()
							snapshot, _ = value.Snapshot.Peek()
							path, _ := value.Dir.Peek()
							require.Equal(t, "/selected", path)
						}
						testutil.CheckFile(t, snapshot, "selected/payload", "mount bytes")
						require.NoError(t, private.OnRelease(ctx))
					}
					// Cache now owns the accepted object; don't release its accessor here.
					ctr = &Container{}
				})
			}
		})
	}
	require.Error(t, RecordCompletedContainerMountProducer(nil, &ContainerWithMountedFileLazy{}))
}
