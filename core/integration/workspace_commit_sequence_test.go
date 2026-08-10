package core

// Deterministic reproduction for hack/designs/async-agents.md §11 item 14 ("a
// surgical edit to a long-tracked file is recorded as a whole-file ADD").
//
// The sightings all report the same shape: `status` says `M +107 -5` and the
// commit that immediately follows records `A +1976 -0`. The two reads differ
// only in what they diff against. `Workspace.git.uncommitted` diffs against
// the staged HEAD. The summary the `commit` tool prints
// (modules/editor/main.dang) comes from `Workspace.git.stagedCommits[N]
// .changes`, whose per-commit delta is rendered in stagedCommitChanges
// (core/schema/workspace_commit.go) as
//
//	before := commit.Committed.Self().Before
//	if index > 0 && commits[index-1].Committed.Self() != nil {
//	    before = commits[index-1].Committed.Self().After   // <-- the defect
//	}
//	inst = commit.Committed.After.changes(from: before)
//
// `commits[N-1].Committed.After` is the scoped staged tree built at commit
// N-1's instant: withCommit hands the overlay's pending remainder to
// scopeChangesetToPaths, whose `scopedAfter` starts from that changeset's
// Before — the overlay's SPARSE host base, sparseHostBase(TouchedPaths)
// (core/schema/workspace.go), sized by the paths touched SO FAR. That tree is
// frozen into the commit record and then reused as the BEFORE tree of the next
// commit's delta.
//
// So a file whose FIRST edit happens after commit N-1 was staged is simply
// absent from the tree commit N is diffed against, and
// computeChangesetPathsDelta -> buildDiffStats (core/changeset.go) reports it
// ADDED, with a whole-file line count. Index 0 anchors on
// commit.Committed.Before — the current sparse base, sized after the edit, so
// it does contain the file — and is always correct.
//
// That accounts for every recorded property of the defect:
//
//   - the FIRST commit of a session is right ("an earlier commit in the same
//     session correctly reported M for core/agent.go");
//   - a LATER commit of a file first touched after the previous commit is
//     wrong (the eighth, chief-side sighting: no module, no worker, no
//     restart);
//   - a still later commit of the SAME file is right again, because by then
//     the path is in the previous commit's frozen tree (the fifth sighting's
//     "a ~160-line edit to the same file minutes later recorded correctly as
//     M +160 -7").
//
// It is not intermittent: it is deterministic in the commit sequence, which is
// why every attempt to reproduce it from a single edit and a single commit —
// including workspace_module_edit_test.go's confirmation experiment — came
// back green.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const (
	// seqTrackedPath is the file under test: committed to the checkout before
	// anything runs, and never touched again except by the one-line edit whose
	// reporting is measured.
	seqTrackedPath = "tracked.txt"
	// seqOtherPath and seqThirdPath are further long-tracked files, used to
	// drive commits that precede the edit without touching it.
	seqOtherPath = "other.txt"
	seqThirdPath = "third.txt"
	// seqScratchPath is a brand-new file: committing it is the cheapest way to
	// stage a commit whose touched set excludes seqTrackedPath.
	seqScratchPath = "scratch.txt"

	seqOldLine = "line 07"
	seqNewLine = "line 07 (edited)"
)

// seqTrackedLines is the committed line count of each tracked file: long
// enough that a whole-file ADD is unmistakable next to a one-line MODIFIED.
const seqTrackedLines = 12

func seqFileContents() string {
	var b strings.Builder
	for i := 1; i <= seqTrackedLines; i++ {
		fmt.Fprintf(&b, "line %02d\n", i)
	}
	return b.String()
}

// seqEditedContents is the committed content with exactly one line replaced.
func seqEditedContents(newLine string) string {
	return strings.Replace(seqFileContents(), seqOldLine, newLine, 1)
}

// seqBase is a checkout with every tracked file committed and clean.
func seqBase(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return gitRepoBase(t, c).
		WithNewFile(seqTrackedPath, seqFileContents()).
		WithNewFile(seqOtherPath, seqFileContents()).
		WithNewFile(seqThirdPath, seqFileContents()).
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"})
}

// seqStagedCommit is one entry of Workspace.git.stagedCommits: the message and
// the changeset that commit alone recorded. This is the per-commit delta the
// `commit` tool prints and `pull` replays — deliberately NOT the cumulative
// WorkspacePendingCommit.Committed record it is derived from.
type seqStagedCommit struct {
	Message string             `json:"message"`
	Changes uncommittedChanges `json:"changes"`
}

// seqAfterCommits is everything each case reads once its commits are staged:
// what the commits REPORT, and what the workspace actually CONTAINS.
type seqAfterCommits struct {
	Entries []string `json:"entries"`
	Git     struct {
		StagedCommits []seqStagedCommit  `json:"stagedCommits"`
		Uncommitted   uncommittedChanges `json:"uncommitted"`
	} `json:"git"`
	C0 struct {
		Contents string `json:"contents"`
	} `json:"c0"`
	C1 struct {
		Contents string `json:"contents"`
	} `json:"c1"`
}

// seqSelection renders that read. contentPaths are aliased c0, c1... and must
// still exist in the workspace — the removal case passes none and reads the
// file listing instead.
func seqSelection(contentPaths ...string) string {
	var b strings.Builder
	b.WriteString(`
    entries: glob(pattern: "*.txt")
    git {
      stagedCommits {
        message
        changes { diffStats { path kind addedLines removedLines } }
      }
      uncommitted { isEmpty diffStats { path kind addedLines removedLines } }
    }`)
	for i, p := range contentPaths {
		fmt.Fprintf(&b, "\n    c%d: file(path: %q) { contents }", i, p)
	}
	return b.String()
}

// seqUncommittedSelection is the other anchor, read at the instant BEFORE the
// last commit: git's own view, which the seventh and eighth sightings observed
// to be RIGHT at the same instant the commit summary lied.
const seqUncommittedSelection = `
    pending: git { uncommitted { isEmpty diffStats { path kind addedLines removedLines } } }`

// decodeSeqAfterCommits pulls the post-commit read out of a response, and logs
// each commit's summary in the same notation the `commit` tool prints — so a
// failure shows the lying summary verbatim.
func decodeSeqAfterCommits(t *testctx.T, out, path string) seqAfterCommits {
	t.Helper()
	raw := gjson.Get(out, path)
	require.True(t, raw.Exists(), "no %q in response: %s", path, out)
	var got seqAfterCommits
	require.NoError(t, json.Unmarshal([]byte(raw.Raw), &got))
	for i, c := range got.Git.StagedCommits {
		rendered := make([]string, 0, len(c.Changes.DiffStats))
		for _, s := range c.Changes.DiffStats {
			rendered = append(rendered, fmt.Sprintf("%s %s (+%d/-%d)",
				s.Kind[:1], s.Path, s.AddedLines, s.RemovedLines))
		}
		t.Logf("staged commit %d %q: %s", i, c.Message, strings.Join(rendered, ", "))
	}
	return got
}

// decodeSeqUncommitted pulls a git.uncommitted changeset out of a response.
func decodeSeqUncommitted(t *testctx.T, out, path string) uncommittedChanges {
	t.Helper()
	raw := gjson.Get(out, path)
	require.True(t, raw.Exists(), "no %q in response: %s", path, out)
	var changes uncommittedChanges
	require.NoError(t, json.Unmarshal([]byte(raw.Raw), &changes))
	return changes
}

// requireSeqModify is the whole measurement: a one-line edit to a file the
// checkout has carried since before the session must be reported as a
// one-line MODIFIED, not as an ADDED of all seqTrackedLines lines.
func requireSeqModify(t *testctx.T, label string, changes uncommittedChanges, path string) {
	t.Helper()
	stat, ok := changes.find(path)
	require.True(t, ok, "%s: no %s entry in %v", label, path, changes.DiffStats)
	t.Logf("%s: %s %s +%d -%d", label, stat.Path, stat.Kind, stat.AddedLines, stat.RemovedLines)
	require.Equal(t, "MODIFIED", stat.Kind,
		"%s: a one-line edit to a long-tracked, host-present file reported as %s +%d -%d",
		label, stat.Kind, stat.AddedLines, stat.RemovedLines)
	require.Equal(t, 1, stat.AddedLines, "%s: added lines", label)
	require.Equal(t, 1, stat.RemovedLines, "%s: removed lines", label)
}

// requireSeqContentCommitted is the innocence check that runs beside every
// measurement above: whatever the summary says, the workspace must hold the
// surgically edited content, and nothing may be left pending — which together
// mean the staged commits' own tree holds it too, since the pending view is
// diffed against exactly that tree. It separates "the history is corrupt" from
// "the history is fine and only the projection lies".
func requireSeqContentCommitted(t *testctx.T, label string, got seqAfterCommits, want string) {
	t.Helper()
	require.Equal(t, want, got.C0.Contents,
		"%s: the workspace's own copy of the edited file", label)
	require.True(t, got.Git.Uncommitted.IsEmpty,
		"%s: everything should have landed in the commits, still pending: %v",
		label, got.Git.Uncommitted.DiffStats)
}

// TestWorkspaceStagedCommitSequenceTrackedEdit drives the multi-commit
// sequences the real sightings came from, entirely client-side: an ordinary
// client-local host workspace, no module, no worker, no restart, no checkout
// move. Each case reads the per-commit delta from Workspace.git.stagedCommits,
// which is what the UI prints and what `pull` replays — and, beside it, what
// the workspace actually contains, so a failure says which of the two is wrong.
func (WorkspaceSuite) TestWorkspaceStagedCommitSequenceTrackedEdit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := seqBase(t, c)
	edited := graphqlString(t, seqEditedContents(seqNewLine))

	// THE REPRO. Commit a file, then first-edit a long-tracked file, then
	// commit that. The second commit's delta is anchored on the first commit's
	// frozen staged tree, which was built on sparseHostBase(["scratch.txt"]) —
	// a tree with no tracked.txt in it at all.
	t.Run("second commit of a newly touched tracked file", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerQuery(`{
  currentWorkspace {
    scratch: withNewFile(path: "` + seqScratchPath + `", contents: "scratch\n") {
      first: withCommit(message: "add scratch", date: "` + commitTestDate + `") {
        edited: withNewFile(path: "` + seqTrackedPath + `", contents: ` + edited + `) {` +
			seqUncommittedSelection + `
          second: withCommit(message: "edit tracked", date: "` + commitTestDate + `") {` +
			seqSelection(seqTrackedPath) + `
          }
        }
      }
    }
  }
}`)).Stdout(ctx)
		require.NoError(t, err)

		root := "currentWorkspace.scratch.first.edited"

		// The anchor that is right, read at the instant before the commit —
		// the `status` line of the seventh and eighth sightings.
		requireSeqModify(t, "git.uncommitted before the commit",
			decodeSeqUncommitted(t, out, root+".pending.uncommitted"), seqTrackedPath)

		got := decodeSeqAfterCommits(t, out, root+".second")
		commits := got.Git.StagedCommits
		require.Len(t, commits, 2)
		require.Equal(t, "edit tracked", commits[1].Message)

		// The content is innocent: only the projection lies.
		requireSeqContentCommitted(t, "second commit of a newly touched tracked file",
			got, seqEditedContents(seqNewLine))

		// The anchor that lies.
		requireSeqModify(t, "staged commit 2 (edit tracked)", commits[1].Changes, seqTrackedPath)
	})

	// CONTROL for the case above, and the discriminator: the same two commits,
	// but both files are edited BEFORE the first commit, so tracked.txt is in
	// the touched set that sizes the first commit's frozen tree. Path-scoping
	// the first commit is what keeps the tracked edit pending across it.
	// Predicted (and required) to be correct.
	t.Run("control: both files edited before the first commit", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerQuery(`{
  currentWorkspace {
    scratch: withNewFile(path: "` + seqScratchPath + `", contents: "scratch\n") {
      edited: withNewFile(path: "` + seqTrackedPath + `", contents: ` + edited + `) {
        first: withCommit(message: "add scratch", date: "` + commitTestDate + `", paths: ["` + seqScratchPath + `"]) {` +
			seqUncommittedSelection + `
          second: withCommit(message: "edit tracked", date: "` + commitTestDate + `", paths: ["` + seqTrackedPath + `"]) {` +
			seqSelection(seqTrackedPath) + `
          }
        }
      }
    }
  }
}`)).Stdout(ctx)
		require.NoError(t, err)

		root := "currentWorkspace.scratch.edited.first"
		requireSeqModify(t, "git.uncommitted before the commit",
			decodeSeqUncommitted(t, out, root+".pending.uncommitted"), seqTrackedPath)

		got := decodeSeqAfterCommits(t, out, root+".second")
		require.Len(t, got.Git.StagedCommits, 2)
		requireSeqContentCommitted(t, "control", got, seqEditedContents(seqNewLine))
		requireSeqModify(t, "staged commit 2 (edit tracked)", got.Git.StagedCommits[1].Changes, seqTrackedPath)
	})

	// The same first-edit-after-a-commit shape with a path-SCOPED commit on
	// both ends: scoping routes through scopeChangesetToPaths, which is where
	// the frozen tree is built, so it is worth pinning separately.
	t.Run("path-scoped second commit", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerQuery(`{
  currentWorkspace {
    scratch: withNewFile(path: "` + seqScratchPath + `", contents: "scratch\n") {
      first: withCommit(message: "add scratch", date: "` + commitTestDate + `", paths: ["` + seqScratchPath + `"]) {
        edited: withNewFile(path: "` + seqTrackedPath + `", contents: ` + edited + `) {` +
			seqUncommittedSelection + `
          second: withCommit(message: "edit tracked", date: "` + commitTestDate + `", paths: ["` + seqTrackedPath + `"]) {` +
			seqSelection(seqTrackedPath) + `
          }
        }
      }
    }
  }
}`)).Stdout(ctx)
		require.NoError(t, err)

		root := "currentWorkspace.scratch.first.edited"
		requireSeqModify(t, "git.uncommitted before the commit",
			decodeSeqUncommitted(t, out, root+".pending.uncommitted"), seqTrackedPath)

		got := decodeSeqAfterCommits(t, out, root+".second")
		require.Len(t, got.Git.StagedCommits, 2)
		requireSeqContentCommitted(t, "path-scoped second commit", got, seqEditedContents(seqNewLine))
		requireSeqModify(t, "staged commit 2 (edit tracked)", got.Git.StagedCommits[1].Changes, seqTrackedPath)
	})

	// Three commits deep, each first-touching a different long-tracked file.
	// The anchor gets staler with every commit, so both the second and the
	// third commit are predicted to misreport — and the third's summary should
	// also show a stale re-report of the second's file if the frozen tree
	// really is the one that lags.
	t.Run("three commits deep", func(ctx context.Context, t *testctx.T) {
		otherEdited := graphqlString(t, seqEditedContents("line 07 (other)"))
		thirdEdited := graphqlString(t, seqEditedContents("line 07 (third)"))
		out, err := base.With(daggerQuery(`{
  currentWorkspace {
    scratch: withNewFile(path: "` + seqScratchPath + `", contents: "scratch\n") {
      first: withCommit(message: "add scratch", date: "` + commitTestDate + `") {
        e2: withNewFile(path: "` + seqOtherPath + `", contents: ` + otherEdited + `) {
          second: withCommit(message: "edit other", date: "` + commitTestDate + `") {
            e3: withNewFile(path: "` + seqThirdPath + `", contents: ` + thirdEdited + `) {
              third: withCommit(message: "edit third", date: "` + commitTestDate + `") {` +
			seqSelection(seqOtherPath, seqThirdPath) + `
              }
            }
          }
        }
      }
    }
  }
}`)).Stdout(ctx)
		require.NoError(t, err)

		got := decodeSeqAfterCommits(t, out,
			"currentWorkspace.scratch.first.e2.second.e3.third")
		commits := got.Git.StagedCommits
		require.Len(t, commits, 3)

		// Both files hold their surgical edit, and nothing is left pending.
		require.Equal(t, seqEditedContents("line 07 (other)"), got.C0.Contents, "other.txt content")
		require.Equal(t, seqEditedContents("line 07 (third)"), got.C1.Contents, "third.txt content")
		require.True(t, got.Git.Uncommitted.IsEmpty,
			"nothing should be left pending, got %v", got.Git.Uncommitted.DiffStats)

		requireSeqModify(t, "staged commit 2 (edit other)", commits[1].Changes, seqOtherPath)
		requireSeqModify(t, "staged commit 3 (edit third)", commits[2].Changes, seqThirdPath)
	})

	// The fifth sighting's pair in one session: the same file edited and
	// committed twice. The first of the two is predicted to lie; the second is
	// predicted to be correct, because by then the path is in the previous
	// commit's frozen tree. Asserting both pins the discriminator exactly.
	t.Run("two commits of the same file", func(ctx context.Context, t *testctx.T) {
		again := graphqlString(t, seqEditedContents("line 07 (edited twice)"))
		out, err := base.With(daggerQuery(`{
  currentWorkspace {
    scratch: withNewFile(path: "` + seqScratchPath + `", contents: "scratch\n") {
      first: withCommit(message: "add scratch", date: "` + commitTestDate + `") {
        edited: withNewFile(path: "` + seqTrackedPath + `", contents: ` + edited + `) {
          second: withCommit(message: "edit tracked", date: "` + commitTestDate + `") {
            again: withNewFile(path: "` + seqTrackedPath + `", contents: ` + again + `) {
              third: withCommit(message: "edit tracked again", date: "` + commitTestDate + `") {` +
			seqSelection(seqTrackedPath) + `
              }
            }
          }
        }
      }
    }
  }
}`)).Stdout(ctx)
		require.NoError(t, err)

		got := decodeSeqAfterCommits(t, out,
			"currentWorkspace.scratch.first.edited.second.again.third")
		commits := got.Git.StagedCommits
		require.Len(t, commits, 3)

		// Both edits are really in the history: the file holds the second
		// edit, and nothing is pending on top of it.
		requireSeqContentCommitted(t, "two commits of the same file",
			got, seqEditedContents("line 07 (edited twice)"))

		// The later edit of the same file: predicted correct, and the fifth
		// sighting observed exactly that.
		requireSeqModify(t, "staged commit 3 (edit tracked again)", commits[2].Changes, seqTrackedPath)
		// The earlier one: predicted to be the whole-file ADD.
		requireSeqModify(t, "staged commit 2 (edit tracked)", commits[1].Changes, seqTrackedPath)
	})

	// The removal-shaped variant: a file whose FIRST edit after a prior commit
	// is a deletion. The frozen anchor does not contain it, so there is
	// nothing for the delta to record as removed — the commit's summary is
	// predicted to omit the deletion entirely, which is strictly worse than a
	// wrong kind: `pull` replaying that delta would not delete the file.
	t.Run("removal as the first edit after a prior commit", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerQuery(`{
  currentWorkspace {
    scratch: withNewFile(path: "` + seqScratchPath + `", contents: "scratch\n") {
      first: withCommit(message: "add scratch", date: "` + commitTestDate + `") {
        removed: withoutFile(path: "` + seqTrackedPath + `") {` +
			seqUncommittedSelection + `
          second: withCommit(message: "drop tracked", date: "` + commitTestDate + `") {` +
			seqSelection() + `
          }
        }
      }
    }
  }
}`)).Stdout(ctx)
		require.NoError(t, err)

		root := "currentWorkspace.scratch.first.removed"

		pending := decodeSeqUncommitted(t, out, root+".pending.uncommitted")
		pendingStat, ok := pending.find(seqTrackedPath)
		require.True(t, ok, "git.uncommitted: no %s entry in %v", seqTrackedPath, pending.DiffStats)
		require.Equal(t, "REMOVED", pendingStat.Kind, "git.uncommitted before the commit")

		got := decodeSeqAfterCommits(t, out, root+".second")
		commits := got.Git.StagedCommits
		require.Len(t, commits, 2)

		// The deletion really happened: the file is gone from the workspace
		// and nothing is left pending, so the staged commit's tree does not
		// contain it either. The summary alone is what loses it.
		t.Logf("workspace after the commits: %v", got.Entries)
		require.NotContains(t, got.Entries, seqTrackedPath,
			"the file should be gone from the workspace")
		require.True(t, got.Git.Uncommitted.IsEmpty,
			"the deletion should be fully committed, still pending: %v", got.Git.Uncommitted.DiffStats)

		stat, ok := commits[1].Changes.find(seqTrackedPath)
		require.True(t, ok, "staged commit 2 (drop tracked): the deletion of %s is missing from %v",
			seqTrackedPath, commits[1].Changes.DiffStats)
		require.Equal(t, "REMOVED", stat.Kind, "staged commit 2 (drop tracked)")
	})
}

// seqSavedHistory runs a query that ends in Workspace.export — the engine's
// "save": staged commits are packed into a git bundle and fast-forwarded onto
// the checkout by the client's own git — and then reads the resulting
// repository with git itself.
func seqSavedHistory(ctx context.Context, t *testctx.T, base *dagger.Container, query, script string) string {
	t.Helper()
	out, err := base.
		With(daggerQuery(query)).
		WithExec([]string{"sh", "-c", script}).
		Stdout(ctx)
	require.NoError(t, err)
	t.Logf("saved checkout:\n%s", out)
	return out
}

// TestWorkspaceStagedCommitSequenceSavedHistoryIsIntact answers the question
// the summary raises but cannot settle: is the recorded HISTORY wrong, or only
// its projection? It runs the same failing sequences, saves them to the
// checkout, and then asks git — the one reader that never consults the
// changeset machinery.
//
// This test is expected to pass both before and after item 14's fix. It is
// here so that the red tests above cannot be misread as "the commits are
// corrupt": they are not, and a reader deciding how alarmed to be needs that
// stated by a measurement rather than by assertion.
func (WorkspaceSuite) TestWorkspaceStagedCommitSequenceSavedHistoryIsIntact(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := seqBase(t, c)
	edited := graphqlString(t, seqEditedContents(seqNewLine))

	t.Run("edit committed after a prior commit", func(ctx context.Context, t *testctx.T) {
		out := seqSavedHistory(ctx, t, base, `{
  currentWorkspace {
    scratch: withNewFile(path: "`+seqScratchPath+`", contents: "scratch\n") {
      first: withCommit(message: "add scratch", date: "`+commitTestDate+`") {
        edited: withNewFile(path: "`+seqTrackedPath+`", contents: `+edited+`) {
          second: withCommit(message: "edit tracked", date: "`+commitTestDate+`") {
            export
          }
        }
      }
    }
  }
}`, `git log --format='commit %s' --name-status
echo '--- HEAD:`+seqTrackedPath+`'
git show HEAD:`+seqTrackedPath+`
echo '--- status'
git status --porcelain`)

		// git's own name-status for the commit whose summary said "A".
		// --format leaves a blank line before the name-status block.
		require.Contains(t, out, "commit edit tracked\n\nM\t"+seqTrackedPath,
			"git records the second commit as a modification of %s", seqTrackedPath)
		require.Contains(t, out, "commit add scratch\n\nA\t"+seqScratchPath)

		// The committed content is the surgical edit, not a re-add of some
		// other version of the file.
		require.Contains(t, out, "--- HEAD:"+seqTrackedPath+"\n"+seqEditedContents(seqNewLine))

		// And the checkout is clean afterwards: the save wrote exactly the
		// commits and left no remainder.
		require.Contains(t, out, "--- status\n")
		require.Equal(t, "", strings.TrimSpace(strings.SplitN(out, "--- status\n", 2)[1]),
			"the checkout should be clean after saving")
	})

	t.Run("removal committed after a prior commit", func(ctx context.Context, t *testctx.T) {
		out := seqSavedHistory(ctx, t, base, `{
  currentWorkspace {
    scratch: withNewFile(path: "`+seqScratchPath+`", contents: "scratch\n") {
      first: withCommit(message: "add scratch", date: "`+commitTestDate+`") {
        removed: withoutFile(path: "`+seqTrackedPath+`") {
          second: withCommit(message: "drop tracked", date: "`+commitTestDate+`") {
            export
          }
        }
      }
    }
  }
}`, `git log --format='commit %s' --name-status
echo '--- ls-tree'
git ls-tree --name-only HEAD
echo '--- status'
git status --porcelain`)

		// The deletion the summary omitted entirely is a real deletion in the
		// saved history: it is the record of the commit that is lossy, not the
		// commit.
		require.Contains(t, out, "commit drop tracked\n\nD\t"+seqTrackedPath,
			"git records the second commit as a deletion of %s", seqTrackedPath)
		require.NotContains(t, strings.SplitN(out, "--- ls-tree\n", 2)[1], seqTrackedPath+"\n",
			"the file should be gone from the saved tree")
	})
}

// TestWorkspaceStagedCommitSequenceHarvest measures the cost item 14 actually
// charges: a commit whose recorded changeset is a whole-file ADD is what a
// colleague's `pull` receives, because Workspace.withCommitsFrom replays that
// same recorded changeset as a patch (core/schema/workspace_commit_from.go).
//
// Both halves stage the SAME two commits with the SAME content in the same
// workspace and replay them onto the same receiver — a workspace snapshotted
// from the same checkout, carrying a committed edit of its own to a different
// line of the same file. They differ only in when the tracked path was first
// touched, i.e. only in what the second commit RECORDS. If the two halves
// disagree, the recorded changeset is the sole cause.
//
// The fixture runs inside a module because the two workspaces must be alive in
// one session: GraphQL cannot feed one field's result into another's argument,
// and currentWorkspace is not replayable across sessions.
func (WorkspaceSuite) TestWorkspaceStagedCommitSequenceHarvest(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := gitRepoBase(t, c).
		With(withModuleFixture(t, c, "editor", "go/workspace-editor")).
		WithNewFile(seqTrackedPath, seqFileContents()).
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"})

	// The receiver's own committed edit, on a line the source never touches.
	const receiverOldLine = "line 01"
	const receiverNewLine = "line 01 (receiver)"

	// What a correct replay must leave behind: both edits, neither clobbered.
	merged := strings.Replace(seqEditedContents(seqNewLine), receiverOldLine, receiverNewLine, 1)

	run := func(ctx context.Context, t *testctx.T, mode string) seqHarvestReport {
		t.Helper()
		out, err := base.With(daggerQueryAt("editor", `{
  harvest(
    path: "`+seqTrackedPath+`",
    oldLine: "`+seqOldLine+`",
    newLine: "`+seqNewLine+`",
    receiverOldLine: "`+receiverOldLine+`",
    receiverNewLine: "`+receiverNewLine+`",
    scratch: "`+seqScratchPath+`",
    date: "`+commitTestDate+`",
    mode: "`+mode+`"
  )
}`)).Stdout(ctx)
		require.NoError(t, err)

		raw := gjson.Get(out, "harvest")
		require.True(t, raw.Exists(), "no harvest in response: %s", out)
		var report seqHarvestReport
		require.NoError(t, json.Unmarshal([]byte(raw.String()), &report))

		for i, commit := range report.SourceCommits {
			rendered := make([]string, 0, len(commit.Stats))
			for _, s := range commit.Stats {
				rendered = append(rendered, fmt.Sprintf("%s %s (+%d/-%d)",
					s.Kind[:1], s.Path, s.AddedLines, s.RemovedLines))
			}
			t.Logf("source commit %d %q: %s", i, commit.Message, strings.Join(rendered, ", "))
		}
		for _, pick := range report.Picks {
			t.Logf("pick %q: %s %s %v", pick.Message, pick.Status, pick.Reason, pick.ConflictPaths)
		}
		if report.ApplyError != "" {
			t.Logf("withCommitsFrom failed: %s", report.ApplyError)
		}
		return report
	}

	requirePulled := func(t *testctx.T, label string, report seqHarvestReport) {
		t.Helper()
		require.Len(t, report.Picks, 2, "%s: picks", label)
		for _, pick := range report.Picks {
			require.Equal(t, "PICKABLE", pick.Status,
				"%s: %q came back %s/%s on %v — the receiver cannot take this commit",
				label, pick.Message, pick.Status, pick.Reason, pick.ConflictPaths)
			require.Equal(t, "NONE", pick.Reason, "%s: %q reason", label, pick.Message)
			require.Empty(t, pick.ConflictPaths, "%s: %q conflict paths", label, pick.Message)
		}
		require.Empty(t, report.ApplyError, "%s: withCommitsFrom", label)
		require.Equal(t,
			[]string{"receiver work", "add scratch", "edit tracked"},
			report.ReceiverCommits, "%s: the receiver's staged stack", label)
		// Neither edit is lost: whole-file application would drop the
		// receiver's own line.
		require.Equal(t, merged, report.ReceiverContents,
			"%s: the replay must merge both edits, not overwrite the receiver's copy", label)
	}

	// THE COST. The failing order: the tracked file is first touched after the
	// first commit is staged, so the second commit records a whole-file ADD —
	// and an ADD cannot be applied onto a file the receiver already has.
	t.Run("after a prior commit", func(ctx context.Context, t *testctx.T) {
		requirePulled(t, "after a prior commit", run(ctx, t, "after-prior-commit"))
	})

	// CONTROL: identical commits and content, recorded correctly.
	t.Run("control: both files edited before the first commit", func(ctx context.Context, t *testctx.T) {
		requirePulled(t, "control", run(ctx, t, "control"))
	})
}

// seqHarvestReport is the JSON the editor module's Harvest function returns.
type seqHarvestReport struct {
	Mode             string             `json:"mode"`
	SourceCommits    []seqHarvestCommit `json:"sourceCommits"`
	Picks            []seqHarvestPick   `json:"picks"`
	ApplyError       string             `json:"applyError"`
	ReceiverCommits  []string           `json:"receiverCommits"`
	ReceiverContents string             `json:"receiverContents"`
}

type seqHarvestCommit struct {
	Message string              `json:"message"`
	Stats   []workspaceDiffStat `json:"stats"`
}

type seqHarvestPick struct {
	Message       string   `json:"message"`
	Status        string   `json:"status"`
	Reason        string   `json:"reason"`
	ConflictPaths []string `json:"conflictPaths"`
}
