package core

// Coverage for Workspace.checkpoint: freezing a live client checkout into a
// portable, host-independent workspace, and saving an agent's work from that
// frozen workspace back to the checkout it was captured from.

import (
	"context"
	"encoding/json"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// checkpointCheckoutBase clones a remote-backed repository into /work with the
// CLI installed, then adds unpushed local history and tracked dirt on top: the
// shape capture is for, where the remote supplies the base objects and only the
// local commits and worktree delta have to travel.
func checkpointCheckoutBase(ctx context.Context, t *testctx.T, c *dagger.Client) *dagger.Container {
	t.Helper()
	gitDaemon, repoURL := gitService(ctx, t, c, c.Directory().WithNewFile("tracked.txt", "base\n"))
	return c.Container().From(golangImage).
		WithExec([]string{"apk", "add", "git"}).
		WithExec([]string{"git", "config", "--global", "user.email", "checkpoint@example.com"}).
		WithExec([]string{"git", "config", "--global", "user.name", "Checkpoint"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithServiceBinding("checkpoint-git", gitDaemon).
		WithExec([]string{"git", "clone", repoURL, "/work"}).
		WithWorkdir("/work").
		WithNewFile("/work/local.txt", "local\n").
		WithExec([]string{"git", "add", "local.txt"}).
		WithExec([]string{"git", "commit", "-m", "local commit"}).
		WithNewFile("/work/tracked.txt", "base\ndirty\n")
}

// TestWorkspaceCheckpointIsAddressableAndSavesToItsOrigin covers both halves of
// a freshly captured checkpoint.
//
// The frozen workspace must be an addressable value: `dagger agent` binds it
// into the LLM it composes agents onto, which needs its ID, so the effectful
// checkpoint call has to hand back the pure constructor's result rather than
// mint one of its own (an uncacheable call's own results have no ID — the
// regression this asserts against reported "result *core.Workspace is
// detached").
//
// It must also still be savable: the checkpoint has no checkout of its own, so
// an explicit save routes through the origin its capturing session retained,
// landing the agent's edits in the checkout the agent is working on behalf of
// while leaving that checkout's own dirt alone.
func (WorkspaceSuite) TestWorkspaceCheckpointIsAddressableAndSavesToItsOrigin(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := checkpointCheckoutBase(ctx, t, c)

	localCommit := gitOut(ctx, t, base, "rev-parse", "HEAD")

	saved := base.With(daggerQuery(`{
  currentWorkspace {
    checkpoint {
      id
      git {
        head { commit }
        uncommitted { modifiedPaths }
      }
      withNewFile(path: "agent.txt", contents: "from agent\n") {
        export
      }
    }
  }
}`))

	out, err := saved.Stdout(ctx)
	require.NoError(t, err)

	var got struct {
		CurrentWorkspace struct {
			Checkpoint struct {
				ID  string `json:"id"`
				Git struct {
					Head struct {
						Commit string `json:"commit"`
					} `json:"head"`
					Uncommitted struct {
						ModifiedPaths []string `json:"modifiedPaths"`
					} `json:"uncommitted"`
				} `json:"git"`
			} `json:"checkpoint"`
		} `json:"currentWorkspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	checkpoint := got.CurrentWorkspace.Checkpoint

	// Addressable: the checkpoint resolved to a value with an ID, which is what
	// an agent's composition binds.
	require.NotEmpty(t, checkpoint.ID)

	// Frozen, and complete: the captured tree carries the commit that exists
	// only in the local checkout, with the tracked dirt still uncommitted on
	// top of it.
	require.Equal(t, localCommit, strings.TrimSpace(checkpoint.Git.Head.Commit))
	require.Equal(t, []string{"tracked.txt"}, checkpoint.Git.Uncommitted.ModifiedPaths)

	// Saved back to the checkout it was captured from: the new file is on disk
	// there, and the checkout's own dirt is untouched.
	agentFile, err := saved.File("/work/agent.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "from agent\n", agentFile)

	dirty, err := saved.File("/work/tracked.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "base\ndirty\n", dirty)
	require.Equal(t, localCommit, gitOut(ctx, t, saved, "rev-parse", "HEAD"))
}
