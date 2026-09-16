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

func TestMountedLazyRepresentations(t *testing.T) {
	for _, file := range []bool{false, true} {
		t.Run(map[bool]string{false: "Directory", true: "File"}[file], func(t *testing.T) {
			ctx, store, cache, srv, _ := executionFixture(t)
			ref, _ := store.Build(t, nil, "selected/payload", "mount bytes")
			parent := attachTransferObject(t, ctx, cache, srv, "operation-execution", "parent", NewContainer(Platform{OS: "linux", Architecture: "amd64"}))
			dir := partTestDirectory(ref, "/selected")
			sourceDir := attachTransferObject(t, ctx, cache, srv, "operation-execution", "sourceDirectory", dir)
			freshFile := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]), Platform: dir.Platform}
			freshFile.SetPath("/selected/payload")
			fileRef, err := store.Manager.GetBySnapshotID(ctx, ref.SnapshotID())
			require.NoError(t, err)
			freshFile.SetSnapshot(fileRef)
			sourceFile := attachTransferObject(t, ctx, cache, srv, "operation-execution", "sourceFile", freshFile)
			makeValue := func() (*Container, Lazy[*Container], string) {
				ctr := NewContainer(dir.Platform)
				CopyContainerMetadata(ctr, parent.Self())
				if file {
					op := &ContainerWithMountedFileLazy{LazyState: NewLazyState(), Parent: parent, Source: sourceFile, Target: "/target"}
					ctr.Lazy = op
					ctr.Mounts = ctr.Mounts.With(ContainerMount{Target: "/target", FileSource: new(LazyAccessor[*File, *Container])})
					return ctr, op, "withMountedFile"
				}
				op := &ContainerWithMountedDirectoryLazy{LazyState: NewLazyState(), Parent: parent, Source: sourceDir, Target: "/target", Readonly: true}
				ctr.Lazy = op
				ctr.Mounts = ctr.Mounts.With(ContainerMount{Target: "/target", Readonly: true, DirectorySource: new(LazyAccessor[*Directory, *Container])})
				return ctr, op, "withMountedDirectory"
			}
			for _, mode := range []string{"pending", "evaluated"} {
				t.Run(mode, func(t *testing.T) {
					ctr, op, field := makeValue()
					defer func() { require.NoError(t, ctr.OnRelease(context.WithoutCancel(ctx))) }()
					if mode == "evaluated" {
						require.NoError(t, ctr.Evaluate(ctx))
					}
					require.Same(t, op, ctr.Lazy)
					// The synthetic frame has no receiver or arguments. Direct operation
					// inputs must keep the exact parent and source alive after publication.
					attached := attachTransferObject(t, ctx, cache, srv, "operation-execution", field, ctr)
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
}
