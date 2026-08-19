package core

// Coverage for Workspace.checkpoint: freezing a live client checkout into a
// portable, host-independent workspace.

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

func (WorkspaceSuite) TestWorkspaceCheckpointReplayableValuePassesThrough(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	var got struct {
		Directory struct {
			AsWorkspace struct {
				WithNewFile struct {
					Original string `json:"original"`
					Frozen   struct {
						ID string `json:"id"`
					} `json:"frozen"`
				} `json:"withNewFile"`
			} `json:"asWorkspace"`
		} `json:"directory"`
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{Query: `{
  directory {
    asWorkspace {
      withNewFile(path: "overlay.txt", contents: "portable") {
        original: id
        frozen: checkpoint { id }
      }
    }
  }
}`}, &dagger.Response{Data: &got}))
	original := got.Directory.AsWorkspace.WithNewFile.Original
	require.NotEmpty(t, original)
	require.Equal(t, original, got.Directory.AsWorkspace.WithNewFile.Frozen.ID)
}

func (WorkspaceSuite) TestWorkspaceCheckpointRejectsNonReplayableLeaf(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	var got any
	err := c.Do(ctx, &dagger.Request{
		Query: `query($path: String!) {
  host {
    directory(path: $path) {
      asWorkspace { checkpoint { id } }
    }
  }
}`,
		Variables: map[string]any{"path": t.TempDir()},
	}, &dagger.Response{Data: &got})
	require.ErrorContains(t, err, "workspace checkpoint source is not replayable: field directory at call")
	require.ErrorContains(t, err, "Reads the live host filesystem")
}

// Checkpoint capture is an owner-only host read. A module executes through a
// nested client, so it may checkpoint an already-portable value but must not
// capture the calling client's live checkout.
func (WorkspaceSuite) TestWorkspaceCheckpointRejectsNestedClientCapture(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := checkpointCheckoutBase(ctx, t, c).
		WithNewFile("/work/dagger.toml", `[env.dev.modules.editor]
source = "modules/editor"
`).
		WithNewFile("/work/modules/editor/dagger.json", `{"name":"editor","engineVersion":"v1.0.0","sdk":"dang"}`).
		WithNewFile("/work/modules/editor/main.dang", editorSourceWithDoc("Read a file from the worker workspace.")).
		WithNewFile("/work/modules/probe/dagger.json", `{"name":"probe","engineVersion":"v1.0.0","sdk":"dang"}`).
		WithNewFile("/work/modules/probe/main.dang", `type Probe {
  tools(source: Workspace!): String! {
    source.checkpoint.agents.compose.tools
  }
}
`)

	out, err := base.With(daggerExecFail("--silent", "--env=dev", "-m", "modules/probe", "call", "tools")).CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "workspace checkpoint capture is only available to the workspace's owning client")
}

// TestWorkspaceCheckpointIsAddressableAndCapturesLocalState verifies that a
// freshly captured checkpoint resolves to a complete, addressable value.
//
// The frozen workspace must be addressable: `dagger agent` binds it into the LLM
// it composes agents onto, which needs its ID, so the effectful checkpoint call
// has to hand back the public composition's result rather than mint one of its
// own (an uncacheable call's own results have no ID — the regression this
// asserts against reported "result *core.Workspace is detached").
func (WorkspaceSuite) TestWorkspaceCheckpointIsAddressableAndCapturesLocalState(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := checkpointCheckoutBase(ctx, t, c)

	localCommit := gitOut(ctx, t, base, "rev-parse", "HEAD")

	captured := base.With(daggerQuery(`{
  currentWorkspace {
    checkpoint(include: ["tracked.txt"]) {
      id
      git {
        head { commit }
        uncommitted { modifiedPaths }
      }
    }
  }
}`))

	out, err := captured.Stdout(ctx)
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
}
