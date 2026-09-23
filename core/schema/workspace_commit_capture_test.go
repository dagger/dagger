package schema

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/engineutil"
	gitsession "github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

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
			for _, name := range []string{"selected", "pending"} {
				require.NoError(t, os.WriteFile(filepath.Join(source, name), []byte(name+"\n"), 0o644))
			}
			capture := new(checkpointCaptureStream)
			require.NoError(t, gitsession.GitAttachable{}.CaptureGit(&gitsession.CaptureGitRequest{CheckoutPath: source, Policy: &gitsession.CaptureGitPolicy{}}, capture))
			require.Nil(t, capture.metadata.Error)
			packed := new(checkpointPackStream)
			require.NoError(t, gitsession.GitAttachable{}.PackCheckout(&gitsession.PackCheckoutRequest{CheckoutPath: source, ExpectedStateDigest: capture.metadata.CheckoutStateDigest}, packed))
			require.Nil(t, packed.metadata.Error)
			pack := &engineutil.GitCheckoutPack{HeadSHA: packed.metadata.HeadSha, ObjectFormat: packed.metadata.ObjectFormat, StateDigest: packed.metadata.StateDigest, BundlePath: filepath.Join(t.TempDir(), "history.bundle")}
			require.NoError(t, os.WriteFile(pack.BundlePath, packed.bundle, 0o600))
			ctx, srv := checkpointTestServer(t, "commit-source")
			repo, err := checkpointLocalGitPackComposition(ctx, srv, capture.metadata, pack)
			require.NoError(t, err)
			ws, err := (&workspaceSchema{}).checkpointCapturedGitCompositionWithBase(ctx, srv, &core.Workspace{Cwd: "/"}, capture.metadata, capture.bundle, "", repo)
			require.NoError(t, err)
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
			require.NoError(t, pack.Close())
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
