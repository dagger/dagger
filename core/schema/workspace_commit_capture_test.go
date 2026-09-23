package schema

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

func checkpointTestGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Snapshot Test", "GIT_AUTHOR_EMAIL=snapshot@example.com", "GIT_COMMITTER_NAME=Snapshot Test", "GIT_COMMITTER_EMAIL=snapshot@example.com")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
}

func checkpointTestServer(t *testing.T, session string) (context.Context, *dagql.Server) {
	t.Helper()
	ctx, _, srv := scratchTestCache(t, testutil.NewStore(t), "", session)
	(&directorySchema{}).Install(srv)
	(&fileSchema{}).Install(srv)
	(&gitSchema{}).Install(srv)
	(&workspaceSchema{}).Install(srv)
	return ctx, srv
}

func TestWorkspaceCommitCapturesIncomingChanges(t *testing.T) {
	testutil.RequireNativeMount(t)
	for _, all := range []bool{false, true} {
		t.Run(map[bool]string{false: "partial", true: "all"}[all], func(t *testing.T) {
			source := t.TempDir()
			checkpointTestGit(t, source, "init", "-b", "main")
			for _, name := range []string{"selected", "pending"} {
				require.NoError(t, os.WriteFile(filepath.Join(source, name), []byte("base\n"), 0o644))
			}
			checkpointTestGit(t, source, "add", ".")
			checkpointTestGit(t, source, "commit", "-m", "baseline")
			ctx, srv := checkpointTestServer(t, "commit-source")
			// Build an explicit immutable fixture repository through ordinary
			// Directory composition. No production host-history capture is involved.
			var gitDir dagql.ObjectResult[*core.Directory]
			require.NoError(t, srv.Select(ctx, srv.Root(), &gitDir, dagql.Selector{Field: "directory"}))
			require.NoError(t, filepath.WalkDir(filepath.Join(source, ".git"), func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				rel, err := filepath.Rel(filepath.Join(source, ".git"), path)
				if err != nil {
					return err
				}
				var file dagql.ObjectResult[*core.File]
				if err := srv.Select(ctx, srv.Root(), &file, dagql.Selector{Field: "blob", Args: []dagql.NamedInput{
					{Name: "name", Value: dagql.NewString(entry.Name())}, {Name: "contents", Value: dagql.Bytes(data)},
				}}); err != nil {
					return err
				}
				id, err := file.ID()
				if err != nil {
					return err
				}
				return srv.Select(ctx, gitDir, &gitDir, dagql.Selector{Field: "withFile", Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.NewString(filepath.ToSlash(rel))}, {Name: "source", Value: dagql.NewID[*core.File](id)},
				}})
			}))
			var ws dagql.ObjectResult[*core.Workspace]
			require.NoError(t, srv.Select(ctx, gitDir, &ws, dagql.Selector{Field: "asGit"}, dagql.Selector{Field: "head"}, dagql.Selector{Field: "asWorkspace"}))
			for _, name := range []string{"selected", "pending"} {
				require.NoError(t, srv.Select(ctx, ws, &ws, dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.NewString(name)}, {Name: "contents", Value: dagql.NewString(name + "\n")},
				}}))
			}
			var base dagql.ObjectResult[*core.Directory]
			require.NoError(t, srv.Select(ctx, ws, &base,
				dagql.Selector{Field: "git"}, dagql.Selector{Field: "head"},
				dagql.Selector{Field: "tree", Args: []dagql.NamedInput{{Name: "discardGitDir", Value: dagql.NewBoolean(true)}}}))
			// This constructor represents a live directory dependency. It exists
			// only in the capturing server, never in the restoring server.
			dagql.Fields[*core.Query]{dagql.NodeFunc("__liveCommitBase", func(ctx context.Context, _ dagql.ObjectResult[*core.Query], _ struct{}) (dagql.ObjectResult[*core.Directory], error) {
				return dagql.NewObjectResultForCurrentCall(ctx, srv, base.Self())
			})}.Install(srv)
			var before, after dagql.ObjectResult[*core.Directory]
			require.NoError(t, srv.Select(ctx, srv.Root(), &before, dagql.Selector{Field: "__liveCommitBase"}))
			require.NoError(t, srv.Select(ctx, before, &after, dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString("selected")}, {Name: "contents", Value: dagql.NewString("selected\n")},
			}}))
			if all {
				require.NoError(t, srv.Select(ctx, after, &after, dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.NewString("pending")}, {Name: "contents", Value: dagql.NewString("pending\n")},
				}}))
			}
			beforeID, err := before.ID()
			require.NoError(t, err)
			var incoming dagql.ObjectResult[*core.Changeset]
			require.NoError(t, srv.Select(ctx, after, &incoming, dagql.Selector{Field: "changes", Args: []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](beforeID)}}}))
			incomingID, err := incoming.ID()
			require.NoError(t, err)
			var committed dagql.ObjectResult[*core.Workspace]
			require.NoError(t, srv.Select(ctx, ws, &committed, dagql.Selector{Field: "withCommit", Args: (workspaceWithCommitArgs{
				Changes: dagql.NewID[*core.Changeset](incomingID), Message: "captured commit", Date: "2026-09-05T12:00:00Z",
				AuthorName: dagql.Opt(dagql.NewString("Captured Author")), AuthorEmail: dagql.Opt(dagql.NewString("captured@example.com")),
			}).selectors()}))
			id, err := committed.RecipeID(ctx)
			require.NoError(t, err)
			proto, err := id.ToProto()
			require.NoError(t, err)
			for _, call := range proto.GetRecipe().CallsByDigest {
				require.NotEqual(t, "__liveCommitBase", call.Field)
			}
			require.NoError(t, os.RemoveAll(source))
			freshCtx, fresh := checkpointTestServer(t, "commit-restore")
			restored, err := dagql.NewID[*core.Workspace](id).Load(freshCtx, fresh)
			require.NoError(t, err)
			for _, name := range []string{"selected", "pending"} {
				var contents dagql.String
				require.NoError(t, fresh.Select(freshCtx, restored, &contents,
					dagql.Selector{Field: "file", Args: []dagql.NamedInput{{Name: "path", Value: dagql.NewString("/" + name)}}},
					dagql.Selector{Field: "contents"}))
				require.Equal(t, name+"\n", string(contents))
			}
			var pending, author dagql.String
			require.NoError(t, fresh.Select(freshCtx, restored, &pending,
				dagql.Selector{Field: "git"}, dagql.Selector{Field: "head"}, dagql.Selector{Field: "tree"},
				dagql.Selector{Field: "file", Args: []dagql.NamedInput{{Name: "path", Value: dagql.NewString("pending")}}}, dagql.Selector{Field: "contents"}))
			require.Equal(t, map[bool]string{false: "base\n", true: "pending\n"}[all], string(pending))
			require.NoError(t, fresh.Select(freshCtx, restored, &author,
				dagql.Selector{Field: "git"}, dagql.Selector{Field: "head"}, dagql.Selector{Field: "targetCommit"}, dagql.Selector{Field: "authorName"}))
			require.Equal(t, "Captured Author", string(author))
		})
	}
}
