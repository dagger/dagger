package schema

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
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

type rebuildTestContent struct{ provider content.InfoReaderProvider }

func (s rebuildTestContent) Provider(context.Context, dagql.PersistedPartOffer, *dagql.PartDemandState) content.InfoReaderProvider {
	return s.provider
}
func (rebuildTestContent) Available(dagql.PersistedPartOffer, time.Time) bool { return true }

// rebuildWithInputs exports value from a with only its inputs' blobs, merges
// it into b, and runs check against the loaded value while b can download
// those blobs.
func rebuildWithInputs(t *testing.T, a *transferTestEngine, actx context.Context, b *transferTestEngine, value dagql.AnyResult, inputs []dagql.AnyResult, check func(context.Context, dagql.AnyResult)) {
	t.Helper()
	selection := dagql.ValueSelection{Roots: []dagql.AnyResult{value}}
	for _, input := range inputs {
		selection.Outputs = append(selection.Outputs, dagql.SelectedValueOutput{Result: input, Address: dagql.PersistedPartAddress{Part: "snapshot"}})
	}
	require.NoError(t, a.cache.WithExportedValues(actx, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
		require.Len(t, exported.Chains.Entries, len(inputs))
		if len(inputs) > 0 {
			b.cache.SetPartContentSource(rebuildTestContent{&testutil.Provider{InfoReaderProvider: exported.Chains.Entries[0].Provider}})
		}
		bctx := dagql.ContextWithServer(b.session(t, "b1"), b.dag)
		reply, err := b.cache.MergeValues(bctx, dagql.CloudCacheID, exported.Bundle)
		require.NoError(t, err)
		require.Len(t, reply.Imported(), 1)
		loaded, err := b.cache.LoadResultByResultID(bctx, "b1-session", b.dag, reply.Imported()[0].ResultID)
		require.NoError(t, err)
		check(bctx, loaded)
		return nil
	}))
}

// rebuildTestGitRepo commits file.txt=base in a new repository and returns it
// as a Directory with no recipe: another engine can only download it.
func rebuildTestGitRepo(t *testing.T, e *transferTestEngine, ctx context.Context) dagql.ObjectResult[*core.Directory] {
	t.Helper()
	// Local repositories run some git commands in the process directory;
	// keep it outside this source checkout.
	t.Chdir(t.TempDir())
	ref, err := e.srv.manager.New(ctx, nil)
	require.NoError(t, err)
	mounted, err := ref.Mount(ctx, false)
	require.NoError(t, err)
	mounts, release, err := mounted.Mount()
	require.NoError(t, err)
	root := mounts[0].Source
	git := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", root, "-c", "user.name=Author", "-c", "user.email=author@example.com"}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("init", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("base\n"), 0o644))
	git("add", ".")
	git("commit", "-m", "base")
	if release != nil {
		require.NoError(t, release())
	}
	snapshot, err := ref.Commit(ctx)
	require.NoError(t, err)
	dir := &core.Directory{Platform: core.Platform{OS: "linux", Architecture: "amd64"}, Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory])}
	dir.SetPath("/")
	dir.SetSnapshot(snapshot)
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "rebuildTestGitRepo", Type: dagql.NewResultCallType(dir.Type())}
	result, err := e.cache.GetOrInitCall(ctx, "a1-session", e.dag, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewObjectResultForCall(dir, e.dag, frame)
	})
	require.NoError(t, err)
	return result.(dagql.ObjectResult[*core.Directory])
}

// rebuildTestHead reads HEAD of the Git storage in an evaluated Directory.
func rebuildTestHead(t *testing.T, value dagql.AnyResult) string {
	t.Helper()
	dir, ok := dagql.UnwrapAs[*core.Directory](value)
	require.True(t, ok)
	ref, ok := dir.Snapshot.Peek()
	require.True(t, ok)
	path, ok := dir.Dir.Peek()
	require.True(t, ok)
	out, err := exec.Command("git", "-C", filepath.Join(testutil.Root(t, ref), path), "rev-parse", "HEAD").CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.TrimSpace(string(out))
}

// GitRef.withCommit's Git storage is rebuilt on another engine when its blob is
// missing, with the same commit.
func TestGitCommitDirectoryRebuildsOnAnotherEngine(t *testing.T) {
	testutil.RequireNativeMount(t)
	salt := transferTestSalt(t)
	a, b := newTransferTestEngine(t, salt), newTransferTestEngine(t, salt)
	actx := dagql.ContextWithServer(a.session(t, "a1"), a.dag)
	repo := rebuildTestGitRepo(t, a, actx)
	var head dagql.ObjectResult[*core.GitRef]
	require.NoError(t, a.dag.Select(actx, repo, &head, dagql.Selector{Field: "asGit"}, dagql.Selector{Field: "head"}))
	var before dagql.ObjectResult[*core.Directory]
	require.NoError(t, a.dag.Select(actx, head, &before, dagql.Selector{Field: "tree", Args: []dagql.NamedInput{rebuildTestArg("discardGitDir", dagql.NewBoolean(true))}}))
	beforeID, err := before.ID()
	require.NoError(t, err)
	var changes dagql.ObjectResult[*core.Changeset]
	require.NoError(t, a.dag.Select(actx, before, &changes,
		dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{rebuildTestArg("path", dagql.NewString("file.txt")), rebuildTestArg("contents", dagql.NewString("edited\n"))}},
		dagql.Selector{Field: "changes", Args: []dagql.NamedInput{rebuildTestArg("from", dagql.NewID[*core.Directory](beforeID))}}))
	rawChangesID, err := changes.ID()
	require.NoError(t, err)
	changesID := dagql.NewID[*core.Changeset](rawChangesID)
	var committed dagql.ObjectResult[*core.Directory]
	require.NoError(t, a.dag.Select(actx, head, &committed, dagql.Selector{Field: "__withCommitDirectory", Args: []dagql.NamedInput{
		rebuildTestArg("changes", changesID),
		rebuildTestArg("message", dagql.NewString("edit")),
		rebuildTestArg("date", dagql.NewString("2026-01-02T03:04:05Z")),
		rebuildTestArg("authorName", dagql.NewString("Author")),
		rebuildTestArg("authorEmail", dagql.NewString("author@example.com")),
	}}))
	require.IsType(t, &core.DirectoryGitCommitLazy{}, committed.Self().Lazy)
	requireRebuildRoute(t, actx, a.cache, committed)
	require.NoError(t, a.cache.Evaluate(actx, committed))
	want := rebuildTestHead(t, committed)

	rebuildWithInputs(t, a, actx, b, committed, []dagql.AnyResult{repo}, func(bctx context.Context, loaded dagql.AnyResult) {
		require.NoError(t, b.cache.Evaluate(bctx, loaded))
		require.Equal(t, want, rebuildTestHead(t, loaded))
	})
}
