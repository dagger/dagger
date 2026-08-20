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

// TestWorkspaceCheckpointIsAddressableAndExportsToTarget covers both halves of
// a freshly captured checkpoint.
//
// The frozen workspace must be addressable: `dagger agent` binds it into the LLM
// it composes agents onto, which needs its ID, so the effectful checkpoint call
// has to hand back the public composition's result rather than mint one of its
// own (an uncacheable call's own results have no ID — the regression this
// asserts against reported "result *core.Workspace is detached").
//
// It must also be savable to an explicit client-local target. Both a pending
// commit and a remaining overlay are exported from the frozen source; only the
// host checkout and client route come from currentWorkspace.
func (WorkspaceSuite) TestWorkspaceCheckpointIsAddressableAndExportsToTarget(ctx context.Context, t *testctx.T) {
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

	// A checkpoint is a value with no checkout of its own, even in its
	// originating session; export must not recover or guess the old route.
	rejected, err := base.
		With(daggerExecFail("--silent", "shell", "-c", `current-workspace | checkpoint --include=tracked.txt | export`)).
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, rejected, "workspace export requires an explicit target")

	// Export from the frozen value to the live checkout. The staged commit must
	// use the frozen source's BaseHeadSHA for reconciliation, while both it and
	// the remaining overlay must route host writes through the explicit target.
	saved := base.With(daggerShell(`current-workspace | checkpoint --include=tracked.txt | with-new-file committed.txt "committed" | with-commit "agent commit" ` + commitTestDate + ` --paths=committed.txt | with-new-file agent.txt "from agent" | export --to $(current-workspace)`))
	_, err = saved.Sync(ctx)
	require.NoError(t, err)

	committedFile, err := saved.File("/work/committed.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "committed", committedFile)

	// The explicit target received the remaining overlay too.
	agentFile, err := saved.File("/work/agent.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "from agent", agentFile)

	dirty, err := saved.File("/work/tracked.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "base\ndirty\n", dirty)
	require.NotEqual(t, localCommit, gitOut(ctx, t, saved, "rev-parse", "HEAD"))
	require.Equal(t, "agent commit", gitOut(ctx, t, saved, "log", "-1", "--format=%s"))
}
