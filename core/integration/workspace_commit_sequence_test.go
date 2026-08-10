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

// seqStagedCommitsSelection is the read side of every case below.
const seqStagedCommitsSelection = `
    git {
      stagedCommits {
        message
        changes { diffStats { path kind addedLines removedLines } }
      }
    }`

// seqUncommittedSelection is the other anchor: git's own view, which the
// seventh and eighth sightings observed to be RIGHT at the same instant the
// commit summary lied.
const seqUncommittedSelection = `
    pending: git { uncommitted { isEmpty diffStats { path kind addedLines removedLines } } }`

// decodeSeqStagedCommits pulls the staged commit list out of a response, and
// logs each commit's summary in the same notation the `commit` tool prints —
// so a failure shows the lying summary verbatim.
func decodeSeqStagedCommits(t *testctx.T, out, path string) []seqStagedCommit {
	t.Helper()
	raw := gjson.Get(out, path)
	require.True(t, raw.Exists(), "no %q in response: %s", path, out)
	var commits []seqStagedCommit
	require.NoError(t, json.Unmarshal([]byte(raw.Raw), &commits))
	for i, c := range commits {
		rendered := make([]string, 0, len(c.Changes.DiffStats))
		for _, s := range c.Changes.DiffStats {
			rendered = append(rendered, fmt.Sprintf("%s %s (+%d/-%d)",
				s.Kind[:1], s.Path, s.AddedLines, s.RemovedLines))
		}
		t.Logf("staged commit %d %q: %s", i, c.Message, strings.Join(rendered, ", "))
	}
	return commits
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

// TestWorkspaceStagedCommitSequenceTrackedEdit drives the multi-commit
// sequences the real sightings came from, entirely client-side: an ordinary
// client-local host workspace, no module, no worker, no restart, no checkout
// move. Each case reads the per-commit delta from Workspace.git.stagedCommits,
// which is what the UI prints and what `pull` replays.
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
			seqStagedCommitsSelection + `
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

		commits := decodeSeqStagedCommits(t, out, root+".second.git.stagedCommits")
		require.Len(t, commits, 2)
		require.Equal(t, "edit tracked", commits[1].Message)

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
			seqStagedCommitsSelection + `
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

		commits := decodeSeqStagedCommits(t, out, root+".second.git.stagedCommits")
		require.Len(t, commits, 2)
		requireSeqModify(t, "staged commit 2 (edit tracked)", commits[1].Changes, seqTrackedPath)
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
			seqStagedCommitsSelection + `
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

		commits := decodeSeqStagedCommits(t, out, root+".second.git.stagedCommits")
		require.Len(t, commits, 2)
		requireSeqModify(t, "staged commit 2 (edit tracked)", commits[1].Changes, seqTrackedPath)
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
			seqStagedCommitsSelection + `
              }
            }
          }
        }
      }
    }
  }
}`)).Stdout(ctx)
		require.NoError(t, err)

		commits := decodeSeqStagedCommits(t, out,
			"currentWorkspace.scratch.first.e2.second.e3.third.git.stagedCommits")
		require.Len(t, commits, 3)
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
			seqStagedCommitsSelection + `
              }
            }
          }
        }
      }
    }
  }
}`)).Stdout(ctx)
		require.NoError(t, err)

		commits := decodeSeqStagedCommits(t, out,
			"currentWorkspace.scratch.first.edited.second.again.third.git.stagedCommits")
		require.Len(t, commits, 3)

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
			seqStagedCommitsSelection + `
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

		commits := decodeSeqStagedCommits(t, out, root+".second.git.stagedCommits")
		require.Len(t, commits, 2)
		stat, ok := commits[1].Changes.find(seqTrackedPath)
		require.True(t, ok, "staged commit 2 (drop tracked): the deletion of %s is missing from %v",
			seqTrackedPath, commits[1].Changes.DiffStats)
		require.Equal(t, "REMOVED", stat.Kind, "staged commit 2 (drop tracked)")
	})
}
