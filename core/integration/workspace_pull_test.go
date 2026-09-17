package core

import (
	"context"
	"fmt"
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
		Node struct{ CompareCommitsFrom []workspacePullPlanEntry }
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
		Query:     `query($id: ID!, $source: ID!, $commits: [String!]!, $max: Int!) { node(id: $id) { ... on Workspace { compareCommitsFrom(source: $source, commits: $commits, maxCommits: $max) { commit { sha message } status reason conflictPaths } } } }`,
		Variables: map[string]any{"id": id, "source": sourceID, "commits": commits, "max": maxCommits},
	}, &dagger.Response{Data: &result})
	return result.Node.CompareCommitsFrom, err
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
	base := snapshotWorkspace(ctx, t, c, c.Git(url, dagger.GitOpts{ExperimentalServiceHost: service}).Branch("main").AsWorkspace())
	source := base.WithNewFile("source.txt", "source").With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "source commit", workspaceCommitDate)
	}).WithNewFile("ignored.txt", "source WIP")
	receiver := base.WithNewFile("pending.txt", "receiver WIP").
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
	// New commits after a pull resolve the calling client's Git identity.
	next := pulled.WithCommit(pulled.Git().Uncommitted(), "save WIP", workspaceCommitDate)
	name, err := next.Git().Head().TargetCommit().AuthorName(ctx)
	require.NoError(t, err)
	require.Equal(t, "Dagger", name)
}

func (WorkspaceSuite) TestWorkspacePullCherryPick(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	git("config", "user.name", "Source")
	git("config", "user.email", "source@example.com")
	sourceClient := connect(ctx, t, dagger.WithWorkdir(checkout))
	sourceID, err := snapshotWorkspace(ctx, t, sourceClient, sourceClient.CurrentWorkspace()).WithNewFile("from-source.txt", "source").With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "source", workspaceCommitDate)
	}).ID(ctx)
	require.NoError(t, err)

	git("config", "user.name", "Receiver")
	git("config", "user.email", "receiver@example.com")
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	base := snapshotWorkspace(ctx, t, c, c.CurrentWorkspace())
	source := dagger.Ref[*dagger.Workspace](c, sourceID)
	receiver := base.WithNewFile("local.txt", "local").With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "local", workspaceCommitDate)
	})
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
	sourceMessage, err := source.Git().Head().TargetCommit().Message(ctx)
	require.NoError(t, err)
	require.Equal(t, sourceMessage, message)
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
	require.Equal(t, "REDUNDANT", plan[0].Status)
	again, err := applyWorkspacePull(ctx, c, pulled, source, nil, 100)
	require.NoError(t, err)
	againSHA, err := again.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, sha, againSHA)
	// A different recipe with equivalent inputs still produces the same SHA.
	equivalent, err := applyWorkspacePull(ctx, c, receiver.WithConfigEnvironment(""), source, nil, 100)
	require.NoError(t, err)
	equivalentSHA, err := equivalent.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, sha, equivalentSHA)
	require.NotEqual(t, oldSHA, git("rev-parse", "HEAD"))
	require.Empty(t, git("status", "--porcelain"))
	// The pull remains compatible with export, landing both real commits.
	require.NoError(t, saveWorkspaceTo(ctx, c, pulled, nil, checkout))
	require.Equal(t, sha, git("rev-parse", "HEAD"))
}

func (WorkspaceSuite) TestWorkspacePullConflictsAndRedundancy(ctx context.Context, t *testctx.T) {
	checkout, _ := workspaceExportCheckout(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	base := snapshotWorkspace(ctx, t, c, c.CurrentWorkspace())
	source := base.WithNewFile("base.txt", "source").With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "conflicting", workspaceCommitDate)
	}).
		WithNewFile("independent.txt", "independent").With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "independent", workspaceCommitDate)
	})
	for _, dirty := range []bool{true, false} {
		receiver := base.WithNewFile("base.txt", "local")
		if !dirty {
			receiver = receiver.WithCommit(receiver.Git().Uncommitted(), "local", workspaceCommitDate)
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
	same := base.WithNewFile("base.txt", "source").With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "equivalent local", workspaceCommitDate)
	})
	plan, err := planWorkspacePull(ctx, c, same, source, nil, 100)
	require.NoError(t, err)
	require.Equal(t, "REDUNDANT", plan[0].Status)
	require.Equal(t, "PICKABLE", plan[1].Status)
	// An empty-directory overlay must not hide a newly committed file.
	emptyDir := base.WithChanges(c.Directory().WithNewDirectory("empty").Changes(c.Directory()))
	dirtyPaths, err := emptyDir.Git().Uncommitted().AddedPaths(ctx)
	require.NoError(t, err)
	require.Contains(t, dirtyPaths, "empty/")
	incoming := base.WithNewFile("empty", "incoming file").With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "replace empty directory", workspaceCommitDate)
	})
	plan, err = planWorkspacePull(ctx, c, emptyDir, incoming, nil, 100)
	require.NoError(t, err)
	require.Len(t, plan, 1)
	require.Equal(t, "CONFLICT", plan[0].Status)
	require.Equal(t, "DIRTY", plan[0].Reason)
	require.Equal(t, []string{"empty"}, plan[0].ConflictPaths)
	_, err = applyWorkspacePull(ctx, c, emptyDir, incoming, nil, 100)
	require.ErrorContains(t, err, "DIRTY conflict on empty")
}

func (WorkspaceSuite) TestWorkspacePullShortSHAs(ctx context.Context, t *testctx.T) {
	checkout, _ := workspaceExportCheckout(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	base := snapshotWorkspace(ctx, t, c, c.CurrentWorkspace())
	source := base.WithNewFile("a", "a").With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "a", workspaceCommitDate)
	})
	a, err := source.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	source = source.WithNewFile("b", "b").With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "b", workspaceCommitDate)
	})
	b, err := source.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)

	for _, commits := range [][]string{{b, a}, {b[:7], a[:7]}, {b, a[:12]}} {
		plan, err := planWorkspacePull(ctx, c, base, source, commits, 100)
		require.NoError(t, err)
		require.Len(t, plan, 2)
		require.Equal(t, a, plan[0].Commit.SHA)
		require.Equal(t, b, plan[1].Commit.SHA)
		for _, pick := range plan {
			require.Equal(t, "PICKABLE", pick.Status)
		}
		pulled, err := applyWorkspacePull(ctx, c, base, source, commits, 100)
		require.NoError(t, err)
		sha, err := pulled.Git().Head().CommitSHA(ctx)
		require.NoError(t, err)
		require.Equal(t, b, sha)
	}

	// Sparse selection cherry-picks only b; a prefix and a full hash produce
	// the same persisted recipe, not just equivalent checkout contents.
	var recipes []dagger.ID
	for _, selection := range []string{b, b[:7]} {
		plan, err := planWorkspacePull(ctx, c, base, source, []string{selection}, 100)
		require.NoError(t, err)
		require.Len(t, plan, 1)
		require.Equal(t, b, plan[0].Commit.SHA)
		pulled, err := applyWorkspacePull(ctx, c, base, source, []string{selection}, 100)
		require.NoError(t, err)
		exists, err := pulled.Directory("/").Exists(ctx, "a")
		require.NoError(t, err)
		require.False(t, exists)
		contents, err := pulled.File("b").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "b", contents)
		recipe, err := c.LLM().WithWorkspace(pulled).PortableID(ctx)
		require.NoError(t, err)
		recipes = append(recipes, recipe)
		var id call.ID
		require.NoError(t, id.Decode(string(recipe)))
		require.Contains(t, id.Display(), b)
		require.NotContains(t, id.Display(), fmt.Sprintf("%q", b[:7]))
		restored := dagger.Ref[*dagger.LLM](c, recipe).Workspace()
		contents, err = restored.File("b").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "b", contents)
	}
	require.Equal(t, recipes[0], recipes[1])

	for _, tc := range []struct {
		commits []string
		max     int
		want    string
	}{
		{[]string{a, a[:7]}, 100, "duplicate selected commit " + a},
		{[]string{a[:7], a}, 100, "duplicate selected commit " + a},
		{[]string{a[:7], a[:12]}, 100, "duplicate selected commit " + a},
		{[]string{a[:7], a[:7]}, 100, "duplicate selected commit"},
		{[]string{a, a}, 100, "duplicate selected commit"},
		{[]string{"abcdef123456789"}, 100, "no commit matches short SHA"},
		{[]string{strings.Repeat("f", 40)}, 100, "not within the source"},
		{[]string{"HEAD"}, 100, "lowercase"},
		{[]string{"abc"}, 100, "lowercase"},
		{[]string{"ABCD"}, 100, "lowercase"},
		{[]string{a[:7], b[:7]}, 1, "selected commits exceed maxCommits"},
	} {
		_, err := planWorkspacePull(ctx, c, base, source, tc.commits, tc.max)
		require.ErrorContains(t, err, tc.want)
		_, err = applyWorkspacePull(ctx, c, base, source, tc.commits, tc.max)
		require.ErrorContains(t, err, tc.want)
	}
}

func (WorkspaceSuite) TestWorkspacePullAmbiguousSHA(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	baseSHA := git("rev-parse", "HEAD")
	tree := git("rev-parse", "HEAD^{tree}")
	// Manufacture two reachable commits sharing a prefix. This exercises Git's
	// object disambiguation rather than assuming that a prefix is unique in a
	// bounded history listing. Only the two colliding commits enter the source.
	seen := map[string]string{}
	var first, second string
	for i := 0; ; i++ {
		require.Less(t, i, 5000, "no 4-character prefix collision found")
		sha := git("commit-tree", tree, "-p", baseSHA, "-m", fmt.Sprintf("collision-%d", i))
		if sha[:4] == baseSHA[:4] {
			continue
		}
		if other, ok := seen[sha[:4]]; ok {
			first, second = other, sha
			break
		}
		seen[sha[:4]] = sha
	}
	var tip string
	for i := 0; ; i++ {
		require.Less(t, i, 100, "merge tip repeatedly collides")
		tip = git("commit-tree", tree, "-p", first, "-p", second, "-m", fmt.Sprintf("collision merge-%d", i))
		if tip[:4] != first[:4] {
			break
		}
	}
	git("reset", "--hard", tip)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	source := snapshotWorkspace(ctx, t, c, c.CurrentWorkspace())
	base := source.WithReset(baseSHA, dagger.WorkspaceWithResetOpts{Hard: true})
	prefix := first[:4]
	_, err := planWorkspacePull(ctx, c, base, source, []string{prefix}, 100)
	require.ErrorContains(t, err, "ambiguous short SHA")
	require.ErrorContains(t, err, prefix)
	require.ErrorContains(t, err, first)
	require.ErrorContains(t, err, second)
	_, err = applyWorkspacePull(ctx, c, base, source, []string{prefix}, 100)
	require.ErrorContains(t, err, "ambiguous short SHA")
	require.ErrorContains(t, err, prefix)
	// A full hash remains usable even though its short form is ambiguous.
	plan, err := planWorkspacePull(ctx, c, base, source, []string{first}, 100)
	require.NoError(t, err)
	require.Len(t, plan, 1)
	require.Equal(t, first, plan[0].Commit.SHA)
	pulled, err := applyWorkspacePull(ctx, c, base, source, []string{first}, 100)
	require.NoError(t, err)
	sha, err := pulled.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, first, sha)
}

func (WorkspaceSuite) TestWorkspacePullSelectionAndLimits(ctx context.Context, t *testctx.T) {
	checkout, _ := workspaceExportCheckout(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	base := snapshotWorkspace(ctx, t, c, c.CurrentWorkspace())
	source := base.WithNewFile("a", "a").With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "a", workspaceCommitDate)
	})
	a, err := source.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	source = source.WithNewFile("b", "b").With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "b", workspaceCommitDate)
	})
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
	require.NoError(t, err)
	_, err = applyWorkspacePull(ctx, c, base, c.CurrentWorkspace(), nil, 100)
	require.ErrorContains(t, err, "call snapshot")
	// Public GitRef.log still rejects zero: pulling doesn't require unlimited history.
	// The SDK omits zero-valued optional ints, so use a direct query.
	baseID, err := base.ID(ctx)
	require.NoError(t, err)
	err = c.Do(ctx, &dagger.Request{Query: `query($id: ID!) { node(id:$id) { ... on Workspace { git { head { log(limit:0) { sha } } } } } }`, Variables: map[string]any{"id": baseID}}, &dagger.Response{})
	require.ErrorContains(t, err, "at least 1")
}
