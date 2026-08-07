package core

// Coverage for WorkspaceGit.push: pushing the workspace's git HEAD — staged
// commits included — to a remote, through the local checkout's own git,
// without ever moving the checkout itself.

import (
	"context"
	"encoding/json"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// withPushRemote adds a bare repository at /remote.git, wired up as the
// checkout's "origin" remote.
func withPushRemote(ctr *dagger.Container) *dagger.Container {
	return ctr.
		WithExec([]string{"git", "init", "--bare", "/remote.git"}).
		WithExec([]string{"git", "remote", "add", "origin", "/remote.git"})
}

// TestWorkspacePushStagedCommits pushes a staged commit to the configured
// origin remote's current branch: the commit lands remotely while the local
// checkout — refs, HEAD, work tree — stays exactly as it was.
func (WorkspaceSuite) TestWorkspacePushStagedCommits(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := withPushRemote(withCommitBase(t, c)).
		WithNewFile("a.txt", "a2")

	baseHead := gitOut(ctx, t, base, "rev-parse", "HEAD")
	branch := gitOut(ctx, t, base, "symbolic-ref", "--short", "HEAD")

	pushed := base.With(daggerQuery(`{
  currentWorkspace {
    withCommit(message: "staged", date: "` + commitTestDate + `") {
      git {
        head { commit }
        push
      }
    }
  }
}`))

	out, err := pushed.Stdout(ctx)
	require.NoError(t, err)

	var got struct {
		CurrentWorkspace struct {
			WithCommit struct {
				Git struct {
					Head struct {
						Commit string `json:"commit"`
					} `json:"head"`
					Push string `json:"push"`
				} `json:"git"`
			} `json:"withCommit"`
		} `json:"currentWorkspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	staged := got.CurrentWorkspace.WithCommit.Git.Head.Commit
	require.Len(t, staged, 40)
	require.NotEqual(t, baseHead, staged)

	// The default destination is the checkout's current branch, reported as
	// the fully qualified ref that was updated.
	require.Equal(t, "refs/heads/"+branch, got.CurrentWorkspace.WithCommit.Git.Push)

	// The staged commit is the remote branch tip, with its metadata intact.
	require.Equal(t, staged, gitOut(ctx, t, pushed, "-C", "/remote.git", "rev-parse", "refs/heads/"+branch))
	require.Equal(t, "staged", gitOut(ctx, t, pushed, "-C", "/remote.git", "log", "-1", "--pretty=%s"))
	require.Equal(t, baseHead, gitOut(ctx, t, pushed, "-C", "/remote.git", "rev-parse", "refs/heads/"+branch+"~1"))

	// The local checkout is untouched: HEAD and branch unmoved, the edit
	// still dirty in the work tree, and no ref left behind for the commits
	// that travelled through.
	require.Equal(t, baseHead, gitOut(ctx, t, pushed, "rev-parse", "HEAD"))
	require.Equal(t, baseHead, gitOut(ctx, t, pushed, "rev-parse", "refs/heads/"+branch))
	require.Equal(t, " M a.txt", gitOut(ctx, t, pushed, "status", "--porcelain"))
	require.Equal(t, "", gitOut(ctx, t, pushed, "for-each-ref", "refs/dagger"))
}

// TestWorkspacePushToBranch pushes staged commits to a named remote branch —
// the "push a PR branch without touching the checkout" flow.
func (WorkspaceSuite) TestWorkspacePushToBranch(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := withPushRemote(withCommitBase(t, c)).
		WithNewFile("a.txt", "a2")

	baseHead := gitOut(ctx, t, base, "rev-parse", "HEAD")

	pushed := base.With(daggerQuery(`{
  currentWorkspace {
    withCommit(message: "staged", date: "` + commitTestDate + `") {
      git {
        head { commit }
        push(branch: "feature")
      }
    }
  }
}`))

	out, err := pushed.Stdout(ctx)
	require.NoError(t, err)

	var got struct {
		CurrentWorkspace struct {
			WithCommit struct {
				Git struct {
					Head struct {
						Commit string `json:"commit"`
					} `json:"head"`
					Push string `json:"push"`
				} `json:"git"`
			} `json:"withCommit"`
		} `json:"currentWorkspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	staged := got.CurrentWorkspace.WithCommit.Git.Head.Commit
	require.Equal(t, "refs/heads/feature", got.CurrentWorkspace.WithCommit.Git.Push)

	// The named branch exists on the remote, at the staged commit...
	require.Equal(t, staged, gitOut(ctx, t, pushed, "-C", "/remote.git", "rev-parse", "refs/heads/feature"))

	// ...and only on the remote: the checkout gained no such branch.
	require.Equal(t, "", gitOut(ctx, t, pushed, "for-each-ref", "refs/heads/feature"))
	require.Equal(t, baseHead, gitOut(ctx, t, pushed, "rev-parse", "HEAD"))
}

// TestWorkspacePushHead pushes with nothing staged: the checkout's own HEAD
// is what lands on the remote.
func (WorkspaceSuite) TestWorkspacePushHead(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := withPushRemote(withCommitBase(t, c))
	head := gitOut(ctx, t, base, "rev-parse", "HEAD")
	branch := gitOut(ctx, t, base, "symbolic-ref", "--short", "HEAD")

	pushed := base.With(daggerQuery(`{
  currentWorkspace {
    git { push }
  }
}`))
	out, err := pushed.Stdout(ctx)
	require.NoError(t, err)

	var got struct {
		CurrentWorkspace struct {
			Git struct {
				Push string `json:"push"`
			} `json:"git"`
		} `json:"currentWorkspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Equal(t, "refs/heads/"+branch, got.CurrentWorkspace.Git.Push)
	require.Equal(t, head, gitOut(ctx, t, pushed, "-C", "/remote.git", "rev-parse", "refs/heads/"+branch))
}

// TestWorkspacePushNonFastForward: a push that would rewind the remote branch
// is refused with git's own diagnostics, and force pushes through it.
func (WorkspaceSuite) TestWorkspacePushNonFastForward(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// The remote's "feature" branch holds a commit the workspace's history
	// does not contain.
	base := withPushRemote(withCommitBase(t, c)).
		WithWorkdir("/seed").
		WithExec([]string{"git", "init", "-q"}).
		WithNewFile("/seed/seed.txt", "s1").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "divergent"}).
		WithExec([]string{"git", "push", "/remote.git", "HEAD:refs/heads/feature"}).
		WithWorkdir("/work").
		WithNewFile("a.txt", "a2")

	divergent := gitOut(ctx, t, base, "-C", "/remote.git", "rev-parse", "refs/heads/feature")

	_, err := base.With(daggerQuery(`{
  currentWorkspace {
    withCommit(message: "staged", date: "` + commitTestDate + `") {
      git { push(branch: "feature") }
    }
  }
}`)).Stdout(ctx)
	requireErrOut(t, err, "cannot push")

	// Sanity: the remote's branch really is the divergent commit (the
	// handler-level tests pin that a refused push leaves the remote alone).
	require.Equal(t, divergent, gitOut(ctx, t, base, "-C", "/remote.git", "rev-parse", "refs/heads/feature"))

	forced := base.With(daggerQuery(`{
  currentWorkspace {
    withCommit(message: "staged", date: "` + commitTestDate + `") {
      git {
        head { commit }
        push(branch: "feature", force: true)
      }
    }
  }
}`))
	out, err := forced.Stdout(ctx)
	require.NoError(t, err)

	var got struct {
		CurrentWorkspace struct {
			WithCommit struct {
				Git struct {
					Head struct {
						Commit string `json:"commit"`
					} `json:"head"`
					Push string `json:"push"`
				} `json:"git"`
			} `json:"withCommit"`
		} `json:"currentWorkspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Equal(t, "refs/heads/feature",
		got.CurrentWorkspace.WithCommit.Git.Push)
	require.Equal(t, got.CurrentWorkspace.WithCommit.Git.Head.Commit,
		gitOut(ctx, t, forced, "-C", "/remote.git", "rev-parse", "refs/heads/feature"))
}

// TestWorkspacePushErrors covers the pushes that must be refused with a clear
// message before anything reaches a remote.
func (WorkspaceSuite) TestWorkspacePushErrors(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("no such remote", func(ctx context.Context, t *testctx.T) {
		// No origin is configured, so the default remote cannot resolve; git
		// itself explains that.
		_, err := withCommitBase(t, c).With(daggerQuery(`{
  currentWorkspace {
    git { push }
  }
}`)).Stdout(ctx)
		requireErrOut(t, err, "origin")
	})

	t.Run("detached HEAD needs a branch", func(ctx context.Context, t *testctx.T) {
		base := withPushRemote(withCommitBase(t, c)).
			WithExec([]string{"git", "checkout", "--detach"})

		_, err := base.With(daggerQuery(`{
  currentWorkspace {
    git { push }
  }
}`)).Stdout(ctx)
		requireErrOut(t, err, "detached")

		// Naming the branch makes the same push work.
		head := gitOut(ctx, t, base, "rev-parse", "HEAD")
		pushed := base.With(daggerQuery(`{
  currentWorkspace {
    git { push(branch: "from-detached") }
  }
}`))
		_, err = pushed.Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, head, gitOut(ctx, t, pushed, "-C", "/remote.git", "rev-parse", "refs/heads/from-detached"))
	})
}

// TestWorkspacePushThenSave pins that pushing does not consume or disturb the
// staged commit stack: the same commits can afterwards be saved to the local
// checkout, which then agrees with the remote.
func (WorkspaceSuite) TestWorkspacePushThenSave(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := withPushRemote(withCommitBase(t, c)).
		WithNewFile("a.txt", "a2")

	branch := gitOut(ctx, t, base, "symbolic-ref", "--short", "HEAD")

	saved := base.With(daggerQuery(`{
  currentWorkspace {
    withCommit(message: "staged", date: "` + commitTestDate + `") {
      git { push }
      export
    }
  }
}`))

	_, err := saved.Stdout(ctx)
	require.NoError(t, err)

	// The remote and the checkout ended up on the same commit.
	head := gitOut(ctx, t, saved, "rev-parse", "HEAD")
	require.Equal(t, head, gitOut(ctx, t, saved, "-C", "/remote.git", "rev-parse", "refs/heads/"+branch))
	require.Equal(t, "staged", gitOut(ctx, t, saved, "log", "-1", "--pretty=%s"))
	require.Equal(t, "", gitOut(ctx, t, saved, "status", "--porcelain"))
}
