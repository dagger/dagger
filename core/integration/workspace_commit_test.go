package core

// Coverage for Workspace.withCommit: staging a git commit engine-side, without
// touching the local checkout.

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// commitTestDate is a fixed author/committer date. withCommit requires one, so
// that a commit's hash never depends on a hidden clock.
const commitTestDate = "2024-01-02T03:04:05Z"

// withCommitBase is a workspace with one commit and two dirty tracked files.
func withCommitBase(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return workspaceBase(t, c).
		WithNewFile("a.txt", "a1").
		WithNewFile("b.txt", "b1").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"})
}

type workspaceCommitGitSnapshot struct {
	Head struct {
		Commit string `json:"commit"`
	} `json:"head"`
	Uncommitted struct {
		IsEmpty bool `json:"isEmpty"`
	} `json:"uncommitted"`
}

type withCommitResult struct {
	CurrentWorkspace struct {
		WithCommit struct {
			Git workspaceCommitGitSnapshot `json:"git"`
		} `json:"withCommit"`
	} `json:"currentWorkspace"`
}

type chainedCommitResult struct {
	CurrentWorkspace struct {
		WithCommit struct {
			WithCommit struct {
				Git workspaceCommitGitSnapshot `json:"git"`
			} `json:"withCommit"`
		} `json:"withCommit"`
	} `json:"currentWorkspace"`
}

// TestWorkspaceWithCommit stages every uncommitted change as a commit: HEAD
// advances to it and nothing is left pending.
func (WorkspaceSuite) TestWorkspaceWithCommit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := withCommitBase(t, c).
		WithNewFile("a.txt", "a2").
		WithNewFile("new.txt", "new")

	baseHead, err := base.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	baseHead = strings.TrimSpace(baseHead)

	out, err := base.With(daggerQuery(`{
  currentWorkspace {
    withCommit(message: "staged", date: "` + commitTestDate + `") {
      git {
        head { commit }
        uncommitted { isEmpty }
      }
    }
  }
}`)).Stdout(ctx)
	require.NoError(t, err)

	var got withCommitResult
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	staged := got.CurrentWorkspace.WithCommit.Git.Head.Commit
	require.Len(t, staged, 40)
	require.NotEqual(t, baseHead, staged)
	require.True(t, got.CurrentWorkspace.WithCommit.Git.Uncommitted.IsEmpty)

	// The local checkout is untouched: the commit only exists engine-side.
	localHead, err := base.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, baseHead, strings.TrimSpace(localHead))
}

// TestWorkspaceWithCommitIsDeterministic locks in the hermeticity guarantee:
// the same workspace state committed with the same arguments, in two separate
// sessions, produces the same commit hash.
func (WorkspaceSuite) TestWorkspaceWithCommitIsDeterministic(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := withCommitBase(t, c).
		WithNewFile("a.txt", "a2")

	query := `{
  currentWorkspace {
    withCommit(message: "staged", date: "` + commitTestDate + `") {
      git { head { commit } uncommitted { isEmpty } }
    }
  }
}`

	first, err := base.With(daggerQuery(query)).Stdout(ctx)
	require.NoError(t, err)
	second, err := base.
		// Bust the exec cache so the query genuinely runs twice.
		WithEnvVariable("WITH_COMMIT_RUN", "2").
		With(daggerQuery(query)).
		Stdout(ctx)
	require.NoError(t, err)

	var gotFirst, gotSecond withCommitResult
	require.NoError(t, json.Unmarshal([]byte(first), &gotFirst))
	require.NoError(t, json.Unmarshal([]byte(second), &gotSecond))
	require.Equal(t,
		gotFirst.CurrentWorkspace.WithCommit.Git.Head.Commit,
		gotSecond.CurrentWorkspace.WithCommit.Git.Head.Commit)
	require.Len(t, gotFirst.CurrentWorkspace.WithCommit.Git.Head.Commit, 40)
}

// TestWorkspaceWithCommitPaths commits a subset of the uncommitted changes and
// leaves the rest pending on top, so a second commit can pick them up.
func (WorkspaceSuite) TestWorkspaceWithCommitPaths(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := withCommitBase(t, c).
		WithNewFile("a.txt", "a2").
		WithNewFile("b.txt", "b2")

	baseHead, err := base.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	baseHead = strings.TrimSpace(baseHead)

	out, err := base.With(daggerQuery(`{
  currentWorkspace {
    withCommit(message: "just a", date: "` + commitTestDate + `", paths: ["a.txt"]) {
      git {
        head { commit }
        uncommitted { isEmpty }
      }
    }
  }
}`)).Stdout(ctx)
	require.NoError(t, err)

	var scoped withCommitResult
	require.NoError(t, json.Unmarshal([]byte(out), &scoped))
	firstCommit := scoped.CurrentWorkspace.WithCommit.Git.Head.Commit
	require.NotEqual(t, baseHead, firstCommit)
	require.False(t, scoped.CurrentWorkspace.WithCommit.Git.Uncommitted.IsEmpty,
		"b.txt should still be pending")

	out, err = base.With(daggerQuery(`{
  currentWorkspace {
    withCommit(message: "just a", date: "` + commitTestDate + `", paths: ["a.txt"]) {
      withCommit(message: "then b", date: "` + commitTestDate + `") {
        git {
          head { commit }
          uncommitted { isEmpty }
        }
      }
    }
  }
}`)).Stdout(ctx)
	require.NoError(t, err)

	var chained chainedCommitResult
	require.NoError(t, json.Unmarshal([]byte(out), &chained))
	secondCommit := chained.CurrentWorkspace.WithCommit.WithCommit.Git.Head.Commit
	require.NotEqual(t, baseHead, secondCommit)
	require.NotEqual(t, firstCommit, secondCommit)
	require.True(t, chained.CurrentWorkspace.WithCommit.WithCommit.Git.Uncommitted.IsEmpty)
}

// TestWorkspaceWithCommitNothingToCommit rejects commits with no content,
// rather than staging an empty commit.
func (WorkspaceSuite) TestWorkspaceWithCommitNothingToCommit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := withCommitBase(t, c)

	t.Run("clean workspace", func(ctx context.Context, t *testctx.T) {
		_, err := base.With(daggerQuery(`{
  currentWorkspace {
    withCommit(message: "empty", date: "` + commitTestDate + `") {
      git { head { commit } }
    }
  }
}`)).Stdout(ctx)
		requireErrOut(t, err, "nothing to commit")
	})

	t.Run("paths match nothing", func(ctx context.Context, t *testctx.T) {
		_, err := base.
			WithNewFile("a.txt", "a2").
			With(daggerQuery(`{
  currentWorkspace {
    withCommit(message: "empty", date: "` + commitTestDate + `", paths: ["b.txt"]) {
      git { head { commit } }
    }
  }
}`)).Stdout(ctx)
		requireErrOut(t, err, "nothing to commit")
	})
}

// gitOut runs a git command in the container and returns its trimmed stdout.
func gitOut(ctx context.Context, t *testctx.T, ctr *dagger.Container, args ...string) string {
	t.Helper()
	out, err := ctr.WithExec(append([]string{"git"}, args...)).Stdout(ctx)
	require.NoError(t, err)
	return strings.TrimSpace(out)
}

// TestWorkspaceCommitExport saves a workspace holding one staged commit plus
// uncommitted remainder: the commit lands on the checked-out branch as a
// fast-forward, and only the remainder is left dirty in the work tree.
func (WorkspaceSuite) TestWorkspaceCommitExport(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := withCommitBase(t, c)
	baseHead := gitOut(ctx, t, base, "rev-parse", "HEAD")
	branch := gitOut(ctx, t, base, "symbolic-ref", "--short", "HEAD")

	saved := base.With(daggerQuery(`{
  currentWorkspace {
    withNewFile(path: "c.txt", contents: "c1") {
      withNewFile(path: "d.txt", contents: "d1") {
        withCommit(message: "staged c", date: "` + commitTestDate + `", paths: ["c.txt"]) {
          export
        }
      }
    }
  }
}`))

	// The staged commit is the branch tip, a direct child of the base HEAD.
	head := gitOut(ctx, t, saved, "rev-parse", "HEAD")
	require.NotEqual(t, baseHead, head)
	require.Equal(t, head, gitOut(ctx, t, saved, "rev-parse", "refs/heads/"+branch))
	require.Equal(t, baseHead, gitOut(ctx, t, saved, "rev-parse", "HEAD~1"))

	require.Equal(t, "staged c", gitOut(ctx, t, saved, "log", "-1", "--pretty=%s"))
	require.Equal(t, "Dagger Tests", gitOut(ctx, t, saved, "log", "-1", "--pretty=%an"))
	require.Equal(t, "dagger@example.com", gitOut(ctx, t, saved, "log", "-1", "--pretty=%ae"))
	wantDate, err := time.Parse(time.RFC3339, commitTestDate)
	require.NoError(t, err)
	require.Equal(t, strconv.FormatInt(wantDate.Unix(), 10), gitOut(ctx, t, saved, "log", "-1", "--pretty=%at"))

	// The commit's content is in the tree, and only the uncommitted remainder
	// is dirty.
	require.Equal(t, "c1", gitOut(ctx, t, saved, "show", "HEAD:c.txt"))
	require.Equal(t, "?? d.txt", gitOut(ctx, t, saved, "status", "--porcelain"))

	// Both files are in the work tree.
	contents, err := saved.File("c.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "c1", contents)
	contents, err = saved.File("d.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "d1", contents)
}

// TestWorkspaceCommitExportChain saves a stack of two staged commits: both land
// on the branch, in order, and the work tree comes out clean.
func (WorkspaceSuite) TestWorkspaceCommitExportChain(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := withCommitBase(t, c)
	baseHead := gitOut(ctx, t, base, "rev-parse", "HEAD")

	saved := base.With(daggerQuery(`{
  currentWorkspace {
    withNewFile(path: "c.txt", contents: "c1") {
      withNewFile(path: "d.txt", contents: "d1") {
        withCommit(message: "commit c", date: "` + commitTestDate + `", paths: ["c.txt"]) {
          withCommit(message: "commit d", date: "` + commitTestDate + `", paths: ["d.txt"]) {
            export
          }
        }
      }
    }
  }
}`))

	require.Equal(t, "commit d\ncommit c\ninitial",
		gitOut(ctx, t, saved, "log", "-3", "--pretty=%s"))
	require.Equal(t, baseHead, gitOut(ctx, t, saved, "rev-parse", "HEAD~2"))
	require.Equal(t, "", gitOut(ctx, t, saved, "status", "--porcelain"))
	require.Equal(t, "c1", gitOut(ctx, t, saved, "show", "HEAD:c.txt"))
	require.Equal(t, "d1", gitOut(ctx, t, saved, "show", "HEAD:d.txt"))
}

