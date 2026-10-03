package schema

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots/testutil"
)

// rebuildTestDirectory is directory().withNewFile(path, contents).
func rebuildTestDirectory(t *testing.T, e *transferTestEngine, ctx context.Context, path, contents string) dagql.ID[*core.Directory] {
	t.Helper()
	return rebuildTestID[*core.Directory](t, e, ctx, dagql.Selector{Field: "directory"}, dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
		rebuildTestArg("path", dagql.NewString(path)), rebuildTestArg("contents", dagql.NewString(contents)),
	}})
}

// rebuildTestChangeset is the changes from path=before to path=after.
func rebuildTestChangeset(t *testing.T, e *transferTestEngine, ctx context.Context, path, before, after string) dagql.ObjectResult[*core.Changeset] {
	t.Helper()
	beforeID := rebuildTestDirectory(t, e, ctx, path, before)
	var changes dagql.ObjectResult[*core.Changeset]
	require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &changes, dagql.Selector{Field: "directory"}, dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
		rebuildTestArg("path", dagql.NewString(path)), rebuildTestArg("contents", dagql.NewString(after)),
	}}, dagql.Selector{Field: "changes", Args: []dagql.NamedInput{rebuildTestArg("from", beforeID)}}))
	return changes
}

// requireRebuiltFile evaluates an imported value on b and returns the
// contents of name in its snapshot.
func requireRebuiltFile(t *testing.T, b *transferTestEngine, bctx context.Context, value dagql.AnyResult, name string) string {
	t.Helper()
	require.NoError(t, b.cache.Evaluate(bctx, value))
	var snapshot interface{ SnapshotID() string }
	root := ""
	switch v := value.Unwrap().(type) {
	case *core.File:
		ref, ok := v.Snapshot.Peek()
		require.True(t, ok)
		snapshot, root = ref, testutil.Root(t, ref)
		path, ok := v.File.Peek()
		require.True(t, ok)
		root = filepath.Join(root, filepath.Dir(path))
	case *core.Directory:
		ref, ok := v.Snapshot.Peek()
		require.True(t, ok)
		snapshot, root = ref, testutil.Root(t, ref)
		path, ok := v.Dir.Peek()
		require.True(t, ok)
		root = filepath.Join(root, path)
	default:
		t.Fatalf("unexpected value %T", v)
	}
	require.NotNil(t, snapshot)
	data, err := os.ReadFile(filepath.Join(root, name))
	require.NoError(t, err)
	return string(data)
}

// Changeset.asPatch is rebuilt on another engine when its blob is missing.
func TestChangesetPatchRebuildsOnAnotherEngine(t *testing.T) {
	salt := transferTestSalt(t)
	a, b := newTransferTestEngine(t, salt), newTransferTestEngine(t, salt)
	actx := dagql.ContextWithServer(a.session(t, "a1"), a.dag)
	changes := rebuildTestChangeset(t, a, actx, "data", "before\n", "after\n")
	var patch dagql.ObjectResult[*core.File]
	require.NoError(t, a.dag.Select(actx, changes, &patch, dagql.Selector{Field: "asPatch"}))
	require.IsType(t, &core.FileChangesetPatchLazy{}, patch.Self().Lazy)
	requireRebuildRoute(t, actx, a.cache, patch)

	testutil.RequireNativeMount(t)
	require.NoError(t, a.cache.Evaluate(actx, patch))
	bctx, loaded := importForRebuild(t, a, actx, b, patch)
	require.Contains(t, requireRebuiltFile(t, b, bctx, loaded, core.ChangesetPatchFilename), "+after")
}

// Changeset merges are rebuilt on another engine when their blob is missing.
func TestChangesetMergesRebuildOnAnotherEngine(t *testing.T) {
	for _, field := range []string{"__mergeWithChangeset", "__mergeWithChangesets", "__mergeForWorkspaceCommit"} {
		t.Run(field, func(t *testing.T) {
			salt := transferTestSalt(t)
			a, b := newTransferTestEngine(t, salt), newTransferTestEngine(t, salt)
			actx := dagql.ContextWithServer(a.session(t, "a1"), a.dag)
			ours := rebuildTestChangeset(t, a, actx, "ours", "0\n", "1\n")
			changeID := func(path string) dagql.ID[*core.Changeset] {
				id, err := rebuildTestChangeset(t, a, actx, path, "0\n", "1\n").ID()
				require.NoError(t, err)
				return dagql.NewID[*core.Changeset](id)
			}
			args := []dagql.NamedInput{rebuildTestArg("changes", changeID("theirs"))}
			want := []string{"ours", "theirs"}
			if field == "__mergeWithChangesets" {
				args = []dagql.NamedInput{rebuildTestArg("changes", dagql.ArrayInput[dagql.ID[*core.Changeset]]{changeID("theirs"), changeID("third")})}
				want = append(want, "third")
			}
			var merged dagql.ObjectResult[*core.Directory]
			require.NoError(t, a.dag.Select(actx, ours, &merged, dagql.Selector{Field: field, Args: args}))
			lazy, ok := merged.Self().Lazy.(*core.DirectoryMergeChangesetsLazy)
			require.True(t, ok, "%T", merged.Self().Lazy)
			require.Equal(t, field == "__mergeForWorkspaceCommit", lazy.Workspace)
			requireRebuildRoute(t, actx, a.cache, merged)

			testutil.RequireNativeMount(t)
			require.NoError(t, a.cache.Evaluate(actx, merged))
			bctx, loaded := importForRebuild(t, a, actx, b, merged)
			for _, path := range want {
				require.Equal(t, "1\n", requireRebuiltFile(t, b, bctx, loaded, path))
			}
		})
	}
}
