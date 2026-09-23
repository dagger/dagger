package schema

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/engineutil"
	gitsession "github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type checkpointCaptureStream struct {
	grpc.ServerStream
	metadata *gitsession.CaptureGitMetadata
	bundle   []byte
}

func (*checkpointCaptureStream) Context() context.Context { return context.Background() }
func (s *checkpointCaptureStream) Send(response *gitsession.CaptureGitResponse) error {
	if metadata := response.GetMetadata(); metadata != nil {
		s.metadata = metadata
	}
	if chunk := response.GetChunk(); chunk != nil && chunk.Kind == gitsession.CAPTURE_CHUNK_BUNDLE {
		s.bundle = append(s.bundle, chunk.Data...)
	}
	return nil
}

type checkpointPackStream struct {
	grpc.ServerStream
	metadata *gitsession.PackCheckoutMetadata
	bundle   []byte
}

func (*checkpointPackStream) Context() context.Context { return context.Background() }
func (s *checkpointPackStream) Send(response *gitsession.PackCheckoutResponse) error {
	if metadata := response.GetMetadata(); metadata != nil {
		s.metadata = metadata
	}
	s.bundle = append(s.bundle, response.GetChunk()...)
	return nil
}

func checkpointTestGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Snapshot Test", "GIT_AUTHOR_EMAIL=snapshot@example.com", "GIT_COMMITTER_NAME=Snapshot Test", "GIT_COMMITTER_EMAIL=snapshot@example.com")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	return strings.TrimSpace(string(out))
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

// Use the actual client pack/capture implementations and schema resolvers, then
// replay the canonical recipe with independent caches and snapshot stores. No
// host resolver is installed in either server, so reconstruction cannot consult
// the source even accidentally.
func TestCheckpointLocalGitRecipeAfterSourceDisappears(t *testing.T) {
	testutil.RequireNativeMount(t)
	for _, remote := range []bool{false, true} {
		for _, detached := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{false: "no-remote", true: "local-remote"}[remote], map[bool]string{false: "branch", true: "detached"}[detached]}, "/"), func(t *testing.T) {
				source := t.TempDir()
				checkpointTestGit(t, source, "init", "-b", "main")
				require.NoError(t, os.WriteFile(filepath.Join(source, "tracked"), []byte("baseline\n"), 0o600))
				checkpointTestGit(t, source, "add", ".")
				checkpointTestGit(t, source, "commit", "-m", "initial history")
				base := checkpointTestGit(t, source, "rev-parse", "HEAD")
				origin := ""
				if remote {
					origin = t.TempDir()
					checkpointTestGit(t, origin, "init", "--bare")
					checkpointTestGit(t, source, "remote", "add", "origin", origin)
					checkpointTestGit(t, source, "push", "origin", "main")
				}
				checkpointTestGit(t, source, "commit", "--allow-empty", "-m", "local history")
				if detached {
					checkpointTestGit(t, source, "checkout", "--detach")
				}
				require.NoError(t, os.WriteFile(filepath.Join(source, "tracked"), []byte("staged\n"), 0o600))
				checkpointTestGit(t, source, "add", "tracked")
				require.NoError(t, os.WriteFile(filepath.Join(source, "tracked"), []byte("worktree\n"), 0o600))
				capture := new(checkpointCaptureStream)
				require.NoError(t, gitsession.GitAttachable{}.CaptureGit(&gitsession.CaptureGitRequest{CheckoutPath: source, Policy: &gitsession.CaptureGitPolicy{}}, capture))
				require.NotNil(t, capture.metadata)
				require.Nil(t, capture.metadata.Error)
				packed := new(checkpointPackStream)
				require.NoError(t, gitsession.GitAttachable{}.PackCheckout(&gitsession.PackCheckoutRequest{CheckoutPath: source, ExpectedStateDigest: capture.metadata.CheckoutStateDigest}, packed))
				require.Nil(t, packed.metadata.Error)
				pack := &engineutil.GitCheckoutPack{HeadSHA: packed.metadata.HeadSha, HeadRef: packed.metadata.HeadRef, ObjectFormat: packed.metadata.ObjectFormat, StateDigest: packed.metadata.StateDigest, BundlePath: filepath.Join(t.TempDir(), "history.bundle")}
				require.NoError(t, os.WriteFile(pack.BundlePath, packed.bundle, 0o600))
				ctx, srv := checkpointTestServer(t, "source")
				repo, err := checkpointLocalGitPackComposition(ctx, srv, capture.metadata, pack)
				require.NoError(t, err)
				ws := &core.Workspace{Cwd: "/", ConfigFile: "dagger.json", LockFile: "dagger.lock"}
				frozen, err := (&workspaceSchema{}).checkpointCapturedGitCompositionWithBase(ctx, srv, ws, capture.metadata, capture.bundle, "test-env", repo)
				require.NoError(t, err)
				id, err := frozen.RecipeID(ctx)
				require.NoError(t, err)
				recipe, err := id.Encode()
				require.NoError(t, err)
				require.NoError(t, os.RemoveAll(source))
				if origin != "" {
					require.NoError(t, os.RemoveAll(origin))
				}
				require.NoError(t, pack.Close())
				freshCtx, fresh := checkpointTestServer(t, "restore")
				var restoredID dagql.ID[*core.Workspace]
				require.NoError(t, restoredID.Decode(recipe))
				restored, err := restoredID.Load(freshCtx, fresh)
				require.NoError(t, err)
				require.Equal(t, "dagger.json", restored.Self().ConfigFile)
				var content dagql.String
				require.NoError(t, fresh.Select(freshCtx, restored, &content,
					dagql.Selector{Field: "file", Args: []dagql.NamedInput{{Name: "path", Value: dagql.NewString("/tracked")}}},
					dagql.Selector{Field: "contents"}))
				require.Equal(t, "worktree\n", string(content))
				src := restored.Self().BaseSource().(*core.WorkspaceSourceGitRef)
				var history dagql.ObjectResult[*core.File]
				require.NoError(t, fresh.Select(freshCtx, src.Ref.Self().Repo, &history,
					dagql.Selector{Field: "ref", Args: []dagql.NamedInput{{Name: "name", Value: dagql.NewString(base)}}},
					dagql.Selector{Field: "tree"},
					dagql.Selector{Field: "file", Args: []dagql.NamedInput{{Name: "path", Value: dagql.NewString("tracked")}}}))
				bytes, err := history.Self().Contents(freshCtx, history, nil, nil)
				require.NoError(t, err)
				require.Equal(t, "baseline\n", string(bytes))
			})
		}
	}
}
