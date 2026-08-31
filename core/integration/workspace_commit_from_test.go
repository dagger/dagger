package core

// Coverage for Workspace.commitsFrom / Workspace.withCommitsFrom: replaying
// commits staged in one workspace onto another, which is how work done in an
// agent's own workspace gets back to the workspace that spawned it.

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// commitsFromRepo builds a real git repository as an in-engine Directory.
//
// The rest of the workspace suite drives `currentWorkspace` through
// `daggerQuery` in a container, one session per exec, which cannot express
// "pass workspace B as an argument to workspace A": GraphQL cannot feed one
// field's result into another's argument, and currentWorkspace is not
// replayable. Directory-backed (value) workspaces can, and they still exercise
// the whole path — ensureWorkspaceGitDirectory, workspaceCommitBaseRepo, the
// overlay's value branch, git.uncommitted through the overlay.
func commitsFromRepo(t testing.TB, c *dagger.Client, files map[string]string) *dagger.Directory {
	t.Helper()
	ctr := c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "--no-cache", "git"}).
		WithWorkdir("/work").
		WithExec([]string{"git", "init", "-q", "-b", "main"}).
		WithExec([]string{"git", "config", "user.name", "Base"}).
		WithExec([]string{"git", "config", "user.email", "base@example.com"})
	for _, p := range slices.Sorted(maps.Keys(files)) {
		ctr = ctr.WithNewFile(p, files[p])
	}
	return ctr.
		WithExec([]string{"git", "add", "-A"}).
		WithExec([]string{"git", "commit", "-q", "-m", "initial"}).
		Directory("/work")
}

// numberedLines is a ten-line file, so tests can edit line 1 and line 10
// independently and have the two edits genuinely not touch each other.
func numberedLines(marks map[int]string) string {
	var out strings.Builder
	for i := 1; i <= 10; i++ {
		if mark, ok := marks[i]; ok {
			out.WriteString(mark + "\n")
			continue
		}
		out.WriteString("line " + strconv.Itoa(i) + "\n")
	}
	return out.String()
}

// commitPickSnapshot is one planned pick, resolved into plain values.
type commitPickSnapshot struct {
	SHA           string
	Origin        string
	Message       string
	Date          string
	AuthorName    string
	AuthorEmail   string
	Status        dagger.WorkspaceCommitPickStatus
	Reason        dagger.WorkspaceCommitPickReason
	ConflictPaths []string
}

func planCommitsFrom(
	ctx context.Context,
	t *testctx.T,
	receiver, source *dagger.Workspace,
	commits ...string,
) []commitPickSnapshot {
	t.Helper()
	var opts []dagger.WorkspaceCommitsFromOpts
	if len(commits) > 0 {
		opts = append(opts, dagger.WorkspaceCommitsFromOpts{Commits: commits})
	}
	picks, err := receiver.CommitsFrom(ctx, source, opts...)
	require.NoError(t, err)
	return resolveCommitPicks(ctx, t, picks)
}

func resolveCommitPicks(ctx context.Context, t *testctx.T, picks []dagger.WorkspaceCommitPick) []commitPickSnapshot {
	t.Helper()
	out := make([]commitPickSnapshot, 0, len(picks))
	for _, pick := range picks {
		var snap commitPickSnapshot
		var err error
		snap.SHA, err = pick.Sha(ctx)
		require.NoError(t, err)
		snap.Origin, err = pick.Origin(ctx)
		require.NoError(t, err)
		snap.Message, err = pick.Message(ctx)
		require.NoError(t, err)
		snap.Date, err = pick.Date(ctx)
		require.NoError(t, err)
		snap.AuthorName, err = pick.AuthorName(ctx)
		require.NoError(t, err)
		snap.AuthorEmail, err = pick.AuthorEmail(ctx)
		require.NoError(t, err)
		snap.Status, err = pick.Status(ctx)
		require.NoError(t, err)
		snap.Reason, err = pick.Reason(ctx)
		require.NoError(t, err)
		snap.ConflictPaths, err = pick.ConflictPaths(ctx)
		require.NoError(t, err)
		out = append(out, snap)
	}
	return out
}

// stagedCommitSnapshot is one staged commit, resolved into plain values.
type stagedCommitSnapshot struct {
	SHA         string
	Origin      string
	Message     string
	Date        string
	AuthorName  string
	AuthorEmail string
}

func stagedCommitsOf(ctx context.Context, t *testctx.T, ws *dagger.Workspace) []stagedCommitSnapshot {
	t.Helper()
	commits, err := ws.Git().StagedCommits(ctx)
	require.NoError(t, err)
	out := make([]stagedCommitSnapshot, 0, len(commits))
	for _, commit := range commits {
		var snap stagedCommitSnapshot
		var err error
		snap.SHA, err = commit.Sha(ctx)
		require.NoError(t, err)
		snap.Origin, err = commit.Origin(ctx)
		require.NoError(t, err)
		snap.Message, err = commit.Message(ctx)
		require.NoError(t, err)
		snap.Date, err = commit.Date(ctx)
		require.NoError(t, err)
		snap.AuthorName, err = commit.AuthorName(ctx)
		require.NoError(t, err)
		snap.AuthorEmail, err = commit.AuthorEmail(ctx)
		require.NoError(t, err)
		out = append(out, snap)
	}
	return out
}

func statuses(picks []commitPickSnapshot) []dagger.WorkspaceCommitPickStatus {
	out := make([]dagger.WorkspaceCommitPickStatus, 0, len(picks))
	for _, p := range picks {
		out = append(out, p.Status)
	}
	return out
}

// TestWorkspaceCommitsFromFreshPull is the base case: a worker's commit lands
// in a chief that has not moved, with its metadata intact and a new hash.
func (WorkspaceSuite) TestWorkspaceCommitsFromFreshPull(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	chief := commitsFromRepo(t, c, map[string]string{"a.txt": "a1\n"}).AsWorkspace()
	worker := chief.
		WithNewFile("worker.txt", "from the worker\n").
		WithCommit("worker work", commitTestDate, dagger.WorkspaceWithCommitOpts{
			AuthorName:  "Scout",
			AuthorEmail: "scout@example.com",
		})

	workerStaged := stagedCommitsOf(ctx, t, worker)
	require.Len(t, workerStaged, 1)

	plan := planCommitsFrom(ctx, t, chief, worker)
	require.Len(t, plan, 1)
	require.Equal(t, dagger.WorkspaceCommitPickStatusPickable, plan[0].Status)
	require.Equal(t, dagger.WorkspaceCommitPickReasonNone, plan[0].Reason)
	require.Empty(t, plan[0].ConflictPaths)
	require.Equal(t, workerStaged[0].SHA, plan[0].SHA)
	require.Empty(t, plan[0].Origin, "the worker authored it, so it has no origin of its own")
	require.Equal(t, "worker work", plan[0].Message)
	require.Equal(t, "Scout", plan[0].AuthorName)
	require.Equal(t, "scout@example.com", plan[0].AuthorEmail)

	// The pick carries the source's own changeset, so a caller can render a
	// per-commit diffstat without a second round trip.
	picks, err := chief.CommitsFrom(ctx, worker)
	require.NoError(t, err)
	require.Len(t, picks, 1)
	stats, err := picks[0].Changes().DiffStats(ctx)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	statPath, err := stats[0].Path(ctx)
	require.NoError(t, err)
	require.Equal(t, "worker.txt", statPath)

	pulled := chief.WithCommitsFrom(worker)
	pulledStaged := stagedCommitsOf(ctx, t, pulled)
	require.Len(t, pulledStaged, 1)
	// The origin is what ties the replayed commit back to the one the worker
	// staged. Here the two hashes happen to coincide as well: the chief has not
	// moved, so the replay rebuilds the same commit on the same parent, and
	// withCommit is deterministic. See DriftStillApplies for the case where the
	// parent — and so the hash — really does change.
	require.Equal(t, workerStaged[0].SHA, pulledStaged[0].Origin)
	require.Equal(t, "worker work", pulledStaged[0].Message)
	require.Equal(t, commitTestDate, pulledStaged[0].Date)
	require.Equal(t, "Scout", pulledStaged[0].AuthorName)
	require.Equal(t, "scout@example.com", pulledStaged[0].AuthorEmail)

	head, err := pulled.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, pulledStaged[0].SHA, head)

	contents, err := pulled.File("worker.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "from the worker\n", contents)

	// Everything the replay wrote went into the commit; nothing is left over.
	isEmpty, err := pulled.Git().Uncommitted().IsEmpty(ctx)
	require.NoError(t, err)
	require.True(t, isEmpty)
}

// TestWorkspaceCommitsFromReplayIsIdempotent locks in the point of recording an
// origin: pulling the same work twice is a no-op, not a duplicate commit.
func (WorkspaceSuite) TestWorkspaceCommitsFromReplayIsIdempotent(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	chief := commitsFromRepo(t, c, map[string]string{"a.txt": "a1\n"}).AsWorkspace()
	worker := chief.
		WithNewFile("worker.txt", "from the worker\n").
		WithCommit("worker work", commitTestDate)

	pulled := chief.WithCommitsFrom(worker)
	require.Len(t, stagedCommitsOf(ctx, t, pulled), 1)

	replan := planCommitsFrom(ctx, t, pulled, worker)
	require.Equal(t,
		[]dagger.WorkspaceCommitPickStatus{dagger.WorkspaceCommitPickStatusPicked},
		statuses(replan))

	again := pulled.WithCommitsFrom(worker)
	require.Len(t, stagedCommitsOf(ctx, t, again), 1)
}

// TestWorkspaceCommitsFromInheritedCommitIsPicked covers the common shape: a
// worker's stack starts as a copy of the chief's, so a pull re-offers the
// chief's own commits verbatim and must recognise them.
func (WorkspaceSuite) TestWorkspaceCommitsFromInheritedCommitIsPicked(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := commitsFromRepo(t, c, map[string]string{"a.txt": "a1\n"}).AsWorkspace()
	chief := base.
		WithNewFile("chief.txt", "from the chief\n").
		WithCommit("chief work", commitTestDate)
	worker := chief.
		WithNewFile("worker.txt", "from the worker\n").
		WithCommit("worker work", commitTestDate)

	chiefStaged := stagedCommitsOf(ctx, t, chief)
	require.Len(t, chiefStaged, 1)
	workerStaged := stagedCommitsOf(ctx, t, worker)
	require.Len(t, workerStaged, 2)

	plan := planCommitsFrom(ctx, t, chief, worker)
	require.Equal(t, []dagger.WorkspaceCommitPickStatus{
		dagger.WorkspaceCommitPickStatusPicked,
		dagger.WorkspaceCommitPickStatusPickable,
	}, statuses(plan))

	pulled := chief.WithCommitsFrom(worker)
	pulledStaged := stagedCommitsOf(ctx, t, pulled)
	require.Len(t, pulledStaged, 2)
	require.Equal(t, chiefStaged[0].SHA, pulledStaged[0].SHA, "the chief's own commit is untouched")
	require.Empty(t, pulledStaged[0].Origin)
	require.Equal(t, workerStaged[1].SHA, pulledStaged[1].Origin)
}

// TestWorkspaceCommitsFromDriftStillApplies is the test that fails under naive
// whole-file application: both sides edited the same file, in different places,
// and both edits have to survive.
func (WorkspaceSuite) TestWorkspaceCommitsFromDriftStillApplies(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := commitsFromRepo(t, c, map[string]string{"a.txt": numberedLines(nil)}).AsWorkspace()
	worker := base.
		WithNewFile("a.txt", numberedLines(map[int]string{1: "worker edit"})).
		WithCommit("worker edits line 1", commitTestDate)
	chief := base.
		WithNewFile("a.txt", numberedLines(map[int]string{10: "chief edit"})).
		WithCommit("chief edits line 10", commitTestDate)

	workerStaged := stagedCommitsOf(ctx, t, worker)
	require.Len(t, workerStaged, 1)

	plan := planCommitsFrom(ctx, t, chief, worker)
	require.Equal(t,
		[]dagger.WorkspaceCommitPickStatus{dagger.WorkspaceCommitPickStatusPickable},
		statuses(plan))

	pulled := chief.WithCommitsFrom(worker)
	contents, err := pulled.File("a.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, numberedLines(map[int]string{1: "worker edit", 10: "chief edit"}), contents)

	pulledStaged := stagedCommitsOf(ctx, t, pulled)
	require.Len(t, pulledStaged, 2)
	// Replayed onto a parent the worker never saw, so the hash is necessarily
	// new; the origin is what still identifies the work.
	require.NotEqual(t, workerStaged[0].SHA, pulledStaged[1].SHA)
	require.Equal(t, workerStaged[0].SHA, pulledStaged[1].Origin)

	isEmpty, err := pulled.Git().Uncommitted().IsEmpty(ctx)
	require.NoError(t, err)
	require.True(t, isEmpty)
}

// TestWorkspaceCommitsFromContentConflict covers two incompatible edits to the
// same lines: the plan reports it, and the apply refuses by name.
func (WorkspaceSuite) TestWorkspaceCommitsFromContentConflict(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := commitsFromRepo(t, c, map[string]string{"a.txt": numberedLines(nil)}).AsWorkspace()
	worker := base.
		WithNewFile("a.txt", numberedLines(map[int]string{1: "worker edit"})).
		WithCommit("worker rewrites the top", commitTestDate)
	chief := base.
		WithNewFile("a.txt", numberedLines(map[int]string{1: "chief edit"})).
		WithCommit("chief rewrites the top", commitTestDate)

	plan := planCommitsFrom(ctx, t, chief, worker)
	require.Len(t, plan, 1)
	require.Equal(t, dagger.WorkspaceCommitPickStatusConflict, plan[0].Status)
	require.Equal(t, dagger.WorkspaceCommitPickReasonContent, plan[0].Reason)
	require.Equal(t, []string{"a.txt"}, plan[0].ConflictPaths)

	_, err := chief.WithCommitsFrom(worker).Git().Head().CommitSHA(ctx)
	require.Error(t, err)
	require.ErrorContains(t, err, "a.txt")
	require.ErrorContains(t, err, plan[0].SHA[:7])
	require.ErrorContains(t, err, "worker rewrites the top")

	// The chief's own work is untouched by a refused pull.
	contents, err := chief.File("a.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, numberedLines(map[int]string{1: "chief edit"}), contents)
}

// TestWorkspaceCommitsFromDirtyPathRefusal is git cherry-pick's rule for a
// dirty worktree: the chief's uncommitted work is never swept into a commit
// attributed to the worker.
func (WorkspaceSuite) TestWorkspaceCommitsFromDirtyPathRefusal(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := commitsFromRepo(t, c, map[string]string{"a.txt": numberedLines(nil)}).AsWorkspace()
	worker := base.
		WithNewFile("a.txt", numberedLines(map[int]string{1: "worker edit"})).
		WithCommit("worker edits line 1", commitTestDate)
	// The chief has work in progress on the same file, not committed.
	chief := base.WithNewFile("a.txt", numberedLines(map[int]string{10: "chief wip"}))

	plan := planCommitsFrom(ctx, t, chief, worker)
	require.Len(t, plan, 1)
	require.Equal(t, dagger.WorkspaceCommitPickStatusConflict, plan[0].Status)
	require.Equal(t, dagger.WorkspaceCommitPickReasonDirty, plan[0].Reason)
	require.Equal(t, []string{"a.txt"}, plan[0].ConflictPaths)

	_, err := chief.WithCommitsFrom(worker).Git().Head().CommitSHA(ctx)
	require.Error(t, err)
	require.ErrorContains(t, err, "uncommitted changes")
	require.ErrorContains(t, err, "a.txt")

	contents, err := chief.File("a.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, numberedLines(map[int]string{10: "chief wip"}), contents,
		"the chief's work in progress must be exactly as it was")
	require.Empty(t, stagedCommitsOf(ctx, t, chief))
}

// TestWorkspaceCommitsFromDependentCommitCascades: a commit that builds on a
// skipped one is patched against a tree missing its pre-image, so it conflicts
// on its own. Nothing special-cases it, and both conflicts are reported at once.
func (WorkspaceSuite) TestWorkspaceCommitsFromDependentCommitCascades(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := commitsFromRepo(t, c, map[string]string{"a.txt": "a1\n"}).AsWorkspace()
	worker := base.
		WithNewFile("new.txt", "worker line 1\n").
		WithCommit("worker adds new.txt", commitTestDate).
		WithNewFile("new.txt", "worker line 1\nworker line 2\n").
		WithCommit("worker extends new.txt", commitTestDate)
	chief := base.
		WithNewFile("new.txt", "chief version\n").
		WithCommit("chief adds new.txt", commitTestDate)

	plan := planCommitsFrom(ctx, t, chief, worker)
	require.Equal(t, []dagger.WorkspaceCommitPickStatus{
		dagger.WorkspaceCommitPickStatusConflict,
		dagger.WorkspaceCommitPickStatusConflict,
	}, statuses(plan))

	_, err := chief.WithCommitsFrom(worker).Git().Head().CommitSHA(ctx)
	require.Error(t, err)
	require.ErrorContains(t, err, "2 commit(s) cannot be applied")
	require.ErrorContains(t, err, "worker adds new.txt")
	require.ErrorContains(t, err, "worker extends new.txt")

	contents, err := chief.File("new.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "chief version\n", contents)
}

// TestWorkspaceCommitsFromRedundantHandMerge is the case the reverse-apply
// probe exists for: git refuses an already-applied patch rather than producing
// an identical tree, so without the probe this would read as a conflict.
func (WorkspaceSuite) TestWorkspaceCommitsFromRedundantHandMerge(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := commitsFromRepo(t, c, map[string]string{"a.txt": numberedLines(nil)}).AsWorkspace()
	edited := numberedLines(map[int]string{5: "the same fix"})
	worker := base.
		WithNewFile("a.txt", edited).
		WithCommit("worker fixes line 5", commitTestDate)
	chief := base.
		WithNewFile("a.txt", edited).
		WithCommit("chief fixes line 5, by hand", commitTestDate)

	plan := planCommitsFrom(ctx, t, chief, worker)
	require.Equal(t,
		[]dagger.WorkspaceCommitPickStatus{dagger.WorkspaceCommitPickStatusRedundant},
		statuses(plan))

	pulled := chief.WithCommitsFrom(worker)
	require.Len(t, stagedCommitsOf(ctx, t, pulled), 1, "a redundant commit is a silent no-op")
	contents, err := pulled.File("a.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, edited, contents)
}

// TestWorkspaceCommitsFromOriginIsTransitive: origins collapse to the root, so
// work that travelled A -> B -> C is recognised when C pulls straight from A.
func (WorkspaceSuite) TestWorkspaceCommitsFromOriginIsTransitive(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := commitsFromRepo(t, c, map[string]string{"a.txt": "a1\n"}).AsWorkspace()
	a := base.
		WithNewFile("shared.txt", "written by a\n").
		WithCommit("a's work", commitTestDate)
	aStaged := stagedCommitsOf(ctx, t, a)
	require.Len(t, aStaged, 1)

	b := base.WithCommitsFrom(a)
	bStaged := stagedCommitsOf(ctx, t, b)
	require.Len(t, bStaged, 1)
	require.Equal(t, aStaged[0].SHA, bStaged[0].Origin)

	cWS := base.WithCommitsFrom(b)
	cStaged := stagedCommitsOf(ctx, t, cWS)
	require.Len(t, cStaged, 1)
	require.Equal(t, aStaged[0].SHA, cStaged[0].Origin,
		"the third generation names the root, not its immediate source")

	plan := planCommitsFrom(ctx, t, cWS, a)
	require.Equal(t,
		[]dagger.WorkspaceCommitPickStatus{dagger.WorkspaceCommitPickStatusPicked},
		statuses(plan))
	require.Len(t, stagedCommitsOf(ctx, t, cWS.WithCommitsFrom(a)), 1)
}

// TestWorkspaceCommitsFromPreservesCommitMetadata: the replay is a faithful
// copy of the original commit's identity, not of the receiver's.
func (WorkspaceSuite) TestWorkspaceCommitsFromPreservesCommitMetadata(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	const (
		message  = "worker: do the thing\n\nWith a body that spans\nseveral lines.\n"
		otherDay = "2021-06-07T08:09:10Z"
	)

	chief := commitsFromRepo(t, c, map[string]string{"a.txt": "a1\n"}).AsWorkspace()
	worker := chief.
		WithNewFile("worker.txt", "work\n").
		WithCommit(message, otherDay, dagger.WorkspaceWithCommitOpts{
			AuthorName:  "Worker Bee",
			AuthorEmail: "bee@example.com",
		})

	pulled := chief.WithCommitsFrom(worker)
	staged := stagedCommitsOf(ctx, t, pulled)
	require.Len(t, staged, 1)
	require.Equal(t, message, staged[0].Message)
	require.Equal(t, otherDay, staged[0].Date)
	require.Equal(t, "Worker Bee", staged[0].AuthorName)
	require.Equal(t, "bee@example.com", staged[0].AuthorEmail)
}

// TestWorkspaceCommitsFromSelectsRequestedCommits: cherry-picking by hash, in
// full or by short prefix — and the requested order never overrides the
// source's stack order, so a dependent stack cannot be reordered by accident.
func (WorkspaceSuite) TestWorkspaceCommitsFromSelectsRequestedCommits(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	chief := commitsFromRepo(t, c, map[string]string{"a.txt": "a1\n"}).AsWorkspace()
	worker := chief.
		WithNewFile("first.txt", "first\n").
		WithCommit("first", commitTestDate).
		WithNewFile("second.txt", "second\n").
		WithCommit("second", commitTestDate)
	staged := stagedCommitsOf(ctx, t, worker)
	require.Len(t, staged, 2)

	t.Run("by full hash", func(ctx context.Context, t *testctx.T) {
		plan := planCommitsFrom(ctx, t, chief, worker, staged[1].SHA)
		require.Len(t, plan, 1)
		require.Equal(t, staged[1].SHA, plan[0].SHA)
	})

	t.Run("by short prefix", func(ctx context.Context, t *testctx.T) {
		plan := planCommitsFrom(ctx, t, chief, worker, staged[0].SHA[:7])
		require.Len(t, plan, 1)
		require.Equal(t, staged[0].SHA, plan[0].SHA)
	})

	t.Run("stack order wins over the requested order", func(ctx context.Context, t *testctx.T) {
		plan := planCommitsFrom(ctx, t, chief, worker, staged[1].SHA, staged[0].SHA)
		require.Len(t, plan, 2)
		require.Equal(t, staged[0].SHA, plan[0].SHA)
		require.Equal(t, staged[1].SHA, plan[1].SHA)
	})

	t.Run("applying one leaves the other behind", func(ctx context.Context, t *testctx.T) {
		pulled := chief.WithCommitsFrom(worker, dagger.WorkspaceWithCommitsFromOpts{
			Commits: []string{staged[0].SHA},
		})
		pulledStaged := stagedCommitsOf(ctx, t, pulled)
		require.Len(t, pulledStaged, 1)
		require.Equal(t, staged[0].SHA, pulledStaged[0].Origin)
		_, err := pulled.File("second.txt").Contents(ctx)
		require.Error(t, err)
	})
}

// TestWorkspaceCommitsFromRejectsUnknownCommit: a typo must not read as
// "nothing to do".
func (WorkspaceSuite) TestWorkspaceCommitsFromRejectsUnknownCommit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	chief := commitsFromRepo(t, c, map[string]string{"a.txt": "a1\n"}).AsWorkspace()
	worker := chief.
		WithNewFile("worker.txt", "work\n").
		WithCommit("worker work", commitTestDate)

	_, err := chief.CommitsFrom(ctx, worker, dagger.WorkspaceCommitsFromOpts{
		Commits: []string{"deadbeefdeadbeef"},
	})
	require.ErrorContains(t, err, "deadbeefdeadbeef")

	_, err = chief.WithCommitsFrom(worker, dagger.WorkspaceWithCommitsFromOpts{
		Commits: []string{"deadbeefdeadbeef"},
	}).Git().Head().CommitSHA(ctx)
	require.ErrorContains(t, err, "deadbeefdeadbeef")
}

// TestWorkspaceCommitsFromEmptySource: a worker that never committed is the
// common case, and it is not an error.
func (WorkspaceSuite) TestWorkspaceCommitsFromEmptySource(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	chief := commitsFromRepo(t, c, map[string]string{"a.txt": "a1\n"}).AsWorkspace()
	worker := chief.WithNewFile("scratch.txt", "not committed\n")

	require.Empty(t, planCommitsFrom(ctx, t, chief, worker))

	pulled := chief.WithCommitsFrom(worker)
	require.Empty(t, stagedCommitsOf(ctx, t, pulled))
	_, err := pulled.File("scratch.txt").Contents(ctx)
	require.Error(t, err, "uncommitted work is not what withCommitsFrom moves")
}

// TestWorkspaceCommitsFromPlanIsReadOnly: planning classifies by speculatively
// applying, so the guarantee that it changes nothing is worth pinning down.
func (WorkspaceSuite) TestWorkspaceCommitsFromPlanIsReadOnly(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := commitsFromRepo(t, c, map[string]string{"a.txt": "a1\n"}).AsWorkspace()
	chief := base.
		WithNewFile("chief.txt", "chief\n").
		WithCommit("chief work", commitTestDate)
	worker := base.
		WithNewFile("worker.txt", "worker\n").
		WithCommit("worker work", commitTestDate)

	before := stagedCommitsOf(ctx, t, chief)
	plan := planCommitsFrom(ctx, t, chief, worker)
	require.Equal(t,
		[]dagger.WorkspaceCommitPickStatus{dagger.WorkspaceCommitPickStatusPickable},
		statuses(plan))

	require.Equal(t, before, stagedCommitsOf(ctx, t, chief))
	_, err := chief.File("worker.txt").Contents(ctx)
	require.Error(t, err, "planning must not write the candidate's content")
}

// TestWorkspaceCommitsFromHostCheckout is the one host-backed end-to-end case:
// only this path exercises sparse host reads, exportPendingCommits and the
// BaseHeadSHA invariant. It runs as a single `dagger shell` session because
// that is the only way to bind one workspace to a variable and pass it as an
// argument to another.
func (WorkspaceSuite) TestWorkspaceCommitsFromHostCheckout(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := withCommitBase(t, c).
		// An unrelated pending edit that must survive the pull untouched.
		WithNewFile("b.txt", "b2")

	out := base.With(daggerShell(`
worker=$(current-workspace | with-new-file worker.txt "from the worker" | with-commit "worker work" ` + commitTestDate + ` --author-name Scout --author-email scout@example.com)
current-workspace | with-commits-from --source $worker | export
`))

	_, err := out.Sync(ctx)
	require.NoError(t, err)

	log := gitOut(ctx, t, out, "log", "-1", "--format=%an <%ae>%n%ad%n%s")
	require.Contains(t, log, "Scout <scout@example.com>")
	require.Contains(t, log, "worker work")

	contents, err := out.File("/work/worker.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "from the worker", contents)

	// The chief's unrelated pending edit is still pending, not swept in.
	status := gitOut(ctx, t, out, "status", "--porcelain")
	require.Contains(t, status, "b.txt")
	require.NotContains(t, status, "worker.txt")
}
