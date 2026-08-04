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

// TestWorkspaceCommitFileVisibility covers the report's finding 1: content that
// is in a staged commit but not yet exported stayed visible to the diff views
// while vanishing from the tree — Workspace.file, Workspace.directory, and
// therefore every container mounting the workspace, which made checks re-run on
// pre-commit source. Staging a commit must not change what the tree contains.
func (WorkspaceSuite) TestWorkspaceCommitFileVisibility(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := withCommitBase(t, c)

	out, err := base.With(daggerQuery(`{
  currentWorkspace {
    withNewFile(path: "probe.txt", contents: "probe") {
      withCommit(message: "add probe", date: "` + commitTestDate + `") {
        file(path: "probe.txt") { contents }
        directory(path: ".") {
          entries
          file(path: "probe.txt") { contents }
        }
        git { uncommitted { isEmpty } }
      }
    }
  }
}`)).Stdout(ctx)
	require.NoError(t, err)

	var got struct {
		CurrentWorkspace struct {
			WithNewFile struct {
				WithCommit struct {
					File struct {
						Contents string `json:"contents"`
					} `json:"file"`
					Directory struct {
						Entries []string `json:"entries"`
						File    struct {
							Contents string `json:"contents"`
						} `json:"file"`
					} `json:"directory"`
					Git workspaceCommitGitSnapshot `json:"git"`
				} `json:"withCommit"`
			} `json:"withNewFile"`
		} `json:"currentWorkspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	staged := got.CurrentWorkspace.WithNewFile.WithCommit

	// The committed file is still readable...
	require.Equal(t, "probe", staged.File.Contents)
	// ...and still in the directory the toolchains mount into containers.
	require.Contains(t, staged.Directory.Entries, "probe.txt")
	require.Equal(t, "probe", staged.Directory.File.Contents)
	// ...while no longer counting as pending.
	require.True(t, staged.Git.Uncommitted.IsEmpty)
}

// TestWorkspaceCommitTreeInvariant covers the root of the report's finding 2:
// the commit tool echoed a reversed diff because the workspace tree changed
// across withCommit. Committing moves content between the staged and pending
// layers; the tree itself must be byte-identical before and after.
func (WorkspaceSuite) TestWorkspaceCommitTreeInvariant(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := withCommitBase(t, c)

	out, err := base.With(daggerQuery(`{
  currentWorkspace {
    withNewFile(path: "a.txt", contents: "a2") {
      withNewFile(path: "new.txt", contents: "new") {
        before: directory(path: ".") { digest }
        withCommit(message: "staged", date: "` + commitTestDate + `") {
          after: directory(path: ".") { digest }
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
					Before struct {
						Digest string `json:"digest"`
					} `json:"before"`
					WithCommit struct {
						After struct {
							Digest string `json:"digest"`
						} `json:"after"`
					} `json:"withCommit"`
				} `json:"withNewFile"`
			} `json:"withNewFile"`
		} `json:"currentWorkspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	snap := got.CurrentWorkspace.WithNewFile.WithNewFile
	require.NotEmpty(t, snap.Before.Digest)
	require.Equal(t, snap.Before.Digest, snap.WithCommit.After.Digest,
		"withCommit must not change the workspace tree")
}

// TestWorkspaceCommitNoResurrection is the report's finding 3, verbatim: commit
// a change to an already-tracked file, then make a *path-scoped* commit of
// something else. The first commit's content must not come back as uncommitted,
// and saving must land both commits with a clean work tree.
func (WorkspaceSuite) TestWorkspaceCommitNoResurrection(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := withCommitBase(t, c)
	baseHead := gitOut(ctx, t, base, "rev-parse", "HEAD")

	saved := base.With(daggerQuery(`{
  currentWorkspace {
    withNewFile(path: "a.txt", contents: "a2") {
      withCommit(message: "edit a", date: "` + commitTestDate + `") {
        first: git { uncommitted { isEmpty } }
        withNewFile(path: "probe.txt", contents: "probe") {
          withCommit(message: "add probe", date: "` + commitTestDate + `", paths: ["probe.txt"]) {
            second: git { uncommitted { isEmpty } }
            export
          }
        }
      }
    }
  }
}`))

	out, err := saved.Stdout(ctx)
	require.NoError(t, err)

	var got struct {
		CurrentWorkspace struct {
			WithNewFile struct {
				WithCommit struct {
					First struct {
						Uncommitted struct {
							IsEmpty bool `json:"isEmpty"`
						} `json:"uncommitted"`
					} `json:"first"`
					WithNewFile struct {
						WithCommit struct {
							Second struct {
								Uncommitted struct {
									IsEmpty bool `json:"isEmpty"`
								} `json:"uncommitted"`
							} `json:"second"`
						} `json:"withCommit"`
					} `json:"withNewFile"`
				} `json:"withCommit"`
			} `json:"withNewFile"`
		} `json:"currentWorkspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	first := got.CurrentWorkspace.WithNewFile.WithCommit
	require.True(t, first.First.Uncommitted.IsEmpty,
		"committing everything must leave nothing pending")
	require.True(t, first.WithNewFile.WithCommit.Second.Uncommitted.IsEmpty,
		"a path-scoped commit must not resurrect the earlier commit as pending")

	// Both commits landed, in order, and the work tree is clean: nothing was
	// written twice, nothing was left behind.
	require.Equal(t, "add probe\nedit a\ninitial",
		gitOut(ctx, t, saved, "log", "-3", "--pretty=%s"))
	require.Equal(t, baseHead, gitOut(ctx, t, saved, "rev-parse", "HEAD~2"))
	require.Equal(t, "", gitOut(ctx, t, saved, "status", "--porcelain"))

	contents, err := saved.File("a.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "a2", contents)
	contents, err = saved.File("probe.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "probe", contents)
}

// TestWorkspaceCommitScopedRemainder pins the other half of the same invariant:
// a path-scoped commit leaves every other change exactly where it was — in the
// tree, out of the staged commit, and therefore still uncommitted.
func (WorkspaceSuite) TestWorkspaceCommitScopedRemainder(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := withCommitBase(t, c)

	out, err := base.With(daggerQuery(`{
  currentWorkspace {
    withNewFile(path: "a.txt", contents: "a2") {
      withNewFile(path: "b.txt", contents: "b2") {
        withCommit(message: "just a", date: "` + commitTestDate + `", paths: ["a.txt"]) {
          committed: file(path: "a.txt") { contents }
          pending: file(path: "b.txt") { contents }
          git {
            uncommitted {
              isEmpty
              modifiedPaths
              addedPaths
            }
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
						Committed struct {
							Contents string `json:"contents"`
						} `json:"committed"`
						Pending struct {
							Contents string `json:"contents"`
						} `json:"pending"`
						Git struct {
							Uncommitted struct {
								IsEmpty       bool     `json:"isEmpty"`
								ModifiedPaths []string `json:"modifiedPaths"`
								AddedPaths    []string `json:"addedPaths"`
							} `json:"uncommitted"`
						} `json:"git"`
					} `json:"withCommit"`
				} `json:"withNewFile"`
			} `json:"withNewFile"`
		} `json:"currentWorkspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	staged := got.CurrentWorkspace.WithNewFile.WithNewFile.WithCommit

	// Both edits are still in the tree, committed or not.
	require.Equal(t, "a2", staged.Committed.Contents)
	require.Equal(t, "b2", staged.Pending.Contents)

	// ...and the remainder is exactly the change that was not committed.
	require.False(t, staged.Git.Uncommitted.IsEmpty)
	require.Equal(t, []string{"b.txt"}, staged.Git.Uncommitted.ModifiedPaths)
	require.Empty(t, staged.Git.Uncommitted.AddedPaths)
}

// TestWorkspaceStagedCommitsList covers the read side of the staged commit
// stack: WorkspaceGit.stagedCommits reports each engine-side commit, oldest
// first, with exactly what that commit folded in.
func (WorkspaceSuite) TestWorkspaceStagedCommitsList(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := withCommitBase(t, c)

	const secondCommitDate = "2024-02-03T04:05:06Z"

	out, err := base.With(daggerQuery(`{
  currentWorkspace {
    before: git { stagedCommits { sha } }
    withNewFile(path: "a.txt", contents: "a2") {
      withNewFile(path: "b.txt", contents: "b2") {
        withNewFile(path: "c.txt", contents: "c1") {
          withCommit(message: "first commit\n\nwith a body", date: "` + commitTestDate + `", paths: ["a.txt"], authorName: "Ada", authorEmail: "ada@example.com") {
            withCommit(message: "second commit", date: "` + secondCommitDate + `", paths: ["b.txt"], authorName: "Bob", authorEmail: "bob@example.com") {
              git {
                head { commit }
                uncommitted { addedPaths modifiedPaths }
                stagedCommits {
                  sha
                  message
                  date
                  authorName
                  authorEmail
                  changes { diffStats { path kind } }
                }
              }
            }
          }
        }
      }
    }
  }
}`)).Stdout(ctx)
	require.NoError(t, err)

	type stagedCommit struct {
		SHA         string `json:"sha"`
		Message     string `json:"message"`
		Date        string `json:"date"`
		AuthorName  string `json:"authorName"`
		AuthorEmail string `json:"authorEmail"`
		Changes     struct {
			DiffStats []struct {
				Path string `json:"path"`
				Kind string `json:"kind"`
			} `json:"diffStats"`
		} `json:"changes"`
	}
	var got struct {
		CurrentWorkspace struct {
			Before struct {
				StagedCommits []stagedCommit `json:"stagedCommits"`
			} `json:"before"`
			WithNewFile struct {
				WithNewFile struct {
					WithNewFile struct {
						WithCommit struct {
							WithCommit struct {
								Git struct {
									Head struct {
										Commit string `json:"commit"`
									} `json:"head"`
									Uncommitted   uncommittedPaths `json:"uncommitted"`
									StagedCommits []stagedCommit   `json:"stagedCommits"`
								} `json:"git"`
							} `json:"withCommit"`
						} `json:"withCommit"`
					} `json:"withNewFile"`
				} `json:"withNewFile"`
			} `json:"withNewFile"`
		} `json:"currentWorkspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	// Nothing staged yet: an empty list, not an error and not a null.
	require.Empty(t, got.CurrentWorkspace.Before.StagedCommits)

	git := got.CurrentWorkspace.WithNewFile.WithNewFile.WithNewFile.WithCommit.WithCommit.Git
	staged := git.StagedCommits
	require.Len(t, staged, 2)

	// Oldest first: the last entry is the staged HEAD.
	require.Len(t, staged[0].SHA, 40)
	require.Len(t, staged[1].SHA, 40)
	require.NotEqual(t, staged[0].SHA, staged[1].SHA)
	require.Equal(t, git.Head.Commit, staged[1].SHA)

	// Metadata is reported exactly as it was recorded.
	require.Equal(t, "first commit\n\nwith a body", staged[0].Message)
	require.Equal(t, commitTestDate, staged[0].Date)
	require.Equal(t, "Ada", staged[0].AuthorName)
	require.Equal(t, "ada@example.com", staged[0].AuthorEmail)
	require.Equal(t, "second commit", staged[1].Message)
	require.Equal(t, secondCommitDate, staged[1].Date)
	require.Equal(t, "Bob", staged[1].AuthorName)
	require.Equal(t, "bob@example.com", staged[1].AuthorEmail)

	// Each entry reports what that commit alone folded in: not the other
	// commit's change, and not the file that is still pending.
	firstPaths := map[string]string{}
	for _, stat := range staged[0].Changes.DiffStats {
		firstPaths[stat.Path] = stat.Kind
	}
	require.Equal(t, map[string]string{"a.txt": "MODIFIED"}, firstPaths)

	secondPaths := map[string]string{}
	for _, stat := range staged[1].Changes.DiffStats {
		secondPaths[stat.Path] = stat.Kind
	}
	require.Equal(t, map[string]string{"b.txt": "MODIFIED"}, secondPaths)

	// ...and c.txt is still uncommitted.
	require.Equal(t, []string{"c.txt"}, git.Uncommitted.AddedPaths)
	require.Empty(t, git.Uncommitted.ModifiedPaths)
}

// gitOut runs a git command in the container and returns its trimmed stdout.
func gitOut(ctx context.Context, t *testctx.T, ctr *dagger.Container, args ...string) string {
	t.Helper()
	out, err := ctr.WithExec(append([]string{"git"}, args...)).Stdout(ctx)
	require.NoError(t, err)
	return strings.TrimSpace(out)
}

// uncommittedPaths is the shape of a Workspace.git.uncommitted path summary.
type uncommittedPaths struct {
	IsEmpty       bool     `json:"isEmpty"`
	AddedPaths    []string `json:"addedPaths"`
	ModifiedPaths []string `json:"modifiedPaths"`
}

// TestWorkspaceCommitPreexistingChangesStayPending is the report's finding 1:
// a workspace that already carried uncommitted work when the agent arrived
// must keep reporting it. Editing an unrelated file creates an overlay, and
// the overlay used to become the *whole* answer to "what is uncommitted",
// silently dropping everything the checkout was already dirty with — so
// status/diff went blind exactly when the user had work in flight, and a
// commit with no paths quietly left it behind.
func (WorkspaceSuite) TestWorkspaceCommitPreexistingChangesStayPending(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// b.txt is dirty on disk before anything else happens, and the agent
	// never touches it.
	base := withCommitBase(t, c).WithNewFile("b.txt", "b2")

	t.Run("an unrelated edit keeps it pending", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerQuery(`{
  currentWorkspace {
    withNewFile(path: "new.txt", contents: "new") {
      git {
        uncommitted {
          isEmpty
          addedPaths
          modifiedPaths
        }
      }
    }
  }
}`)).Stdout(ctx)
		require.NoError(t, err)

		var got struct {
			CurrentWorkspace struct {
				WithNewFile struct {
					Git struct {
						Uncommitted uncommittedPaths `json:"uncommitted"`
					} `json:"git"`
				} `json:"withNewFile"`
			} `json:"currentWorkspace"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &got))
		pending := got.CurrentWorkspace.WithNewFile.Git.Uncommitted
		require.False(t, pending.IsEmpty)
		require.Equal(t, []string{"new.txt"}, pending.AddedPaths)
		require.Equal(t, []string{"b.txt"}, pending.ModifiedPaths,
			"the pre-existing dirty file must still be reported")
	})

	t.Run("an unscoped commit picks up both", func(ctx context.Context, t *testctx.T) {
		saved := base.With(daggerQuery(`{
  currentWorkspace {
    withNewFile(path: "new.txt", contents: "new") {
      withCommit(message: "everything", date: "` + commitTestDate + `") {
        git { uncommitted { isEmpty addedPaths modifiedPaths } }
        export
      }
    }
  }
}`))

		out, err := saved.Stdout(ctx)
		require.NoError(t, err)
		var got struct {
			CurrentWorkspace struct {
				WithNewFile struct {
					WithCommit struct {
						Git struct {
							Uncommitted uncommittedPaths `json:"uncommitted"`
						} `json:"git"`
					} `json:"withCommit"`
				} `json:"withNewFile"`
			} `json:"currentWorkspace"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &got))
		require.True(t, got.CurrentWorkspace.WithNewFile.WithCommit.Git.Uncommitted.IsEmpty,
			"committing everything must leave nothing pending")

		// Both layers landed in the one commit, and the checkout is clean.
		require.Equal(t, "b2", gitOut(ctx, t, saved, "show", "HEAD:b.txt"))
		require.Equal(t, "new", gitOut(ctx, t, saved, "show", "HEAD:new.txt"))
		require.Equal(t, "", gitOut(ctx, t, saved, "status", "--porcelain"))
	})

	t.Run("a scoped commit leaves it pending", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerQuery(`{
  currentWorkspace {
    withNewFile(path: "new.txt", contents: "new") {
      withCommit(message: "just the new file", date: "` + commitTestDate + `", paths: ["new.txt"]) {
        git { uncommitted { isEmpty addedPaths modifiedPaths } }
      }
    }
  }
}`)).Stdout(ctx)
		require.NoError(t, err)

		var got struct {
			CurrentWorkspace struct {
				WithNewFile struct {
					WithCommit struct {
						Git struct {
							Uncommitted uncommittedPaths `json:"uncommitted"`
						} `json:"git"`
					} `json:"withCommit"`
				} `json:"withNewFile"`
			} `json:"currentWorkspace"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &got))
		pending := got.CurrentWorkspace.WithNewFile.WithCommit.Git.Uncommitted
		require.False(t, pending.IsEmpty)
		require.Empty(t, pending.AddedPaths, "the committed file must drop out")
		require.Equal(t, []string{"b.txt"}, pending.ModifiedPaths,
			"the pre-existing dirty file must survive a scoped commit")
	})
}

// TestWorkspaceCommitPreexistingChangesExportRemainder pins the export half of
// finding 1: with pre-existing dirty content left *out* of the commit, saving
// must still land the commit and write exactly the overlay's remainder —
// without touching, duplicating or reverting the work the checkout carried.
func (WorkspaceSuite) TestWorkspaceCommitPreexistingChangesExportRemainder(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := withCommitBase(t, c).WithNewFile("b.txt", "b2")
	baseHead := gitOut(ctx, t, base, "rev-parse", "HEAD")

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

	require.Equal(t, "staged c", gitOut(ctx, t, saved, "log", "-1", "--pretty=%s"))
	require.Equal(t, baseHead, gitOut(ctx, t, saved, "rev-parse", "HEAD~1"))
	require.Equal(t, "c1", gitOut(ctx, t, saved, "show", "HEAD:c.txt"))

	// The untouched dirty file is still dirty, with its own content; the
	// uncommitted edit landed as an untracked file.
	require.Equal(t, "M b.txt\n?? d.txt", gitOut(ctx, t, saved, "status", "--porcelain"))
	contents, err := saved.File("b.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "b2", contents)
	contents, err = saved.File("d.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "d1", contents)
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

	// Landing goes through the client's own git, so the move is recorded in
	// the reflog like any other fast-forward.
	require.Equal(t, baseHead, gitOut(ctx, t, saved, "rev-parse", "HEAD@{1}"))
	require.Contains(t, gitOut(ctx, t, saved, "reflog", "-1"), "Fast-forward")

	// Both files are in the work tree.
	contents, err := saved.File("c.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "c1", contents)
	contents, err = saved.File("d.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "d1", contents)
}

// TestWorkspaceCommitExportWorktree saves staged commits from a linked git
// worktree, whose .git is a pointer file into the main checkout. The client's
// own git applies the bundle, so the per-worktree branch advances and the main
// checkout is left alone.
func (WorkspaceSuite) TestWorkspaceCommitExportWorktree(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		WithNewFile("tracked.txt", "v1").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"}).
		WithExec([]string{"git", "worktree", "add", "-b", "feature", "/linked"}).
		WithWorkdir("/linked")

	baseHead := gitOut(ctx, t, base, "rev-parse", "HEAD")
	mainBranch := gitOut(ctx, t, base, "-C", "/work", "symbolic-ref", "--short", "HEAD")
	mainHead := gitOut(ctx, t, base, "-C", "/work", "rev-parse", "HEAD")

	saved := base.With(daggerQuery(`{
  currentWorkspace {
    withNewFile(path: "feature.txt", contents: "f1") {
      withCommit(message: "staged feature", date: "` + commitTestDate + `") {
        export
      }
    }
  }
}`))

	// The worktree's own branch advanced to the staged commit.
	head := gitOut(ctx, t, saved, "rev-parse", "HEAD")
	require.NotEqual(t, baseHead, head)
	require.Equal(t, baseHead, gitOut(ctx, t, saved, "rev-parse", "HEAD~1"))
	require.Equal(t, "staged feature", gitOut(ctx, t, saved, "log", "-1", "--pretty=%s"))
	// ...and the branch ref lives in the main checkout's .git, so it is
	// visible from there too.
	require.Equal(t, head, gitOut(ctx, t, saved, "-C", "/work", "rev-parse", "feature"))

	// The main checkout is untouched.
	require.Equal(t, mainHead, gitOut(ctx, t, saved, "-C", "/work", "rev-parse", "HEAD"))
	require.Equal(t, mainHead, gitOut(ctx, t, saved, "-C", "/work", "rev-parse", mainBranch))

	// The committed content is in the linked work tree, which is clean.
	contents, err := saved.File("/linked/feature.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "f1", contents)
	require.Equal(t, "", gitOut(ctx, t, saved, "status", "--porcelain"))
}

// TestWorkspaceCommitExportSubmodule saves staged commits from a submodule
// checkout, whose .git is a pointer file into the superproject's
// .git/modules/<name>.
func (WorkspaceSuite) TestWorkspaceCommitExportSubmodule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		WithNewFile("tracked.txt", "v1").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"}).
		// The submodule's upstream, a plain local repository.
		WithWorkdir("/upstream").
		WithExec([]string{"git", "init", "-q", "-b", "main", "."}).
		WithNewFile("/upstream/sub.txt", "s1").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "sub initial"}).
		WithWorkdir("/work").
		WithExec([]string{"git", "-c", "protocol.file.allow=always", "submodule", "add", "/upstream", "sub"}).
		WithExec([]string{"git", "commit", "-m", "add submodule"}).
		WithWorkdir("/work/sub")

	baseHead := gitOut(ctx, t, base, "rev-parse", "HEAD")

	saved := base.With(daggerQuery(`{
  currentWorkspace {
    withNewFile(path: "more.txt", contents: "m1") {
      withCommit(message: "staged in submodule", date: "` + commitTestDate + `") {
        export
      }
    }
  }
}`))

	head := gitOut(ctx, t, saved, "rev-parse", "HEAD")
	require.NotEqual(t, baseHead, head)
	require.Equal(t, baseHead, gitOut(ctx, t, saved, "rev-parse", "HEAD~1"))
	require.Equal(t, "staged in submodule", gitOut(ctx, t, saved, "log", "-1", "--pretty=%s"))

	contents, err := saved.File("/work/sub/more.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "m1", contents)
	require.Equal(t, "", gitOut(ctx, t, saved, "status", "--porcelain"))
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
