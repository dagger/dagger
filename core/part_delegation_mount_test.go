package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/stretchr/testify/require"
)

func TestPartDelegationMountRoles(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "shifted-role", true: "wrong-kind"}[mismatch], func(t *testing.T) {
			aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
			actx, a, asrv := transferCache(t, aStore, "", "a")
			bctx, b, bsrv := transferCache(t, bStore, "", "b")
			makeParent := func(store *testutil.Store) *Container {
				removed, _ := store.Build(t, nil, "old", "removed")
				kept, _ := store.Build(t, nil, "selected/kept", "mount bytes")
				ctr := NewContainer(Platform{OS: "linux", Architecture: "amd64"})
				dir := new(LazyAccessor[*Directory, *Container])
				dir.setValue(partTestDirectory(removed, "/"))
				file := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]), Platform: ctr.Platform}
				file.SetPath("/selected/kept")
				file.SetSnapshot(kept)
				accessor := new(LazyAccessor[*File, *Container])
				accessor.setValue(file)
				ctr.Mounts = ContainerMounts{{Target: "/removed", DirectorySource: dir}, {Target: "/kept", FileSource: accessor, Readonly: true}}
				return ctr
			}
			base := makeParent(aStore)
			parent := attachTransferObject(t, actx, a, asrv, "a", "mountRoleParent", base)
			attachTransferObject(t, bctx, b, bsrv, "b", "mountRoleParent", makeParent(bStore))
			child := NewContainer(base.Platform)
			var err error
			child.Mounts, err = CloneContainerMounts(actx, base.Mounts[1:])
			require.NoError(t, err)
			child.Config.WorkingDir = "/retained-child"
			result := attachDelegationChild(t, actx, a, asrv, "a", "withoutMount", parent, child)
			require.NoError(t, a.WithExportedValues(actx, dagql.ValueSelection{Roots: []dagql.AnyResult{result}}, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
				require.Empty(t, exported.Chains.Entries)
				if mismatch {
					var root *dagql.PersistedRecord
					for i := range exported.Bundle.Values {
						if exported.Bundle.Values[i].Record.Call.Field == "withoutMount" {
							root = &exported.Bundle.Values[i].Record
						}
					}
					require.NotNil(t, root)
					var payload persistedContainerPayload
					require.NoError(t, json.Unmarshal(root.Envelope.ObjectJSON, &payload))
					payload.Metadata.Value.Mounts[0].Kind = persistedContainerMountKindDirectory
					part := payload.Parts["mount:/kept"]
					part.ValueKind, part.Role = containerPartDirectory, "mount_dir:0"
					payload.Parts["mount:/kept"] = part
					root.Envelope.ObjectJSON, err = json.Marshal(payload)
					require.NoError(t, err)
				}
				imported, err := b.ImportValues(bctx, exported.Bundle)
				require.NoError(t, err)
				loaded, err := b.LoadResultByResultID(bctx, "", bsrv, imported[0].ResultID)
				require.NoError(t, err)
				got := loaded.(dagql.ObjectResult[*Container])
				err = b.EvaluateParts(bctx, got, ContainerPartMount("/kept"))
				if mismatch {
					require.ErrorContains(t, err, "incompatible parent part")
					return nil
				}
				require.NoError(t, err)
				require.Len(t, got.Self().Mounts, 1)
				require.Equal(t, "/kept", got.Self().Mounts[0].Target)
				require.True(t, got.Self().Mounts[0].Readonly)
				require.Equal(t, "/retained-child", got.Self().Config.WorkingDir)
				file, ok := got.Self().Mounts[0].FileSource.Peek()
				require.True(t, ok)
				path, ok := file.File.Peek()
				require.True(t, ok)
				require.Equal(t, "/selected/kept", path)
				ref, ok := file.Snapshot.Peek()
				require.True(t, ok)
				testutil.CheckFile(t, ref, "selected/kept", "mount bytes")
				record, err := b.CapturePersistedRecord(bctx, got)
				require.NoError(t, err)
				require.Equal(t, []dagql.PersistedSnapshotRefLink{{Role: "mount_file:0", RefKey: ref.SnapshotID()}}, record.SnapshotLinks)
				require.Error(t, b.EvaluateParts(bctx, got, ContainerPartMount("/removed")))
				// Forward the closure without local roles. C derives the mapping
				// from its relocated receiver and independently installs the mount.
				cStore := testutil.NewStore(t)
				cctx, c, csrv := transferCache(t, cStore, "", "c")
				for _, field := range []string{"paddingOne", "paddingTwo", "paddingThree"} {
					attachTransferObject(t, cctx, c, csrv, "c", field, NewContainer(base.Platform))
				}
				attachTransferObject(t, cctx, c, csrv, "c", "mountRoleParent", makeParent(cStore))
				require.NoError(t, b.WithExportedValues(bctx, dagql.ValueSelection{Roots: []dagql.AnyResult{got}}, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, forwarded *dagql.ExportedValues) error {
					for _, value := range forwarded.Bundle.Values {
						require.Empty(t, value.Record.SnapshotLinks)
					}
					mapped, err := c.ImportValues(cctx, forwarded.Bundle)
					require.NoError(t, err)
					final, err := c.LoadResultByResultID(cctx, "", csrv, mapped[0].ResultID)
					require.NoError(t, err)
					finalCall, err := c.ResultCallByResultID(cctx, "", mapped[0].ResultID)
					require.NoError(t, err)
					require.NotEqual(t, record.Call.Receiver.ResultID, finalCall.Receiver.ResultID)
					require.NoError(t, c.EvaluateParts(cctx, final, ContainerPartMount("/kept")))
					captured, err := c.CapturePersistedRecord(cctx, final)
					require.NoError(t, err)
					require.Len(t, captured.SnapshotLinks, 1)
					require.Equal(t, "mount_file:0", captured.SnapshotLinks[0].Role)
					return nil
				}))
				return nil
			}))
		})
	}
}
