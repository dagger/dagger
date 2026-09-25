package core

import (
	"context"
	"fmt"
	"io"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestWorkspaceRemoteParentHistoryDoesNotFetch(ctx context.Context, t *testctx.T) {
	if runWithPrivateTraceSession(ctx, t) {
		return
	}
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithLogOutput(io.Discard))...)
	service, url := gitService(ctx, t, c, c.Directory().WithNewFile("file.txt", "base\n"))
	remote := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: service}).Head()
	base := remote.AsWorkspace()
	working := base.WithNewFile("file.txt", "committed\n")
	id, err := working.WithCommit(working.Git().Uncommitted(), "remote child", workspaceCommitDate, dagger.WorkspaceWithCommitOpts{AuthorName: "Oracle", AuthorEmail: "oracle@example.com"}).ID(ctx)
	require.NoError(t, err)
	child := dagger.Ref[*dagger.Workspace](c, id)
	head := child.Git().Head()
	remoteSHA, err := remote.CommitSHA(ctx)
	require.NoError(t, err)
	_, err = service.Stop(ctx)
	require.NoError(t, err)

	ahead, err := head.Log(ctx, dagger.GitRefLogOpts{Base: remote, Limit: 101})
	require.NoError(t, err)
	require.Len(t, ahead, 1)
	parents, err := ahead[0].ParentShas(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{remoteSHA}, parents)
	behind, err := remote.Log(ctx, dagger.GitRefLogOpts{Base: head, Limit: 101})
	require.NoError(t, err)
	require.Empty(t, behind)
	// A different tree selector forces another incremental materialization;
	// its canonical parent dependency must work without restarting the source.
	contents, err := head.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true, Depth: -1}).File("file.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "committed\n", contents)
	gotSHA, err := remote.CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, remoteSHA, gotSHA, "operation-local substitution must not mutate public refs")
	require.NoError(t, c.Close())

	traces, _ := sink.capture()
	names, parentIDs := map[string]string{}, map[string]string{}
	for _, request := range traces {
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if span.EndTimeUnixNano <= span.StartTimeUnixNano {
						continue
					}
					id := string(span.TraceId) + string(span.SpanId)
					names[id], parentIDs[id] = span.Name, string(span.TraceId)+string(span.ParentSpanId)
				}
			}
		}
	}
	walks := 0
	for id, name := range names {
		for parent := parentIDs[id]; parent != ""; parent = parentIDs[parent] {
			if names[parent] != "GitRef.log" {
				continue
			}
			require.False(t, strings.HasPrefix(name, "git fetch") || strings.HasPrefix(name, "fetching "), "exact remote/local parent comparison fetched: %s", name)
			require.NotEqual(t, "service.start", name, "history comparison restarted the stopped remote service")
			if strings.HasPrefix(name, "git rev-list") && names[parentIDs[id]] != name {
				walks++
			}
			break
		}
	}
	require.GreaterOrEqual(t, walks, 2, "both ahead and behind must actually traverse history")
}

// Each fixture has its own URL and mirror: one demand test must not warm the
// history that a different test is supposed to fetch lazily. Everything is
// created in containers; no host workspace capture participates in the commits.
type workspaceRemoteHistoryFixture struct {
	repo    *dagger.GitRepository
	oracle  *dagger.Container
	server  *dagger.Container
	service *dagger.Service
}

func newWorkspaceRemoteHistoryFixture(ctx context.Context, t *testctx.T, c *dagger.Client) workspaceRemoteHistoryFixture {
	t.Helper()
	oracle := c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git", "git-daemon"}).
		WithEnvVariable("GIT_AUTHOR_DATE", workspaceCommitDate).
		WithEnvVariable("GIT_COMMITTER_DATE", workspaceCommitDate).
		WithNewFile("/fixture-id", identity.NewID()).
		WithWorkdir("/repo").
		WithExec([]string{"sh", "-ec", `
 git init -b main
 git config user.name Oracle
 git config user.email oracle@example.com
 cp /fixture-id fixture-id
 echo root > ancient.txt
 echo base > selected.txt
 git add . && git commit -m root
 echo old > ancient.txt
 git commit -am old
 git tag old
 git checkout -b side
 echo side > side.txt
 git add . && git commit -m side
 git checkout main
 echo main > main.txt
 git add . && git commit -m main
 git merge --no-ff side -m merge
 git tag merge
 echo tip > tip.txt
 git add . && git commit -m tip
 git checkout -b divergent old
 echo divergent > divergent.txt
 git add . && git commit -m divergent
 git checkout main
 git clone --bare . /seed.git
`})
	// The sentinel survives service restarts. Removing repo.git below therefore
	// makes the remote genuinely unavailable, unlike merely stopping a service
	// which the Git backend is allowed to restart automatically.
	server := oracle.WithMountedCache("/srv", c.CacheVolume(identity.NewID())).
		WithExec([]string{"sh", "-ec", "if [ ! -e /srv/initialized ]; then cp -a /seed.git /srv/repo.git; touch /srv/initialized; fi"})
	service := server.WithExposedPort(9418).
		WithDefaultArgs([]string{"git", "daemon", "--verbose", "--export-all", "--base-path=/srv"}).AsService()
	host, err := service.Hostname(ctx)
	require.NoError(t, err)
	return workspaceRemoteHistoryFixture{
		repo:   c.Git("git://"+host+"/repo.git", dagger.GitOpts{ExperimentalServiceHost: service}),
		oracle: oracle, server: server, service: service,
	}
}

func (f workspaceRemoteHistoryFixture) git(ctx context.Context, t *testctx.T, args ...string) string {
	t.Helper()
	out, err := f.oracle.WithExec(append([]string{"git"}, args...)).Stdout(ctx)
	require.NoError(t, err)
	return strings.TrimSpace(out)
}

func workspaceRemoteHistoryCommit(ctx context.Context, t *testctx.T, c *dagger.Client, ws *dagger.Workspace, n int) *dagger.Workspace {
	t.Helper()
	working := ws.WithNewFile("selected.txt", fmt.Sprintf("local %d\n", n))
	id, err := working.WithCommit(working.Git().Uncommitted(), fmt.Sprintf("local %d", n), workspaceCommitDate,
		dagger.WorkspaceWithCommitOpts{AuthorName: "Oracle", AuthorEmail: "oracle@example.com"}).ID(ctx)
	require.NoError(t, err)
	return dagger.Ref[*dagger.Workspace](c, id)
}

func workspaceRemoteHistorySHAs(ctx context.Context, t *testctx.T, commits []dagger.GitCommit) []string {
	t.Helper()
	shas := make([]string, len(commits))
	for i, commit := range commits {
		sha, err := commit.Sha(ctx)
		require.NoError(t, err)
		shas[i] = sha
	}
	return shas
}

// Inspect commands, not the generic "fetching" wrapper: that wrapper also
// surrounds the initial depth-one capture, which is explicitly allowed. Only
// count remote fetches, not a local object transfer into a comparison repo.
func workspaceRemoteHistoryFetches(sink *agentTraceSink, ancestor string) (shallow, full []string) {
	traces, _ := sink.capture()
	names, parents := map[string]string{}, map[string]string{}
	for _, request := range traces {
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if span.EndTimeUnixNano <= span.StartTimeUnixNano {
						continue
					}
					id := string(span.TraceId) + string(span.SpanId)
					names[id], parents[id] = span.Name, string(span.TraceId)+string(span.ParentSpanId)
				}
			}
		}
	}
	for id, name := range names {
		if !strings.HasPrefix(name, "git fetch ") {
			continue
		}
		remote, inScope := false, ancestor == ""
		for parent := parents[id]; parent != ""; parent = parents[parent] {
			remote = remote || strings.HasPrefix(names[parent], "fetching git://")
			inScope = inScope || names[parent] == ancestor
		}
		if !remote || !inScope {
			continue
		}
		if strings.Contains(name, "--depth=") && !strings.Contains(name, "--unshallow") {
			shallow = append(shallow, name)
		} else {
			full = append(full, name)
		}
	}
	return
}

func (WorkspaceSuite) TestWorkspaceRemoteLazyHistoryOrdinary(ctx context.Context, t *testctx.T) {
	if runWithPrivateTraceSession(ctx, t) {
		return
	}
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithLogOutput(io.Discard))...)
	fixture := newWorkspaceRemoteHistoryFixture(ctx, t, c)
	before := fixture.repo.Head()
	ws := before.AsWorkspace()
	for n := 1; n <= 2; n++ {
		parentSHA, err := before.CommitSHA(ctx)
		require.NoError(t, err)
		ws = workspaceRemoteHistoryCommit(ctx, t, c, ws, n)
		head := ws.Git().Head()
		headSHA, err := head.CommitSHA(ctx)
		require.NoError(t, err)
		// Materialize both the public source and the committed tree. An ID-only
		// test can miss an eager fetch deferred until directory consumption.
		for _, file := range []*dagger.File{ws.File("selected.txt"), head.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true}).File("selected.txt")} {
			contents, err := file.Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, fmt.Sprintf("local %d\n", n), contents)
		}
		empty, err := ws.Git().Uncommitted().IsEmpty(ctx)
		require.NoError(t, err)
		require.True(t, empty)
		recent, err := head.Log(ctx, dagger.GitRefLogOpts{Limit: 2})
		require.NoError(t, err)
		require.Equal(t, []string{headSHA, parentSHA}, workspaceRemoteHistorySHAs(ctx, t, recent))
		parents, err := recent[0].ParentShas(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{parentSHA}, parents)
		ahead, err := head.Log(ctx, dagger.GitRefLogOpts{Base: before, Limit: 101})
		require.NoError(t, err)
		require.Equal(t, []string{headSHA}, workspaceRemoteHistorySHAs(ctx, t, ahead))
		behind, err := before.Log(ctx, dagger.GitRefLogOpts{Base: head, Limit: 101})
		require.NoError(t, err)
		require.Empty(t, behind)
		before = head
	}
	require.NoError(t, c.Close())
	shallow, full := workspaceRemoteHistoryFetches(sink, "")
	require.NotEmpty(t, shallow, "must observe the real shallow remote capture, not a warmed mirror")
	require.Empty(t, full, "ordinary commit/source/status/short-log/parent comparisons must not hydrate history")
}

func (WorkspaceSuite) TestWorkspaceRemoteLazyHistoryDemand(ctx context.Context, t *testctx.T) {
	if runWithPrivateTraceSession(ctx, t) {
		return
	}
	for _, demand := range []string{"deep log", "old path", "older comparison", "divergent comparison", "retained checkout"} {
		t.Run(demand, func(ctx context.Context, t *testctx.T) {
			sink := newAgentTraceSink(t)
			c := connect(ctx, t, append(sink.clientOpts(), dagger.WithLogOutput(io.Discard))...)
			fixture := newWorkspaceRemoteHistoryFixture(ctx, t, c)
			ws := workspaceRemoteHistoryCommit(ctx, t, c, fixture.repo.Head().AsWorkspace(), 1)
			ws = workspaceRemoteHistoryCommit(ctx, t, c, ws, 2)
			head := ws.Git().Head()
			headSHA, err := head.CommitSHA(ctx)
			require.NoError(t, err)
			parents, err := head.TargetCommit().ParentShas(ctx)
			require.NoError(t, err)
			require.Len(t, parents, 1)
			localSHAs := []string{headSHA, parents[0]}
			remoteSHAs := strings.Fields(fixture.git(ctx, t, "rev-list", "main"))
			var commits []dagger.GitCommit
			switch demand {
			case "deep log":
				commits, err = head.Log(ctx, dagger.GitRefLogOpts{Limit: 100})
				require.NoError(t, err)
				require.ElementsMatch(t, append(localSHAs, remoteSHAs...), workspaceRemoteHistorySHAs(ctx, t, commits))
				mergeSHA := fixture.git(ctx, t, "rev-parse", "merge")
				for _, commit := range commits {
					sha, err := commit.Sha(ctx)
					require.NoError(t, err)
					if sha == mergeSHA {
						parents, err := commit.ParentShas(ctx)
						require.NoError(t, err)
						require.Equal(t, strings.Fields(fixture.git(ctx, t, "show", "-s", "--format=%P", "merge")), parents)
					}
				}
			case "old path":
				commits, err = head.Log(ctx, dagger.GitRefLogOpts{Limit: 100, Paths: []string{"ancient.txt"}})
				require.NoError(t, err)
				require.Equal(t, strings.Fields(fixture.git(ctx, t, "rev-list", "main", "--", "ancient.txt")), workspaceRemoteHistorySHAs(ctx, t, commits))
			case "older comparison", "divergent comparison":
				name := "old"
				if demand == "divergent comparison" {
					name = "divergent"
				}
				other := fixture.repo.Ref(name)
				commits, err = head.Log(ctx, dagger.GitRefLogOpts{Base: other, Limit: 100})
				require.NoError(t, err)
				expected := append(localSHAs, strings.Fields(fixture.git(ctx, t, "rev-list", "main", "^"+name))...)
				require.ElementsMatch(t, expected, workspaceRemoteHistorySHAs(ctx, t, commits))
				behind, err := other.Log(ctx, dagger.GitRefLogOpts{Base: head, Limit: 100})
				require.NoError(t, err)
				require.ElementsMatch(t, strings.Fields(fixture.git(ctx, t, "rev-list", name, "^main")), workspaceRemoteHistorySHAs(ctx, t, behind))
			case "retained checkout":
				// A new container sees only this directory, not the engine's
				// mirror/object pool. fsck and old-tree access must work offline.
				tree, err := head.Tree(dagger.GitRefTreeOpts{Depth: -1}).Sync(ctx)
				require.NoError(t, err)
				_, err = fixture.service.Stop(ctx)
				require.NoError(t, err)
				out, err := c.Container().From(alpineImage).WithExec([]string{"apk", "add", "git"}).
					WithDirectory("/checkout", tree).WithWorkdir("/checkout").
					WithExec([]string{"sh", "-ec", `
 test "$(git rev-parse --is-shallow-repository)" = false
 test ! -s .git/objects/info/alternates
 git fsck --full --no-dangling >&2
 git show HEAD:ancient.txt | grep -qx old
 git show "$(git rev-list --max-parents=0 HEAD):ancient.txt" | grep -qx root
 git rev-list HEAD
`}).Stdout(ctx)
				require.NoError(t, err)
				require.ElementsMatch(t, append(localSHAs, remoteSHAs...), strings.Fields(out))
			}
			if demand == "deep log" || demand == "old path" {
				oldSHA := fixture.git(ctx, t, "rev-parse", "old")
				require.Contains(t, workspaceRemoteHistorySHAs(ctx, t, commits), oldSHA)
				for _, commit := range commits {
					sha, err := commit.Sha(ctx)
					require.NoError(t, err)
					if sha == oldSHA {
						message, err := commit.MessageHeadline(ctx)
						require.NoError(t, err)
						require.Equal(t, "old", message)
						contents, err := commit.Tree(dagger.GitCommitTreeOpts{DiscardGitDir: true}).File("ancient.txt").Contents(ctx)
						require.NoError(t, err)
						require.Equal(t, "old\n", contents)
					}
				}
				for _, selector := range []string{oldSHA, oldSHA[:12]} {
					sha, err := head.AsRepository().Ref(selector).CommitSHA(ctx)
					require.NoError(t, err)
					require.Equal(t, oldSHA, sha)
				}
			}
			require.NoError(t, c.Close())
			shallow, full := workspaceRemoteHistoryFetches(sink, "")
			require.NotEmpty(t, shallow, "fixture must begin as a real shallow remote capture")
			require.NotEmpty(t, full, "history-demanding operation must actually hydrate the shallow history")
			operation := "GitRef.log"
			if demand == "retained checkout" {
				operation = "GitRef.tree"
			}
			_, demandFetches := workspaceRemoteHistoryFetches(sink, operation)
			require.NotEmpty(t, demandFetches, "hydration must happen inside the requesting operation")
			_, commitFetches := workspaceRemoteHistoryFetches(sink, "Workspace.withCommit")
			require.Empty(t, commitFetches, "hydration must be deferred past native commit creation")
		})
	}
}

func (WorkspaceSuite) TestWorkspaceRemoteLazyHistoryUnavailable(ctx context.Context, t *testctx.T) {
	if runWithPrivateTraceSession(ctx, t) {
		return
	}
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithLogOutput(io.Discard))...)
	fixture := newWorkspaceRemoteHistoryFixture(ctx, t, c)
	ws := workspaceRemoteHistoryCommit(ctx, t, c, fixture.repo.Head().AsWorkspace(), 1)
	_, err := fixture.server.WithExec([]string{"rm", "-rf", "/srv/repo.git"}).Sync(ctx)
	require.NoError(t, err)
	// Native descendants remain usable without the origin. Use another tree
	// selector to exercise source materialization, rather than only a cached
	// directory read. Even explicit full depth means no history for source-only
	// consumption. History beyond the boundary must fail, not return a prefix.
	firstSHA, err := ws.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	ws = workspaceRemoteHistoryCommit(ctx, t, c, ws, 2)
	head := ws.Git().Head()
	headSHA, err := head.CommitSHA(ctx)
	require.NoError(t, err)
	contents, err := head.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true, Depth: -1}).File("selected.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "local 2\n", contents)
	recent, err := head.Log(ctx, dagger.GitRefLogOpts{Limit: 2})
	require.NoError(t, err)
	require.Equal(t, []string{headSHA, firstSHA}, workspaceRemoteHistorySHAs(ctx, t, recent))
	_, err = head.Log(ctx, dagger.GitRefLogOpts{Limit: 100})
	require.Error(t, err, "unavailable ancestry must not silently truncate deep history")
	_, err = ws.Git().Head().Log(ctx, dagger.GitRefLogOpts{Paths: []string{"ancient.txt"}, Limit: 100})
	require.Error(t, err, "unavailable ancestry must not silently omit old path matches")
	require.NoError(t, c.Close())
	_, full := workspaceRemoteHistoryFetches(sink, "GitRef.log")
	require.NotEmpty(t, full, "failure must come from attempting the deferred remote fetch")
	_, commitFetches := workspaceRemoteHistoryFetches(sink, "Workspace.withCommit")
	require.Empty(t, commitFetches, "native descendants must commit without hydrating the unavailable origin")
}
