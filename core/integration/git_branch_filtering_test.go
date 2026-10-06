package core

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (GitSuite) TestGitBranchFiltering(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	service, url := gitService(ctx, t, c, c.Directory().WithNewFile("base", "base"))
	repo := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: service})
	checkout := repo.Head().Tree()
	ctr := c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithDirectory("/src", checkout).WithWorkdir("/src").With(gitUserConfig).
		WithExec([]string{"git", "checkout", "-b", "feature"}).
		WithNewFile("/src/feature", "feature").
		WithExec([]string{"git", "add", "feature"}).
		WithExec([]string{"git", "commit", "-m", "feature"})
	local := repo.WithContents(ctr.Directory("."))
	localID, err := local.ID(ctx)
	require.NoError(t, err)
	currentID, err := local.Head().ID(ctx)
	require.NoError(t, err)
	baseID, err := repo.Head().ID(ctx)
	require.NoError(t, err)
	query := func(refID dagger.ID) bool {
		var response struct {
			Node struct {
				DefaultRemote struct {
					Name       string
					Repository struct{ Head struct{ Contains bool } }
				}
			}
		}
		require.NoError(t, c.Do(ctx, &dagger.Request{
			Query: `query($repository: ID!, $current: ID!) {
				node(id: $repository) { ... on GitRepository {
					defaultRemote { name repository { head(noLock: true) { contains(other: $current) } } }
				}}
			}`,
			Variables: map[string]any{"repository": localID, "current": refID},
		}, &dagger.Response{Data: &response}))
		require.Equal(t, "origin", response.Node.DefaultRemote.Name)
		return response.Node.DefaultRemote.Repository.Head.Contains
	}
	require.True(t, query(baseID))
	require.False(t, query(currentID), "remote HEAD must not inherit the workspace's feature commit")
	// List and lookup remain metadata-only even for an unreachable registered remote.
	configured := local.WithRemote("offline", "https://invalid.example/repo")
	id, err := configured.ID(ctx)
	require.NoError(t, err)
	var listing struct {
		Node struct{ Remotes []struct{ Name string } }
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{Query: `query($id: ID!) { node(id: $id) { ... on GitRepository { remotes { name } } } }`, Variables: map[string]any{"id": id}}, &dagger.Response{Data: &listing}))
	require.Len(t, listing.Node.Remotes, 2)
	require.Equal(t, "origin", listing.Node.Remotes[0].Name)
	require.Equal(t, "offline", listing.Node.Remotes[1].Name)
	var missing any
	err = c.Do(ctx, &dagger.Request{Query: `query($id: ID!) { node(id: $id) { ... on GitRepository { remote(name: "missing") { name } } } }`, Variables: map[string]any{"id": id}}, &dagger.Response{Data: &missing})
	require.ErrorContains(t, err, "no remote named")
	// The same snapshot preserves the source capability across a pinned HEAD.
	snapshot := local.Head().AsWorkspace().Snapshot()
	snapshotRepoID, err := snapshot.Git().Head().AsRepository().ID(ctx)
	require.NoError(t, err)
	localID = snapshotRepoID
	require.False(t, query(currentID))
	sha, err := snapshot.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, strings.TrimSpace(sha))
}

func (GitSuite) TestGitBranchFilteringRemoteSelectionPersistence(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	fixture := c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).With(gitUserConfig).
		WithWorkdir("/repo").WithExec([]string{"git", "init", "-b", "feature"}).
		WithNewFile("base", "base").WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "base"}).
		WithExec([]string{"git", "remote", "add", "--", "team/trunk", "https://invalid.example/trunk"}).
		WithExec([]string{"git", "remote", "add", "--", "-fork", "https://invalid.example/fork"}).
		WithExec([]string{"git", "config", "branch.feature.remote", "team/trunk"}).
		WithExec([]string{"git", "config", "branch.feature.merge", "refs/heads/main"})
	repo := fixture.Directory(".").AsGit()
	assertGitRemoteSelection(ctx, t, c, repo, []string{"-fork", "team/trunk"}, "team/trunk")
	frozen := repo.Head().AsWorkspace().Snapshot()
	assertGitRemoteSelection(ctx, t, c, frozen.Git().Head().AsRepository(), []string{"-fork", "team/trunk"}, "team/trunk")
	retained := frozen.Git().Head().Tree(dagger.GitRefTreeOpts{Depth: 0})
	assertGitRemoteSelection(ctx, t, c, retained.AsGit(), []string{"-fork", "team/trunk"}, "team/trunk")
	before := repo.Head().Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
	changes := before.WithNewFile("authored", "authored").Changes(before)
	headSHA, err := repo.Head().CommitSHA(ctx)
	require.NoError(t, err)
	for _, derived := range []struct {
		name string
		repo *dagger.GitRepository
	}{
		{"supplied storage", repo.WithContents(retained)},
		{"bundle import", repo.WithBundle(repo.Bundle([]string{"HEAD"})).Ref(headSHA).AsRepository()},
		{"authored commit", repo.Head().WithCommit(changes, "authored", workspaceCommitDate, "Author", "author@example.com").AsRepository()},
	} {
		t.Run(derived.name, func(ctx context.Context, t *testctx.T) {
			assertGitRemoteSelection(ctx, t, c, derived.repo, []string{"-fork", "team/trunk"}, "team/trunk")
			assertGitRemoteSelection(ctx, t, c, derived.repo.Head().Tree(dagger.GitRefTreeOpts{Depth: 0}).AsGit(), []string{"-fork", "team/trunk"}, "team/trunk")
		})
	}
	detached := fixture.WithExec([]string{"git", "checkout", "--detach"}).Directory(".").AsGit()
	assertGitRemoteSelection(ctx, t, c, detached, []string{"-fork", "team/trunk"}, "")
}

func (GitSuite) TestGitBranchFilteringRemoteURLRewrite(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	service, url := gitService(ctx, t, c, c.Directory().WithNewFile("remote", "remote"))
	source := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: service})
	want, err := source.Head().CommitSHA(ctx)
	require.NoError(t, err)
	local := c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).With(gitUserConfig).
		WithWorkdir("/repo").WithExec([]string{"git", "init", "-b", "feature"}).
		WithNewFile("base", "base").WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "base"}).
		WithExec([]string{"git", "config", "url." + url + ".insteadOf", "alias:repo"}).
		WithExec([]string{"git", "remote", "add", "origin", "alias:repo"}).Directory(".")
	remote, err := source.WithContents(local).DefaultRemote(ctx)
	require.NoError(t, err)
	require.NotNil(t, remote)
	got, err := remote.Repository().Head(dagger.GitRepositoryHeadOpts{NoLock: true}).CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, want, got, "the resolved URL must retain access to the source service")
}

func (GitSuite) TestGitBranchFilteringEditedCheckout(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	fixture := c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).With(gitUserConfig).
		WithWorkdir("/repo").WithExec([]string{"git", "init", "-b", "feature"}).
		WithNewFile("base", "base").WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "base"}).
		WithExec([]string{"git", "remote", "add", "trunk", "https://invalid.example/trunk"}).
		WithExec([]string{"git", "remote", "add", "fork", "https://invalid.example/fork"}).
		WithExec([]string{"git", "config", "branch.feature.remote", "trunk"}).
		WithExec([]string{"git", "config", "branch.feature.merge", "refs/heads/main"})
	retained := fixture.Directory(".").AsGit().Head().Tree(dagger.GitRefTreeOpts{Depth: 0})
	checkout := fixture.WithDirectory("/edited", retained).WithWorkdir("/edited")
	assertGitRemoteSelection(ctx, t, c, retained.AsGit(), []string{"fork", "trunk"}, "trunk")
	t.Run("rename", func(ctx context.Context, t *testctx.T) {
		edited := checkout.WithExec([]string{"git", "remote", "rename", "trunk", "renamed"}).Directory(".").AsGit()
		assertGitRemoteSelection(ctx, t, c, edited, []string{"fork", "renamed"}, "")
		frozen := edited.Head().AsWorkspace().Snapshot().Git().Head().AsRepository()
		assertGitRemoteSelection(ctx, t, c, frozen, []string{"fork", "renamed"}, "")
	})
	t.Run("tracking", func(ctx context.Context, t *testctx.T) {
		edited := checkout.WithExec([]string{"git", "checkout", "-B", "feature"}).
			WithExec([]string{"git", "config", "branch.feature.remote", "fork"}).
			WithExec([]string{"git", "config", "branch.feature.merge", "refs/heads/main"}).Directory(".").AsGit()
		assertGitRemoteSelection(ctx, t, c, edited, []string{"fork", "trunk"}, "fork")
	})
	t.Run("detach", func(ctx context.Context, t *testctx.T) {
		detached := checkout.WithExec([]string{"git", "checkout", "--detach"}).Directory(".").AsGit()
		assertGitRemoteSelection(ctx, t, c, detached, []string{"fork", "trunk"}, "trunk")
	})
}

func (GitSuite) TestGitBranchFilteringShallow(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	fixture := c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).With(gitUserConfig).
		WithWorkdir("/repo").WithExec([]string{"git", "init", "-b", "main"}).
		WithNewFile("base", "base").WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "base"})
	base := fixture.Directory(".").AsGit().Head()
	fixture = fixture.WithNewFile("tip", "tip").WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "tip"})
	service, url := gitService(ctx, t, c, fixture.Directory("."))
	repo := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: service})
	shallow := repo.Head().Tree()
	boundaries, err := shallow.File(".git/shallow").Contents(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, boundaries)
	// A Directory alone grants no remote access, even with an origin URL.
	_, err = shallow.AsGit().Head().Contains(ctx, base)
	require.ErrorContains(t, err, "git ancestry requires complete history")
	// An explicit source capability can supply the omitted ancestry.
	contains, err := repo.WithContents(shallow).Head().Contains(ctx, base)
	require.NoError(t, err)
	require.True(t, contains)
	after, err := shallow.File(".git/shallow").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, boundaries, after, "history completion must not mutate the supplied checkout")
}

func (GitSuite) TestGitBranchFilteringApprovedHostHistory(ctx context.Context, t *testctx.T) {
	fixture := newWorkspaceHostHistoryFixture(ctx, t)
	c := fixture.client
	fixture.git("remote", "rename", "origin", "trunk")
	fixture.git("remote", "add", "fork", "https://invalid.example/fork")
	base := c.CurrentWorkspace().Snapshot()
	baseID, err := base.Git().Head().ID(ctx)
	require.NoError(t, err)
	head := fixture.commit(ctx, t)
	assertGitRemoteSelection(ctx, t, c, head.AsRepository(), []string{"fork", "trunk"}, "trunk")
	// Force complete-history promotion through the approved host checkout.
	// Its config must not replace the selection captured by the workspace.
	fixture.origin.Close()
	var response struct {
		Node struct {
			Base struct {
				AsGit struct{ ID dagger.ID }
			}
		}
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query: `query($id: ID!) {
			node(id: $id) { ... on GitRef {
				base: __nativeCommitBase(depth: 0) { asGit { id } }
			}}
		}`,
		Variables: map[string]any{"id": baseID},
	}, &dagger.Response{Data: &response}))
	promoted := dagger.Ref[*dagger.GitRepository](c, response.Node.Base.AsGit.ID)
	assertGitRemoteSelection(ctx, t, c, promoted, []string{"fork", "trunk"}, "trunk")
	commits, err := head.Log(ctx, dagger.GitRefLogOpts{Limit: 100})
	require.NoError(t, err)
	headSHA, err := head.CommitSHA(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, append([]string{headSHA}, fixture.shas...), workspaceRemoteHistorySHAs(ctx, t, commits))
	retained := head.Tree(dagger.GitRefTreeOpts{Depth: -1})
	assertGitRemoteSelection(ctx, t, c, retained.AsGit(), []string{"fork", "trunk"}, "trunk")
	require.NoError(t, c.Close())
	_, full := workspaceRemoteHistoryFetches(fixture.sink, "")
	require.Empty(t, full, "approved host history must avoid a full remote fetch")
	names, _ := workspaceRemoteHistoryTrace(fixture.sink)
	imported := false
	for _, name := range names {
		imported = imported || name == "git import approved host commit closure"
	}
	require.True(t, imported, "complete-history promotion must use the approved checkout")
}

func (GitSuite) TestGitBranchFilteringNativeHistory(ctx context.Context, t *testctx.T) {
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithLogOutput(io.Discard))...)
	fixture := newWorkspaceRemoteHistoryFixture(ctx, t, c)
	url, err := fixture.repo.URL(ctx)
	require.NoError(t, err)
	id, err := fixture.repo.ID(ctx)
	require.NoError(t, err)
	remotes, err := json.Marshal([]map[string]string{
		{"name": "trunk", "url": url},
		{"name": "fork", "url": "https://invalid.example/fork"},
	})
	require.NoError(t, err)
	// Host captures use this selector to preserve a tracked remote without
	// adding an implicit origin. Exercise that metadata on the new remote
	// native commit path, which owns only the selected commit at first.
	var response struct {
		Node struct {
			Selection struct {
				ID   dagger.ID
				Head struct {
					Base struct {
						AsGit struct{ ID dagger.ID }
					}
				}
			}
		}
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query: `query($id: ID!, $remotes: String!) {
			node(id: $id) { ... on GitRepository {
				selection: __withRemoteSelection(remotes: $remotes, upstreamRemote: "trunk") {
					id head { base: __nativeCommitBase(depth: 1) { asGit { id } } }
				}
			}}
		}`,
		Variables: map[string]any{"id": id, "remotes": string(remotes)},
	}, &dagger.Response{Data: &response}))
	repo := dagger.Ref[*dagger.GitRepository](c, response.Node.Selection.ID)
	assertGitRemoteSelection(ctx, t, c, dagger.Ref[*dagger.GitRepository](c, response.Node.Selection.Head.Base.AsGit.ID), []string{"fork", "trunk"}, "trunk")
	anchor := repo.Head()
	first := workspaceRemoteHistoryCommit(ctx, t, c, anchor.AsWorkspace(), 1)
	second := workspaceRemoteHistoryCommit(ctx, t, c, first, 2)
	assertGitRemoteSelection(ctx, t, c, second.Git().Head().AsRepository(), []string{"fork", "trunk"}, "trunk")
	assertGitRemoteSelection(ctx, t, c, second.Git().Head().Tree().AsGit(), []string{"fork", "trunk"}, "trunk")
	// Removing the served repository makes a full history fetch fail even
	// if the service is restarted. Comparisons above the anchor stay local.
	_, err = fixture.server.WithExec([]string{"rm", "-rf", "/srv/repo.git"}).Sync(ctx)
	require.NoError(t, err)
	contains, err := anchor.Contains(ctx, second.Git().Head())
	require.NoError(t, err)
	require.False(t, contains)
	contains, err = second.Git().Head().Contains(ctx, anchor)
	require.NoError(t, err)
	require.True(t, contains)
	contains, err = first.Git().Head().Contains(ctx, second.Git().Head())
	require.NoError(t, err)
	require.False(t, contains)
	contains, err = second.Git().Head().Contains(ctx, first.Git().Head())
	require.NoError(t, err)
	require.True(t, contains)
	require.NoError(t, c.Close())
	shallow, full := workspaceRemoteHistoryFetches(sink, "")
	require.NotEmpty(t, shallow, "the source must start with a shallow capture")
	require.Empty(t, full, "freezing, committing and comparing native workspace commits must not hydrate older history")
}

func assertGitRemoteSelection(ctx context.Context, t *testctx.T, c *dagger.Client, repo *dagger.GitRepository, names []string, selected string) {
	t.Helper()
	id, err := repo.ID(ctx)
	require.NoError(t, err)
	var result struct {
		Node struct {
			Remotes       []struct{ Name string }
			DefaultRemote *struct{ Name string }
		}
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!) { node(id: $id) { ... on GitRepository { remotes { name } defaultRemote { name } } } }`,
		Variables: map[string]any{"id": id},
	}, &dagger.Response{Data: &result}))
	var got []string
	for _, remote := range result.Node.Remotes {
		got = append(got, remote.Name)
	}
	require.Equal(t, names, got)
	if selected == "" {
		require.Nil(t, result.Node.DefaultRemote)
	} else {
		require.NotNil(t, result.Node.DefaultRemote)
		require.Equal(t, selected, result.Node.DefaultRemote.Name)
	}
}

func (GitSuite) TestGitBranchFilteringPrivateSnapshot(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	repos := makeGitDir(c, c.Directory().WithNewFile("base", "base"), "main")
	service, _ := httpServiceDirAuth(ctx, t, c, "", repos.WithDirectory("other.git", repos.Directory("repo.git")), "", c.SetSecret("remote-password", "foobar"))
	service, err := service.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := service.Stop(context.WithoutCancel(ctx))
		require.NoError(t, err)
	})
	hostname, err := service.Hostname(ctx)
	require.NoError(t, err)
	baseURL := "http://" + resolveServiceIP(ctx, t, c, hostname)
	url := baseURL + "/repo.git"
	repo := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: service, HTTPAuthToken: c.SetSecret("source-token", "foobar")})
	current := repo.Head()
	// Reconstruct storage and freeze the workspace before following the remote.
	frozen := repo.WithContents(current.Tree()).Head().AsWorkspace().Snapshot()
	remote, err := frozen.Git().Head().AsRepository().DefaultRemote(ctx)
	require.NoError(t, err)
	require.NotNil(t, remote)
	contains, err := remote.Repository().Head(dagger.GitRepositoryHeadOpts{NoLock: true}).Contains(ctx, current)
	require.NoError(t, err)
	require.True(t, contains)
	// Even on the same server, another repository does not inherit the token.
	other := frozen.Git().Head().AsRepository().WithRemote("other", baseURL+"/other.git").Remote("other").Repository()
	_, err = other.Head(dagger.GitRepositoryHeadOpts{NoLock: true}).CommitSHA(ctx)
	require.Error(t, err)
	requireErrOut(t, err, "authentication failed")
}

func (WorkspaceSuite) TestWorkspaceGitRemoteSelectionCapture(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = workdir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
	}
	git("init", "-b", "feature")
	git("config", "user.name", "Remote Selection")
	git("config", "user.email", "selection@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "base"), []byte("base"), 0o600))
	git("add", ".")
	git("commit", "-m", "base")
	remote := filepath.Join(t.TempDir(), "remote.git")
	git("clone", "--bare", workdir, remote)
	git("remote", "add", "fork", remote)
	git("remote", "add", "trunk", remote)
	git("config", "branch.feature.remote", "trunk")
	git("config", "branch.feature.merge", "refs/heads/feature")
	c := connect(ctx, t, dagger.WithWorkdir(workdir))
	live := c.CurrentWorkspace()
	assertGitRemoteSelection(ctx, t, c, live.Git().Head().AsRepository(), []string{"fork", "trunk"}, "trunk")
	frozen := snapshotWorkspace(ctx, t, c, live)
	assertGitRemoteSelection(ctx, t, c, frozen.Git().Head().AsRepository(), []string{"fork", "trunk"}, "trunk")
	git("config", "branch.feature.remote", "fork")
	assertGitRemoteSelection(ctx, t, c, frozen.Git().Head().AsRepository(), []string{"fork", "trunk"}, "trunk")
	assertGitRemoteSelection(ctx, t, c, snapshotWorkspace(ctx, t, c, live).Git().Head().AsRepository(), []string{"fork", "trunk"}, "fork")
}
