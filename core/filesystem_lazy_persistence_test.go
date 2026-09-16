package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/stretchr/testify/require"
)

// This adapter lets the actual selection operations stat a temporary tree.
// Snapshot reopening and persistence still use the existing test manager.
type operationTreeRef struct {
	*cacheVolumeTestImmutableRef
	root string
}

type operationTreeMount string

func (r *operationTreeRef) Mount(context.Context, bool) (bkcache.MountableRef, error) {
	return operationTreeMount(r.root), nil
}

func (m operationTreeMount) Mount() ([]mount.Mount, func() error, error) {
	return []mount.Mount{{Type: "bind", Source: string(m)}}, func() error { return nil }, nil
}

func TestFilesystemEvaluatedLazyOperationPersistence(t *testing.T) {
	for _, kind := range []string{"Directory", "File", "Container.rootfs"} {
		t.Run(kind, func(t *testing.T) {
			env := newPersistedFamiliesTestEnv(t, "completed-filesystem")
			ctx, cache, srv := env.open(t)
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, "selected", "nested"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, "selected", "nested", "name.txt"), []byte("saved operation\n"), 0o644))
			source := containerPersistenceTestDirectory("operation-tree", "/selected")
			source.Snapshot.setValue(&operationTreeRef{
				cacheVolumeTestImmutableRef: &cacheVolumeTestImmutableRef{id: "operation-tree", snapshotID: "operation-tree"},
				root:                        root,
			})
			var parent, otherParent dagql.AnyResult
			var value dagql.Typed
			var wantKind, wantPath, wantInputPath string
			if kind == "Container.rootfs" {
				ctr := NewContainer(source.Platform)
				ctr.FS.setValue(source)
				parent = env.attach(t, ctx, cache, srv, "operation-parent", ctr)
				otherParent = env.attach(t, ctx, cache, srv, "operation-other", NewContainer(source.Platform))
				value = &Directory{
					Platform: source.Platform,
					Dir:      new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
					Lazy: &ContainerRootFSLazy{LazyState: NewLazyState(), Parent: parent.(dagql.ObjectResult[*Container])},
				}
				wantKind, wantPath = persistedDirectoryLazyKindContainerRootFS, "/selected"
			} else {
				parent = env.attach(t, ctx, cache, srv, "operation-parent", source)
				otherParent = env.attach(t, ctx, cache, srv, "operation-other", containerPersistenceTestDirectory("other-tree", "/other"))
				parentDir := parent.(dagql.ObjectResult[*Directory])
				if kind == "Directory" {
					var err error
					value, err = source.Subdirectory(ctx, parentDir, "nested")
					require.NoError(t, err)
					wantKind, wantPath, wantInputPath = persistedDirectoryLazyKindSubdirectory, "/selected/nested", "nested"
				} else {
					var err error
					value, err = source.Subfile(ctx, parentDir, "nested/name.txt")
					require.NoError(t, err)
					wantKind, wantPath, wantInputPath = persistedFileLazyKindDirectoryFile, "/selected/nested/name.txt", "nested/name.txt"
				}
			}
			parentID, otherID := persistedRowID(t, cache, parent), persistedRowID(t, cache, otherParent)
			child := env.attach(t, ctx, cache, srv, "operation-child", value)
			pending, err := cache.CapturePersistedRecord(ctx, child)
			require.NoError(t, err)
			require.True(t, dagql.HasPendingLazyEvaluation(child), "capture leaves the operation unstarted")
			var pendingPayload persistedDirectoryPayload // File has the same operation fields.
			require.NoError(t, json.Unmarshal(pending.Envelope.ObjectJSON, &pendingPayload))
			require.NoError(t, cache.Evaluate(ctx, child))
			path, err := storedSnapshotTestPath(ctx, child)
			require.NoError(t, err)
			require.Equal(t, wantPath, path)
			require.False(t, dagql.HasPendingLazyEvaluation(child))
			rec, err := cache.CapturePersistedRecord(ctx, child)
			require.NoError(t, err)
			var payload persistedDirectoryPayload
			require.NoError(t, json.Unmarshal(rec.Envelope.ObjectJSON, &payload))
			require.Equal(t, "snapshot", payload.Form)
			require.Equal(t, wantKind, payload.LazyKind)
			require.JSONEq(t, string(pendingPayload.LazyJSON), string(payload.LazyJSON))
			var operation struct {
				ParentResultID uint64 `json:"parentResultID"`
				Subdir         string `json:"subdir"`
				Path           string `json:"path"`
			}
			require.NoError(t, json.Unmarshal(payload.LazyJSON, &operation))
			require.Equal(t, parentID, operation.ParentResultID)
			require.Equal(t, wantInputPath, operation.Subdir+operation.Path)
			require.Equal(t, []dagql.PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "operation-tree"}}, rec.SnapshotLinks)

			if dir, ok := value.(*Directory); ok {
				derived, err := dir.Subdirectory(ctx, child.(dagql.ObjectResult[*Directory]), "another")
				require.NoError(t, err)
				require.False(t, derived.Lazy.IsEvaluated())
				require.Empty(t, derived.lazyJSON)
				encoded, err := derived.EncodePersistedObject(ctx, dagql.NewPersistEncodeContext(cache, 0, nil))
				require.NoError(t, err)
				var own persistedDirectoryPayload
				require.NoError(t, json.Unmarshal(encoded.JSON, &own))
				var op persistedDirectorySubdirectoryLazy
				require.NoError(t, json.Unmarshal(own.LazyJSON, &op))
				require.Equal(t, rec.ResultID, op.ParentResultID)
				require.Equal(t, "another", op.Subdir)
			}

			opens := env.manager.openCount("operation-tree")
			ctx, cache, srv = env.restart(t, ctx, cache)
			assertParentsUnloaded := func() {
				t.Helper()
				found := 0
				for _, row := range cache.DebugEGraphSnapshot().Results {
					if row.SharedResultID == parentID || row.SharedResultID == otherID {
						require.False(t, row.HasValue, "parent %d was decoded", row.SharedResultID)
						found++
					}
				}
				require.Equal(t, 2, found)
			}
			assertParentsUnloaded()
			loaded, err := cache.LoadResultByResultID(ctx, env.session, srv, rec.ResultID)
			require.NoError(t, err)
			path, err = storedSnapshotTestPath(ctx, loaded)
			require.NoError(t, err)
			require.Equal(t, wantPath, path)
			require.Equal(t, opens, env.manager.openCount("operation-tree"))
			assertParentsUnloaded()
			enc := dagql.NewPersistEncodeContext(cache, rec.ResultID, rec.Call)
			assertEncoding := func() {
				t.Helper()
				encoded, err := loaded.Unwrap().(dagql.PersistedObject).EncodePersistedObject(ctx, enc)
				require.NoError(t, err)
				require.JSONEq(t, string(rec.Envelope.ObjectJSON), string(encoded.JSON))
				assertParentsUnloaded()
			}
			assertEncoding()
			wantErr := errors.New("local snapshot unavailable")
			env.manager.beforeOpen = func(context.Context, string) error { return wantErr }
			require.ErrorIs(t, cache.Evaluate(ctx, loaded), wantErr)
			assertEncoding()
			env.manager.beforeOpen = nil
			require.NoError(t, cache.Evaluate(ctx, loaded))
			require.Equal(t, opens+2, env.manager.openCount("operation-tree"))
			require.False(t, dagql.HasPendingLazyEvaluation(loaded))
			assertEncoding()

			reloc := &relocationVisitor{mapping: map[uint64]uint64{rec.ResultID: rec.ResultID, parentID: otherID}}
			out, err := dagql.VisitEncodedReferences(rec, reloc.visit)
			require.NoError(t, err)
			require.Equal(t, map[string]uint64{"objectJSON.lazyJSON.parentResultID": parentID}, reloc.childIDs())
			var relocated persistedDirectoryPayload
			require.NoError(t, json.Unmarshal(out.Envelope.ObjectJSON, &relocated))
			require.NoError(t, json.Unmarshal(relocated.LazyJSON, &operation))
			require.Equal(t, otherID, operation.ParentResultID)
			codec := loaded.Unwrap().(dagql.PersistedObjectDecoder)
			decoded, err := codec.DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, out.ResultID, out.Call), out.Envelope.ObjectJSON)
			require.NoError(t, err)
			again, err := decoded.(dagql.PersistedObject).EncodePersistedObject(ctx, enc)
			require.NoError(t, err)
			require.JSONEq(t, string(out.Envelope.ObjectJSON), string(again.JSON))
			assertParentsUnloaded()

			// Preserve the type-specific path while removing only operation fields.
			var old map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(rec.Envelope.ObjectJSON, &old))
			delete(old, "lazyKind")
			delete(old, "lazyJSON")
			oldJSON, err := json.Marshal(old)
			require.NoError(t, err)
			decoded, err = codec.DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, rec.ResultID, rec.Call), oldJSON)
			require.NoError(t, err)
			again, err = decoded.(dagql.PersistedObject).EncodePersistedObject(ctx, enc)
			require.NoError(t, err)
			require.JSONEq(t, string(oldJSON), string(again.JSON))
			assertParentsUnloaded()
		})
	}
}
