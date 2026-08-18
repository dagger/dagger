package core

// Coverage for Workspace.git.unmanaged: the pending workspace edits git cannot
// see at all — gitignored paths, and paths inside a nested repository. They are
// written to the local checkout by Workspace.export, but they never show up in
// Workspace.git.uncommitted and cannot be committed.
//
// The mechanism lives in core/git_local.go: LocalGitRepository.Cleaned builds
// the comparison baseline with `git clean -fd`, which removes neither ignored
// files (that needs -x) nor anything inside an untracked nested repo (-ff), so
// those paths are identical on both sides of the diff and cancel out. The
// unmanaged view is derived as a set difference instead:
//
//	unmanaged := paths(overlay changes) \ paths(git.uncommitted)

import (
	"context"
	"encoding/json"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// unmanagedIgnoredBase is a committed workspace whose .gitignore ignores *.out.
func unmanagedIgnoredBase(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return workspaceBase(t, c).
		WithNewFile(".gitignore", "*.out\n").
		WithNewFile("a.txt", "a1").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"})
}

// unmanagedNestedBase is a committed workspace containing a nested git
// repository at nested/, with its own commit — mirroring modules/editor.
func unmanagedNestedBase(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return workspaceBase(t, c).
		WithNewFile("a.txt", "a1").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"}).
		WithWorkdir("/work/nested").
		WithExec([]string{"git", "init"}).
		WithNewFile("/work/nested/inner.txt", "inner\n").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "nested initial"}).
		WithWorkdir("/work")
}

// unmanagedProbe is the shape of the three-view query below.
type unmanagedProbe struct {
	CurrentWorkspace struct {
		WithNewFile struct {
			Changes uncommittedChanges `json:"changes"`
			Git     struct {
				Uncommitted uncommittedChanges `json:"uncommitted"`
				Unmanaged   uncommittedChanges `json:"unmanaged"`
			} `json:"git"`
		} `json:"withNewFile"`
	} `json:"currentWorkspace"`
}

// queryUnmanagedProbe writes path through the overlay and reads all three
// views of the result: the overlay changeset (what export writes), git's
// uncommitted set (what status/diff/commit see), and the derived unmanaged set.
func queryUnmanagedProbe(ctx context.Context, t *testctx.T, base *dagger.Container, path string) unmanagedProbe {
	t.Helper()
	out, err := base.With(daggerQuery(`{
  currentWorkspace {
    withNewFile(path: "` + path + `", contents: "probe\n") {
      changes { isEmpty diffStats { path kind addedLines removedLines } }
      git {
        uncommitted { isEmpty diffStats { path kind addedLines removedLines } }
        unmanaged { isEmpty diffStats { path kind addedLines removedLines } }
      }
    }
  }
}`)).Stdout(ctx)
	require.NoError(t, err)
	var got unmanagedProbe
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	return got
}

// TestWorkspaceUnmanagedIgnoredFile covers the gitignored half: a pending edit
// to an ignored path is real (export writes it), invisible to git, and
// surfaced by unmanaged.
func (WorkspaceSuite) TestWorkspaceUnmanagedIgnoredFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := unmanagedIgnoredBase(t, c)
	got := queryUnmanagedProbe(ctx, t, base, "probe.out")
	ws := got.CurrentWorkspace.WithNewFile

	// The overlay — what export writes — has it as an addition...
	stat, ok := ws.Changes.find("probe.out")
	require.True(t, ok, "expected probe.out in %v", ws.Changes.DiffStats)
	require.Equal(t, "ADDED", stat.Kind)

	// ...git cannot see it at all...
	require.NotContains(t, ws.Git.Uncommitted.paths(), "probe.out")

	// ...and unmanaged is exactly that gap.
	require.False(t, ws.Git.Unmanaged.IsEmpty)
	require.Contains(t, ws.Git.Unmanaged.paths(), "probe.out")

	// Committing it explains itself instead of silently doing nothing.
	_, err := base.With(daggerQuery(`{
  currentWorkspace {
    withNewFile(path: "probe.out", contents: "probe\n") {
      withCommit(message: "probe", date: "` + commitTestDate + `", paths: ["probe.out"]) {
        git { head { commit } }
      }
    }
  }
}`)).Stdout(ctx)
	requireErrOut(t, err, "pending changes git cannot track")
}

// TestWorkspaceUnmanagedNestedRepoFile covers the nested-repository half: the
// outer repo's `git clean -fd` baseline never descends into an untracked
// nested repo, so the edit cancels out of uncommitted and only unmanaged
// reports it.
func (WorkspaceSuite) TestWorkspaceUnmanagedNestedRepoFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := unmanagedNestedBase(t, c)
	got := queryUnmanagedProbe(ctx, t, base, "nested/probe.txt")
	ws := got.CurrentWorkspace.WithNewFile

	stat, ok := ws.Changes.find("nested/probe.txt")
	require.True(t, ok, "expected nested/probe.txt in %v", ws.Changes.DiffStats)
	require.Equal(t, "ADDED", stat.Kind)

	require.NotContains(t, ws.Git.Uncommitted.paths(), "nested/probe.txt")

	require.False(t, ws.Git.Unmanaged.IsEmpty)
	require.Contains(t, ws.Git.Unmanaged.paths(), "nested/probe.txt")

	_, err := base.With(daggerQuery(`{
  currentWorkspace {
    withNewFile(path: "nested/probe.txt", contents: "probe\n") {
      withCommit(message: "probe", date: "` + commitTestDate + `", paths: ["nested/probe.txt"]) {
        git { head { commit } }
      }
    }
  }
}`)).Stdout(ctx)
	requireErrOut(t, err, "pending changes git cannot track")
}

// TestWorkspaceUnmanagedEditsStillExport is the load-bearing one: whatever the
// diff views say, saving the session writes these edits to the user's checkout.
// That is the whole reason they must be reported somewhere — before unmanaged
// existed they landed on disk without ever appearing in status, diff or a
// commit.
func (WorkspaceSuite) TestWorkspaceUnmanagedEditsStillExport(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		WithNewFile(".gitignore", "*.out\n").
		WithNewFile("a.txt", "a1").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"}).
		WithWorkdir("/work/nested").
		WithExec([]string{"git", "init"}).
		WithNewFile("/work/nested/inner.txt", "inner\n").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "nested initial"}).
		WithWorkdir("/work")

	saved := base.With(daggerQuery(`{
  currentWorkspace {
    withNewFile(path: "probe.out", contents: "ignored probe\n") {
      withNewFile(path: "nested/probe.txt", contents: "nested probe\n") {
        export
      }
    }
  }
}`))

	// Both edits are in the checkout...
	ignored, err := saved.WithExec([]string{"cat", "probe.out"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "ignored probe\n", ignored)
	nested, err := saved.WithExec([]string{"cat", "nested/probe.txt"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "nested probe\n", nested)

	// ...while the outer repo still has nothing committable for either: the
	// ignored file is invisible, and the nested repo is reported (if at all)
	// as an untracked directory, never as its contents.
	status := gitOut(ctx, t, saved, "status", "--porcelain")
	require.NotContains(t, status, "probe.out")
	require.NotContains(t, status, "nested/probe.txt")
}

// TestWorkspaceUnmanagedControls pins the boundaries of the derived view: an
// ordinary edit belongs to git and must not be double-reported, an untouched
// workspace has nothing unmanaged, and workspaces whose uncommitted set *is*
// the overlay report nothing at all.
func (WorkspaceSuite) TestWorkspaceUnmanagedControls(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("an ordinary edit stays in uncommitted and out of unmanaged", func(ctx context.Context, t *testctx.T) {
		got := queryUnmanagedProbe(ctx, t, unmanagedIgnoredBase(t, c), "tracked-probe.txt")
		ws := got.CurrentWorkspace.WithNewFile
		require.Contains(t, ws.Git.Uncommitted.paths(), "tracked-probe.txt")
		require.True(t, ws.Git.Unmanaged.IsEmpty,
			"a git-visible edit must not be double-reported: %v", ws.Git.Unmanaged.DiffStats)
	})

	t.Run("a clean workspace has nothing unmanaged", func(ctx context.Context, t *testctx.T) {
		out, err := unmanagedIgnoredBase(t, c).With(daggerQuery(`{
  currentWorkspace {
    git { unmanaged { isEmpty } }
  }
}`)).Stdout(ctx)
		require.NoError(t, err)
		var got struct {
			CurrentWorkspace struct {
				Git struct {
					Unmanaged struct {
						IsEmpty bool `json:"isEmpty"`
					} `json:"unmanaged"`
				} `json:"git"`
			} `json:"currentWorkspace"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &got))
		require.True(t, got.CurrentWorkspace.Git.Unmanaged.IsEmpty)
	})

	t.Run("a value workspace reports nothing unmanaged", func(ctx context.Context, t *testctx.T) {
		// For a synthetic (non-host-backed) workspace, uncommitted *is* the
		// overlay, so the set difference is empty by construction and the
		// edit must not be reported twice.
		dirID, err := unmanagedIgnoredBase(t, c).Directory("/work").ID(ctx)
		require.NoError(t, err)

		got, err := testutil.QueryWithClient[struct {
			Node struct {
				AsWorkspace struct {
					WithNewFile struct {
						Changes struct {
							IsEmpty bool `json:"isEmpty"`
						} `json:"changes"`
						Git struct {
							Unmanaged struct {
								IsEmpty bool `json:"isEmpty"`
							} `json:"unmanaged"`
						} `json:"git"`
					} `json:"withNewFile"`
				} `json:"asWorkspace"`
			} `json:"node"`
		}](c, t, `query ValueWorkspaceUnmanaged($dir: ID!) {
  node(id: $dir) {
    ... on Directory {
      asWorkspace {
        withNewFile(path: "probe.out", contents: "probe\n") {
          changes { isEmpty }
          git { unmanaged { isEmpty } }
        }
      }
    }
  }
}`, &testutil.QueryOptions{Variables: map[string]any{"dir": dirID}})
		require.NoError(t, err)
		staged := got.Node.AsWorkspace.WithNewFile
		require.False(t, staged.Changes.IsEmpty, "the overlay still carries the edit")
		require.True(t, staged.Git.Unmanaged.IsEmpty)
	})
}

// TestWorkspaceUnmanagedSurvivesStagedCommit checks the anchoring: unmanaged is
// computed from the staged-anchored overlay remainder, so staging a commit for
// an unrelated path leaves the unmanaged entry reported exactly once.
func (WorkspaceSuite) TestWorkspaceUnmanagedSurvivesStagedCommit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := unmanagedIgnoredBase(t, c).With(daggerQuery(`{
  currentWorkspace {
    withNewFile(path: "probe.out", contents: "probe\n") {
      withNewFile(path: "tracked.txt", contents: "tracked\n") {
        withCommit(message: "tracked", date: "` + commitTestDate + `", paths: ["tracked.txt"]) {
          git {
            uncommitted { isEmpty diffStats { path kind addedLines removedLines } }
            unmanaged { isEmpty diffStats { path kind addedLines removedLines } }
          }
        }
      }
    }
  }
}`)).Stdout(ctx)
	require.NoError(t, err)

	var got struct {
		CurrentWorkspace struct {
			WithNewFile struct {
				WithNewFile struct {
					WithCommit struct {
						Git struct {
							Uncommitted uncommittedChanges `json:"uncommitted"`
							Unmanaged   uncommittedChanges `json:"unmanaged"`
						} `json:"git"`
					} `json:"withCommit"`
				} `json:"withNewFile"`
			} `json:"withNewFile"`
		} `json:"currentWorkspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	git := got.CurrentWorkspace.WithNewFile.WithNewFile.WithCommit.Git

	require.NotContains(t, git.Uncommitted.paths(), "probe.out",
		"the ignored file is still invisible to git after a commit")

	var seen int
	for _, stat := range git.Unmanaged.DiffStats {
		if stat.Path == "probe.out" {
			seen++
		}
	}
	require.Equal(t, 1, seen, "expected exactly one probe.out entry in %v", git.Unmanaged.DiffStats)
}
