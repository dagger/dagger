package core

import (
	"context"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type workspacePullPlanEntry struct {
	Commit         struct{ SHA, Message string }
	Status, Reason string
	ConflictPaths  []string
}

func planWorkspacePull(ctx context.Context, c *dagger.Client, receiver, source *dagger.Workspace, commits []string, maxCommits int) ([]workspacePullPlanEntry, error) {
	var result struct {
		Node struct{ CommitsFrom []workspacePullPlanEntry }
	}
	id, err := receiver.ID(ctx)
	if err != nil {
		return nil, err
	}
	sourceID, err := source.ID(ctx)
	if err != nil {
		return nil, err
	}
	if commits == nil {
		commits = []string{}
	}
	err = c.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!, $source: ID!, $commits: [String!]!, $max: Int!) { node(id: $id) { ... on Workspace { commitsFrom(source: $source, commits: $commits, maxCommits: $max) { commit { sha message } status reason conflictPaths } } } }`,
		Variables: map[string]any{"id": id, "source": sourceID, "commits": commits, "max": maxCommits},
	}, &dagger.Response{Data: &result})
	return result.Node.CommitsFrom, err
}

func applyWorkspacePull(ctx context.Context, c *dagger.Client, receiver, source *dagger.Workspace, commits []string, maxCommits int) (*dagger.Workspace, error) {
	var result struct {
		Node struct{ WithCommitsFrom struct{ ID dagger.ID } }
	}
	id, err := receiver.ID(ctx)
	if err != nil {
		return nil, err
	}
	sourceID, err := source.ID(ctx)
	if err != nil {
		return nil, err
	}
	if commits == nil {
		commits = []string{}
	}
	err = c.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!, $source: ID!, $commits: [String!]!, $max: Int!) { node(id: $id) { ... on Workspace { withCommitsFrom(source: $source, commits: $commits, maxCommits: $max) { id } } } }`,
		Variables: map[string]any{"id": id, "source": sourceID, "commits": commits, "max": maxCommits},
	}, &dagger.Response{Data: &result})
	if err != nil {
		return nil, err
	}
	return dagger.Ref[*dagger.Workspace](c, result.Node.WithCommitsFrom.ID), nil
}

func (WorkspaceSuite) TestWorkspacePullFastForward(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	service, url := gitService(ctx, t, c, c.Directory().WithNewFile("base.txt", "base"))
	base := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: service}).Branch("main").AsWorkspace().Checkpoint()
	source := base.WithNewFile("source.txt", "source").WithCommit("source commit", workspaceCommitDate).WithNewFile("ignored.txt", "source WIP")
	receiver := base.WithGitAuthor("Receiver", "receiver@example.com").WithNewFile("pending.txt", "receiver WIP").
		WithMountedDirectory("mount", c.Directory().WithNewFile("mounted.txt", "mount"))
	sourceSHA, err := source.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	baseSHA, err := receiver.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	plan, err := planWorkspacePull(ctx, c, receiver, source, nil, 100)
	require.NoError(t, err)
	require.Len(t, plan, 1)
	require.Equal(t, sourceSHA, plan[0].Commit.SHA)
	require.Equal(t, "PICKABLE", plan[0].Status)
	require.Equal(t, "NONE", plan[0].Reason)
	require.Empty(t, plan[0].ConflictPaths)
	pulled, err := applyWorkspacePull(ctx, c, receiver, source, nil, 100)
	require.NoError(t, err)
	sha, err := pulled.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, sourceSHA, sha)
	for path, want := range map[string]string{"source.txt": "source", "pending.txt": "receiver WIP", "mount/mounted.txt": "mount"} {
		got, err := pulled.File(path).Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	exists, err := pulled.Directory("/").Exists(ctx, "ignored.txt")
	require.NoError(t, err)
	require.False(t, exists)
	remaining, err := pulled.Git().Uncommitted().AddedPaths(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"pending.txt"}, remaining)
	sha, err = receiver.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, baseSHA, sha, "planning/applying must not mutate the receiver")
	plan, err = planWorkspacePull(ctx, c, pulled, source, nil, 100)
	require.NoError(t, err)
	require.Empty(t, plan)
	portable, err := pulled.Portable(ctx)
	require.NoError(t, err)
	require.True(t, portable)
	// The returned composition has pinned refs, not a mutable branch lookup.
	recipe, err := c.LLM().WithWorkspace(pulled).PortableID(ctx)
	require.NoError(t, err)
	var id call.ID
	require.NoError(t, id.Decode(string(recipe)))
	require.NotContains(t, id.Display(), "branch(name:")
	restored := dagger.Ref[*dagger.LLM](c, recipe).Workspace()
	sha, err = restored.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, sourceSHA, sha)
	// Receiver identity survives the pull and becomes the next commit author.
	next := pulled.WithCommit("save WIP", workspaceCommitDate)
	name, err := next.Git().Head().TargetCommit().AuthorName(ctx)
	require.NoError(t, err)
	require.Equal(t, "Receiver", name)
}

func (WorkspaceSuite) TestWorkspacePullCherryPick(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	base := c.CurrentWorkspace().Checkpoint()
	source := base.WithGitAuthor("Source", "source@example.com").WithNewFile("from-source.txt", "source").WithCommit("source", workspaceCommitDate)
	receiver := base.WithNewFile("local.txt", "local").WithCommit("local", workspaceCommitDate).WithGitAuthor("Receiver", "receiver@example.com")
	sourceSHA, err := source.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	oldSHA, err := receiver.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	plan, err := planWorkspacePull(ctx, c, receiver, source, nil, 100)
	require.NoError(t, err)
	require.Equal(t, "PICKABLE", plan[0].Status)
	pulled, err := applyWorkspacePull(ctx, c, receiver, source, nil, 100)
	require.NoError(t, err)
	sha, err := pulled.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.NotEqual(t, sourceSHA, sha)
	commit := pulled.Git().Head().TargetCommit()
	message, err := commit.Message(ctx)
	require.NoError(t, err)
	require.Contains(t, message, "(cherry picked from commit "+sourceSHA+")")
	author, err := commit.AuthorName(ctx)
	require.NoError(t, err)
	require.Equal(t, "Source", author)
	committer, err := commit.CommitterName(ctx)
	require.NoError(t, err)
	require.Equal(t, "Receiver", committer)
	date, err := commit.CommittedDate(ctx)
	require.NoError(t, err)
	require.Equal(t, workspaceCommitDate, date)
	plan, err = planWorkspacePull(ctx, c, pulled, source, nil, 100)
	require.NoError(t, err)
	require.Equal(t, "PICKED", plan[0].Status)
	again, err := applyWorkspacePull(ctx, c, pulled, source, nil, 100)
	require.NoError(t, err)
	againSHA, err := again.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, sha, againSHA)
	// A different recipe with equivalent inputs still produces the same SHA.
	equivalent, err := applyWorkspacePull(ctx, c, receiver.WithGitAuthor("Receiver", "receiver@example.com"), source, nil, 100)
	require.NoError(t, err)
	equivalentSHA, err := equivalent.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, sha, equivalentSHA)
	require.NotEqual(t, oldSHA, git("rev-parse", "HEAD"))
	require.Empty(t, git("status", "--porcelain"))
	// The pull remains compatible with export, landing both real commits.
	require.NoError(t, exportWorkspace(ctx, c, pulled, c.CurrentWorkspace()))
	require.Equal(t, sha, git("rev-parse", "HEAD"))
}

func (WorkspaceSuite) TestWorkspacePullConflictsAndRedundancy(ctx context.Context, t *testctx.T) {
	checkout, _ := workspaceExportCheckout(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	base := c.CurrentWorkspace().Checkpoint()
	source := base.WithNewFile("base.txt", "source").WithCommit("conflicting", workspaceCommitDate).
		WithNewFile("independent.txt", "independent").WithCommit("independent", workspaceCommitDate)
	for _, dirty := range []bool{true, false} {
		receiver := base.WithNewFile("base.txt", "local")
		if !dirty {
			receiver = receiver.WithCommit("local", workspaceCommitDate)
		}
		original, err := receiver.Git().Head().CommitSHA(ctx)
		require.NoError(t, err)
		plan, err := planWorkspacePull(ctx, c, receiver, source, nil, 100)
		require.NoError(t, err)
		require.Len(t, plan, 2)
		require.Equal(t, "CONFLICT", plan[0].Status)
		want := "CONTENT"
		if dirty {
			want = "DIRTY"
		}
		require.Equal(t, want, plan[0].Reason)
		require.Equal(t, []string{"base.txt"}, plan[0].ConflictPaths)
		require.Equal(t, "PICKABLE", plan[1].Status)
		_, err = applyWorkspacePull(ctx, c, receiver, source, nil, 100)
		require.ErrorContains(t, err, plan[0].Commit.SHA)
		require.ErrorContains(t, err, "base.txt")
		sha, err := receiver.Git().Head().CommitSHA(ctx)
		require.NoError(t, err)
		require.Equal(t, original, sha)
		contents, err := receiver.File("base.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "local", contents)
	}
	same := base.WithNewFile("base.txt", "source").WithCommit("equivalent local", workspaceCommitDate)
	plan, err := planWorkspacePull(ctx, c, same, source, nil, 100)
	require.NoError(t, err)
	require.Equal(t, "REDUNDANT", plan[0].Status)
	require.Equal(t, "PICKABLE", plan[1].Status)
	// An empty-directory overlay must not hide a newly committed file.
	emptyDir := base.WithChanges(c.Directory().WithNewDirectory("empty").Changes(c.Directory()))
	dirtyPaths, err := emptyDir.Git().Uncommitted().AddedPaths(ctx)
	require.NoError(t, err)
	require.Contains(t, dirtyPaths, "empty/")
	incoming := base.WithNewFile("empty", "incoming file").WithCommit("replace empty directory", workspaceCommitDate)
	plan, err = planWorkspacePull(ctx, c, emptyDir, incoming, nil, 100)
	require.NoError(t, err)
	require.Len(t, plan, 1)
	require.Equal(t, "CONFLICT", plan[0].Status)
	require.Equal(t, "DIRTY", plan[0].Reason)
	require.Equal(t, []string{"empty"}, plan[0].ConflictPaths)
	_, err = applyWorkspacePull(ctx, c, emptyDir, incoming, nil, 100)
	require.ErrorContains(t, err, "DIRTY conflict on empty")
}

func (WorkspaceSuite) TestWorkspacePullSelectionAndLimits(ctx context.Context, t *testctx.T) {
	checkout, _ := workspaceExportCheckout(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	base := c.CurrentWorkspace().Checkpoint()
	source := base.WithNewFile("a", "a").WithCommit("a", workspaceCommitDate)
	a, err := source.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	source = source.WithNewFile("b", "b").WithCommit("b", workspaceCommitDate)
	b, err := source.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	plan, err := planWorkspacePull(ctx, c, base, source, []string{b, a}, 100)
	require.NoError(t, err)
	require.Equal(t, a, plan[0].Commit.SHA)
	require.Equal(t, b, plan[1].Commit.SHA)
	pulled, err := applyWorkspacePull(ctx, c, base, source, []string{b}, 100)
	require.NoError(t, err)
	exists, err := pulled.Directory("/").Exists(ctx, "a")
	require.NoError(t, err)
	require.False(t, exists)
	for _, max := range []int{0, 1, 1001} {
		_, err := planWorkspacePull(ctx, c, base, source, nil, max)
		require.ErrorContains(t, err, "maxCommits")
		_, err = applyWorkspacePull(ctx, c, base, source, nil, max)
		require.ErrorContains(t, err, "maxCommits")
	}
	_, err = planWorkspacePull(ctx, c, base, source, []string{strings.Repeat("a", 40)}, 100)
	require.ErrorContains(t, err, "not within the source")
	_, err = planWorkspacePull(ctx, c, c.CurrentWorkspace(), source, nil, 100)
	require.ErrorContains(t, err, "call checkpoint")
	_, err = applyWorkspacePull(ctx, c, base, c.CurrentWorkspace(), nil, 100)
	require.ErrorContains(t, err, "call checkpoint")
	// Public GitRef.log still rejects zero: pulling doesn't require unlimited history.
	// The SDK omits zero-valued optional ints, so use a direct query.
	baseID, err := base.ID(ctx)
	require.NoError(t, err)
	err = c.Do(ctx, &dagger.Request{Query: `query($id: ID!) { node(id:$id) { ... on Workspace { git { head { log(limit:0) { sha } } } } } }`, Variables: map[string]any{"id": baseID}}, &dagger.Response{})
	require.ErrorContains(t, err, "at least 1")
}
