package core

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger/core"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestWorkspaceRemoteParentHistoryDoesNotFetch(ctx context.Context, t *testctx.T) {
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithLogOutput(io.Discard))...)
	service, url := gitService(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("file.txt", "base\n"))
	remote := core.NewQuery(c).Git(url, core.GitOpts{ExperimentalServiceHost: service}).Head()
	base := remote.AsWorkspace()
	working := base.WithNewFile("file.txt", "committed\n")
	id, err := working.WithCommit(working.Git().Uncommitted(), "remote child", workspaceCommitDate, core.WorkspaceWithCommitOpts{AuthorName: "Oracle", AuthorEmail: "oracle@example.com"}).ID(ctx)
	require.NoError(t, err)
	child := core.Ref[*core.Workspace](core.NewQuery(c), id)
	head := child.Git().Head()
	remoteSHA, err := remote.CommitSHA(ctx)
	require.NoError(t, err)
	_, err = service.Stop(ctx)
	require.NoError(t, err)

	ahead, err := head.Log(ctx, core.GitRefLogOpts{Base: remote, Limit: 101})
	require.NoError(t, err)
	require.Len(t, ahead, 1)
	parents, err := ahead[0].ParentShas(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{remoteSHA}, parents)
	behind, err := remote.Log(ctx, core.GitRefLogOpts{Base: head, Limit: 101})
	require.NoError(t, err)
	require.Empty(t, behind)
	// A different tree selector forces another incremental materialization;
	// its canonical parent dependency must work without restarting the source.
	contents, err := head.Tree(core.GitRefTreeOpts{DiscardGitDir: true, Depth: -1}).File("file.txt").Contents(ctx)
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

// Committing on a remote workspace turns it into owned storage holding only
// its own (detached) history. Other names still resolve through the remote it
// came from, with that remote's bindings (here, the service that makes git://
// reachable), while HEAD stays the local commit.
func (WorkspaceSuite) TestWorkspaceRemoteCommitResolvesUpstreamRefs(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	fixture := newWorkspaceRemoteHistoryFixture(ctx, t, c)
	ws := workspaceRemoteHistoryCommit(ctx, t, c, fixture.repo.Head().AsWorkspace(), 1)
	head := ws.Git().Head()
	headSHA, err := head.CommitSHA(ctx)
	require.NoError(t, err)
	repo := head.AsRepository()

	for _, name := range []string{"main", "divergent", "side", "old", "refs/heads/side"} {
		want := fixture.git(ctx, t, "rev-parse", name+"^{commit}")
		got, err := repo.Ref(name).CommitSHA(ctx)
		require.NoError(t, err, name)
		require.Equal(t, want, got, name)
	}
	contents, err := repo.Ref("divergent").Tree(core.GitRefTreeOpts{DiscardGitDir: true}).File("divergent.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "divergent\n", contents)

	// Local names never reach the remote.
	for _, name := range []string{"HEAD", headSHA} {
		got, err := repo.Ref(name).CommitSHA(ctx)
		require.NoError(t, err, name)
		require.Equal(t, headSHA, got, name)
	}
	// The remote-resolved base is comparable with the local commit, as a
	// rebase onto it needs.
	base, err := head.CommonAncestor(repo.Ref("main")).CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, fixture.git(ctx, t, "rev-parse", "main"), base)

	// Revisions of a remote-only name walk the remote's history, and SHAs read
	// from remote-resolved refs can be passed back in: the storage lacks those
	// commits, so they resolve through the remote too.
	divergentSHA := fixture.git(ctx, t, "rev-parse", "divergent")
	for name, want := range map[string]string{
		"divergent^0":       divergentSHA,
		"divergent~1":       fixture.git(ctx, t, "rev-parse", "old"),
		divergentSHA:        divergentSHA,
		divergentSHA + "~1": fixture.git(ctx, t, "rev-parse", "old"),
	} {
		got, err := repo.Ref(name).CommitSHA(ctx)
		require.NoError(t, err, name)
		require.Equal(t, want, got, name)
	}
	contents, err = repo.Ref(divergentSHA).Tree(core.GitRefTreeOpts{DiscardGitDir: true}).File("divergent.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "divergent\n", contents)
	contents, err = repo.Commit(divergentSHA).Tree(core.GitCommitTreeOpts{DiscardGitDir: true}).File("divergent.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "divergent\n", contents)

	// Listings include the names ref resolves through the remote.
	branches, err := repo.Branches(ctx)
	require.NoError(t, err)
	require.Subset(t, branches, []string{"main", "divergent", "side"})
	tags, err := repo.Tags(ctx)
	require.NoError(t, err)
	require.Subset(t, tags, []string{"old", "merge"})
	tags, err = repo.Tags(ctx, core.GitRepositoryTagsOpts{Patterns: []string{"refs/tags/m*"}})
	require.NoError(t, err)
	require.Equal(t, []string{"merge"}, tags)

	_, err = repo.Ref("missing").CommitSHA(ctx)
	require.ErrorContains(t, err, `ref "missing" not found locally`)
	require.ErrorContains(t, err, `does not contain ref "missing"`)
}

// Each fixture has its own URL and mirror: one demand test must not warm the
// history that a different test is supposed to fetch lazily. Everything is
// created in containers; no host workspace capture participates in the commits.
type workspaceRemoteHistoryFixture struct {
	repo    *core.GitRepository
	oracle  *core.Container
	server  *core.Container
	service *core.Service
}

func newWorkspaceRemoteHistoryFixture(ctx context.Context, t *testctx.T, c *dagger.Client) workspaceRemoteHistoryFixture {
	t.Helper()
	oracle := core.NewQuery(c).Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git", "git-daemon"}).
		WithEnvVariable("GIT_AUTHOR_DATE", workspaceCommitDate).
		WithEnvVariable("GIT_COMMITTER_DATE", workspaceCommitDate).
		WithNewFile("/fixture-id", rand.Text()).
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
	server := oracle.WithMountedCache("/srv", core.NewQuery(c).CacheVolume(rand.Text())).
		WithExec([]string{"sh", "-ec", "if [ ! -e /srv/initialized ]; then cp -a /seed.git /srv/repo.git; touch /srv/initialized; fi"})
	service := server.WithExposedPort(9418).
		WithDefaultArgs([]string{"git", "daemon", "--verbose", "--export-all", "--base-path=/srv"}).AsService()
	host, err := service.Hostname(ctx)
	require.NoError(t, err)
	return workspaceRemoteHistoryFixture{
		repo:   core.NewQuery(c).Git("git://"+host+"/repo.git", core.GitOpts{ExperimentalServiceHost: service}),
		oracle: oracle, server: server, service: service,
	}
}

func (f workspaceRemoteHistoryFixture) git(ctx context.Context, t *testctx.T, args ...string) string {
	t.Helper()
	out, err := f.oracle.WithExec(append([]string{"git"}, args...)).Stdout(ctx)
	require.NoError(t, err)
	return strings.TrimSpace(out)
}

func workspaceRemoteHistoryCommit(ctx context.Context, t *testctx.T, c *dagger.Client, ws *core.Workspace, n int) *core.Workspace {
	t.Helper()
	working := ws.WithNewFile("selected.txt", fmt.Sprintf("local %d\n", n))
	id, err := working.WithCommit(working.Git().Uncommitted(), fmt.Sprintf("local %d", n), workspaceCommitDate,
		core.WorkspaceWithCommitOpts{AuthorName: "Oracle", AuthorEmail: "oracle@example.com"}).ID(ctx)
	require.NoError(t, err)
	return core.Ref[*core.Workspace](core.NewQuery(c), id)
}

func workspaceRemoteHistorySHAs(ctx context.Context, t *testctx.T, commits []core.GitCommit) []string {
	t.Helper()
	shas := make([]string, len(commits))
	for i, commit := range commits {
		sha, err := commit.Sha(ctx)
		require.NoError(t, err)
		shas[i] = sha
	}
	return shas
}

func workspaceRemoteHistoryTrace(sink *agentTraceSink) (names, parents map[string]string) {
	traces, _ := sink.capture()
	names, parents = map[string]string{}, map[string]string{}
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
	return
}

// Inspect commands, not the generic "fetching" wrapper: that wrapper also
// surrounds the initial depth-one capture, which is explicitly allowed. Only
// count remote fetches, not a local object transfer into a comparison repo.
func workspaceRemoteHistoryFetches(sink *agentTraceSink, ancestor string) (shallow, full []string) {
	names, parents := workspaceRemoteHistoryTrace(sink)
	for id, name := range names {
		if !strings.HasPrefix(name, "git fetch ") {
			continue
		}
		remote, inScope := false, ancestor == ""
		for parent := parents[id]; parent != ""; parent = parents[parent] {
			remote = remote || strings.HasPrefix(names[parent], "fetching git://") || strings.HasPrefix(names[parent], "fetching http://")
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
	return shallow, full
}

func (WorkspaceSuite) TestWorkspaceRemoteLazyHistoryOrdinary(ctx context.Context, t *testctx.T) {
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithLogOutput(io.Discard))...)
	fixture := newWorkspaceRemoteHistoryFixture(ctx, t, c)
	remote := fixture.repo.Head()
	before := remote
	var local []string
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
		for _, file := range []*core.File{ws.File("selected.txt"), head.Tree(core.GitRefTreeOpts{DiscardGitDir: true}).File("selected.txt")} {
			contents, err := file.Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, fmt.Sprintf("local %d\n", n), contents)
		}
		empty, err := ws.Git().Uncommitted().IsEmpty(ctx)
		require.NoError(t, err)
		require.True(t, empty)
		recent, err := head.Log(ctx, core.GitRefLogOpts{Limit: 2})
		require.NoError(t, err)
		require.Equal(t, []string{headSHA, parentSHA}, workspaceRemoteHistorySHAs(ctx, t, recent))
		parents, err := recent[0].ParentShas(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{parentSHA}, parents)
		ahead, err := head.Log(ctx, core.GitRefLogOpts{Base: before, Limit: 101})
		require.NoError(t, err)
		require.Equal(t, []string{headSHA}, workspaceRemoteHistorySHAs(ctx, t, ahead))
		behind, err := before.Log(ctx, core.GitRefLogOpts{Base: head, Limit: 101})
		require.NoError(t, err)
		require.Empty(t, behind)
		local = append([]string{headSHA}, local...)
		// Against the original remote ref, the range spans every local commit
		// and is still answered from the owned store, in both directions.
		ahead, err = head.Log(ctx, core.GitRefLogOpts{Base: remote, Limit: 101})
		require.NoError(t, err)
		require.Equal(t, local, workspaceRemoteHistorySHAs(ctx, t, ahead))
		behind, err = remote.Log(ctx, core.GitRefLogOpts{Base: head, Limit: 101})
		require.NoError(t, err)
		require.Empty(t, behind)
		if n == 2 {
			// Requested checkout depth is public behavior, independent of the
			// owned snapshot's remote-anchor boundary. Truncate the exported
			// graph at HEAD without rewriting its raw parent metadata.
			out, err := core.NewQuery(c).Container().From(alpineImage).WithExec([]string{"apk", "add", "git"}).
				WithDirectory("/checkout", head.Tree(core.GitRefTreeOpts{Depth: 1})).WithWorkdir("/checkout").
				WithEnvVariable("HEAD_SHA", headSHA).WithEnvVariable("PARENT_SHA", parentSHA).
				WithExec([]string{"sh", "-ec", `
 test "$(git rev-parse --is-shallow-repository)" = true
 test "$(cat .git/shallow)" = "$HEAD_SHA"
 test ! -s .git/objects/info/alternates
 git cat-file -p HEAD | grep -qx "parent $PARENT_SHA"
 git fsck --full --no-dangling >&2
 git rev-list HEAD
`}).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{headSHA}, strings.Fields(out))
		}
		before = head
	}
	require.NoError(t, c.Close())
	shallow, full := workspaceRemoteHistoryFetches(sink, "")
	require.NotEmpty(t, shallow, "must observe the real shallow remote capture, not a warmed mirror")
	require.Empty(t, full, "ordinary commit/source/status/short-log/parent comparisons must not hydrate history")
}

func (WorkspaceSuite) TestWorkspaceRemoteLazyHistoryDemand(ctx context.Context, t *testctx.T) {
	for _, demand := range []string{"deep log", "old path", "older comparison", "divergent comparison", "retained checkout", "full bundle"} {
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
			var commits []core.GitCommit
			switch demand {
			case "deep log":
				commits, err = head.Log(ctx, core.GitRefLogOpts{Limit: 100})
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
				commits, err = head.Log(ctx, core.GitRefLogOpts{Limit: 100, Paths: []string{"ancient.txt"}})
				require.NoError(t, err)
				require.Equal(t, strings.Fields(fixture.git(ctx, t, "rev-list", "main", "--", "ancient.txt")), workspaceRemoteHistorySHAs(ctx, t, commits))
			case "older comparison", "divergent comparison":
				name := "old"
				if demand == "divergent comparison" {
					name = "divergent"
				}
				other := fixture.repo.Ref(name)
				commits, err = head.Log(ctx, core.GitRefLogOpts{Base: other, Limit: 100})
				require.NoError(t, err)
				expected := append([]string(nil), localSHAs...)
				expected = append(expected, strings.Fields(fixture.git(ctx, t, "rev-list", "main", "^"+name))...)
				require.ElementsMatch(t, expected, workspaceRemoteHistorySHAs(ctx, t, commits))
				behind, err := other.Log(ctx, core.GitRefLogOpts{Base: head, Limit: 100})
				require.NoError(t, err)
				require.ElementsMatch(t, strings.Fields(fixture.git(ctx, t, "rev-list", name, "^main")), workspaceRemoteHistorySHAs(ctx, t, behind))
			case "full bundle":
				bundle, err := head.AsRepository().Bundle([]string{"HEAD"}).AsFile().Sync(ctx)
				require.NoError(t, err)
				_, err = fixture.server.WithExec([]string{"rm", "-rf", "/srv/repo.git"}).Sync(ctx)
				require.NoError(t, err)
				// A full bundle must have no prerequisites or engine-local
				// alternates: a completely independent clone can verify and use it.
				out, err := core.NewQuery(c).Container().From(alpineImage).WithExec([]string{"apk", "add", "git"}).
					WithMountedFile("/export.bundle", bundle).
					WithExec([]string{"git", "clone", "/export.bundle", "/checkout"}).
					WithWorkdir("/checkout").
					WithExec([]string{"sh", "-ec", `
 git bundle verify /export.bundle >&2
 test "$(git rev-parse --is-shallow-repository)" = false
 test ! -s .git/objects/info/alternates
 git fsck --full --no-dangling >&2
 git show "$(git rev-list --max-parents=0 HEAD):ancient.txt" | grep -qx root
 git rev-list HEAD
`}).Stdout(ctx)
				require.NoError(t, err)
				require.ElementsMatch(t, append(localSHAs, remoteSHAs...), strings.Fields(out))
			case "retained checkout":
				// A new container sees only this directory, not the engine's
				// mirror/object pool. fsck and old-tree access must work offline.
				tree, err := head.Tree(core.GitRefTreeOpts{Depth: -1}).Sync(ctx)
				require.NoError(t, err)
				_, err = fixture.service.Stop(ctx)
				require.NoError(t, err)
				out, err := core.NewQuery(c).Container().From(alpineImage).WithExec([]string{"apk", "add", "git"}).
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
						contents, err := commit.Tree(core.GitCommitTreeOpts{DiscardGitDir: true}).File("ancient.txt").Contents(ctx)
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
			if demand == "deep log" {
				_, err := fixture.server.WithExec([]string{"rm", "-rf", "/srv/repo.git"}).Sync(ctx)
				require.NoError(t, err)
				// Different API selectors avoid reusing the original log response.
				// This proves offline reuse after hydration, not engine GC/restart:
				// the private mirror and owned hydrated snapshot are still warm.
				again, err := head.Log(ctx, core.GitRefLogOpts{Limit: 101})
				require.NoError(t, err)
				require.ElementsMatch(t, append(localSHAs, remoteSHAs...), workspaceRemoteHistorySHAs(ctx, t, again))
				out, err := core.NewQuery(c).Container().From(alpineImage).WithExec([]string{"apk", "add", "git"}).
					WithDirectory("/checkout", head.Tree(core.GitRefTreeOpts{Depth: -1})).WithWorkdir("/checkout").
					WithExec([]string{"sh", "-ec", `
 test "$(git rev-parse --is-shallow-repository)" = false
 test ! -s .git/objects/info/alternates
 git fsck --full --no-dangling >&2
 git show "$(git rev-list --max-parents=0 HEAD):ancient.txt" | grep -qx root
 git rev-list HEAD
`}).Stdout(ctx)
				require.NoError(t, err)
				require.ElementsMatch(t, append(localSHAs, remoteSHAs...), strings.Fields(out))
			}
			require.NoError(t, c.Close())
			if demand == "deep log" {
				names, _ := workspaceRemoteHistoryTrace(sink)
				hydrations := 0
				for _, name := range names {
					if name == "git hydrate owned history" {
						hydrations++
					}
				}
				require.Equal(t, 1, hydrations, "repeat consumers must reuse the owned hydrated closure, not rebuild it from the warm mirror")
			}
			shallow, full := workspaceRemoteHistoryFetches(sink, "")
			require.NotEmpty(t, shallow, "fixture must begin as a real shallow remote capture")
			require.NotEmpty(t, full, "history-demanding operation must actually hydrate the shallow history")
			operation := "GitRef.log"
			switch demand {
			case "retained checkout":
				operation = "GitRef.tree"
			case "full bundle":
				operation = "GitRepository.bundle"
			}
			_, demandFetches := workspaceRemoteHistoryFetches(sink, operation)
			require.NotEmpty(t, demandFetches, "hydration must happen inside the requesting operation")
			_, commitFetches := workspaceRemoteHistoryFetches(sink, "Workspace.withCommit")
			require.Empty(t, commitFetches, "hydration must be deferred past native commit creation")
		})
	}
}

func (WorkspaceSuite) TestWorkspaceRemoteLazyHistoryUnavailable(ctx context.Context, t *testctx.T) {
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
	contents, err := head.Tree(core.GitRefTreeOpts{DiscardGitDir: true, Depth: -1}).File("selected.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "local 2\n", contents)
	recent, err := head.Log(ctx, core.GitRefLogOpts{Limit: 2})
	require.NoError(t, err)
	require.Equal(t, []string{headSHA, firstSHA}, workspaceRemoteHistorySHAs(ctx, t, recent))
	// Prefixes of commits already in owned storage (local work and the remote
	// anchor) resolve locally; hydrating here would fail without the origin.
	for _, sha := range []string{headSHA, firstSHA, fixture.git(ctx, t, "rev-parse", "main")} {
		got, err := head.AsRepository().Ref(sha[:12]).CommitSHA(ctx)
		require.NoError(t, err)
		require.Equal(t, sha, got)
	}
	_, err = head.Log(ctx, core.GitRefLogOpts{Limit: 100})
	require.Error(t, err, "unavailable ancestry must not silently truncate deep history")
	_, err = ws.Git().Head().Log(ctx, core.GitRefLogOpts{Paths: []string{"ancient.txt"}, Limit: 100})
	require.Error(t, err, "unavailable ancestry must not silently omit old path matches")
	require.NoError(t, c.Close())
	_, full := workspaceRemoteHistoryFetches(sink, "GitRef.log")
	require.NotEmpty(t, full, "failure must come from attempting the deferred remote fetch")
	_, commitFetches := workspaceRemoteHistoryFetches(sink, "Workspace.withCommit")
	require.Empty(t, commitFetches, "native descendants must commit without hydrating the unavailable origin")
}

// Unlike the container-only fixtures above, this clean checkout is approved by
// CurrentWorkspace().Snapshot() in the owning client. The origin has its own object
// store so removing or replacing the donor cannot accidentally break fallback.
type workspaceHostHistoryFixture struct {
	client   *dagger.Client
	sink     *agentTraceSink
	checkout string
	origin   *httptest.Server
	git      func(...string) string
	shas     []string
	straySHA string
}

// Use upload-pack over smart HTTP: the test image includes Git but not
// the separately packaged git-daemon executable. This server advertises
// only the origin's frozen refs and reads its packed repository.
func hostHistoryOriginServer(t *testing.T, repo string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		args := []string{"upload-pack", "--stateless-rpc"}
		if r.Method == http.MethodGet && r.URL.Path == "/repo/info/refs" && r.URL.Query().Get("service") == "git-upload-pack" {
			w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
			_, _ = io.WriteString(w, "001e# service=git-upload-pack\n0000")
			args = append(args, "--advertise-refs")
		} else if r.Method == http.MethodPost && r.URL.Path == "/repo/git-upload-pack" {
			w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
		} else {
			http.NotFound(w, r)
			return
		}
		args = append(args, repo)
		cmd := exec.CommandContext(r.Context(), "git", args...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		if protocol := r.Header.Get("Git-Protocol"); protocol != "" {
			cmd.Env = append(cmd.Env, "GIT_PROTOCOL="+protocol)
		}
		cmd.Stdin, cmd.Stdout, cmd.Stderr = r.Body, w, io.Discard
		if err := cmd.Run(); err != nil {
			t.Logf("host history origin upload-pack: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func newWorkspaceHostHistoryFixture(ctx context.Context, t *testctx.T) workspaceHostHistoryFixture {
	t.Helper()
	checkout := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = checkout
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_AUTHOR_DATE="+workspaceCommitDate, "GIT_COMMITTER_DATE="+workspaceCommitDate)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	write := func(path, content string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(checkout, path), []byte(content), 0o600))
	}
	git("init", "-b", "main")
	for _, setting := range [][2]string{{"user.name", "Oracle"}, {"user.email", "oracle@example.com"}, {"commit.gpgsign", "false"}, {"core.hooksPath", "/dev/null"}, {"gc.auto", "0"}} {
		git("config", setting[0], setting[1])
	}
	write("fixture-id", rand.Text())
	write("ancient.txt", "root\n")
	write("selected.txt", "base\n")
	git("add", ".")
	git("commit", "-m", "root")
	write("ancient.txt", "old\n")
	git("commit", "-am", "old")
	git("checkout", "-b", "side")
	write("side.txt", "side\n")
	git("add", ".")
	git("commit", "-m", "side")
	git("checkout", "main")
	write("main.txt", "main\n")
	git("add", ".")
	git("commit", "-m", "main")
	git("merge", "--no-ff", "side", "-m", "merge")
	write("tip.txt", "tip\n")
	git("add", ".")
	git("commit", "-m", "tip")
	shas := strings.Fields(git("rev-list", "main"))
	// An unreachable branch shares neither ancestry nor content with main.
	// Packing the donor's entire object store would leak all of these objects.
	git("checkout", "--orphan", "unrelated")
	git("rm", "-rf", ".")
	write("unrelated.txt", rand.Text())
	git("add", ".")
	git("commit", "-m", "unrelated")
	straySHA := git("rev-parse", "HEAD")
	git("tag", "unrelated-tag")
	git("checkout", "main")
	git("repack", "-ad")
	originDir := filepath.Join(t.TempDir(), "origin.git")
	git("clone", "--bare", "--no-hardlinks", ".", originDir)

	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithWorkdir(checkout), dagger.WithLogOutput(io.Discard))...)
	origin := hostHistoryOriginServer(t.Unwrap(), originDir)
	port := origin.Listener.Addr().(*net.TCPAddr).Port
	tunnel, err := core.NewQuery(c).Host().Service([]core.PortForward{{Frontend: 80, Backend: port}}).Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = tunnel.Stop(context.Background()) })
	host, err := tunnel.Hostname(ctx)
	require.NoError(t, err)
	url := "http://" + host + "/repo"
	git("remote", "add", "origin", url)
	git("update-ref", "refs/remotes/origin/main", shas[0])
	git("branch", "--set-upstream-to=origin/main", "main")
	// Route only the host's HTTP transport via loopback. The captured recipe
	// still contains the engine-reachable origin URL, not a rewritten URL.
	git("config", "http."+url+".proxy", origin.URL)
	require.Equal(t, url, git("remote", "get-url", "origin"))
	require.Equal(t, shas[0]+"\tHEAD", git("ls-remote", "origin", "HEAD"))
	// Bind the tunnel to an engine-side Git request before capture. A started
	// host tunnel alone does not establish the remote Git service's DNS route.
	remoteSHA, err := core.NewQuery(c).Git(url, core.GitOpts{ExperimentalServiceHost: tunnel}).Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, shas[0], remoteSHA)
	require.Empty(t, git("status", "--porcelain"))
	return workspaceHostHistoryFixture{client: c, sink: sink, checkout: checkout, origin: origin, git: git, shas: shas, straySHA: straySHA}
}

func (f workspaceHostHistoryFixture) commit(ctx context.Context, t *testctx.T) *core.GitRef {
	t.Helper()
	id, err := core.NewQuery(f.client).CurrentWorkspace().Snapshot().ID(ctx)
	require.NoError(t, err)
	base := core.Ref[*core.Workspace](core.NewQuery(f.client), id)
	sha, err := base.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, f.shas[0], sha, "capture must preserve the advertised remote anchor")
	ws := workspaceRemoteHistoryCommit(ctx, t, f.client, base, 1)
	head := ws.Git().Head()
	contents, err := head.Tree(core.GitRefTreeOpts{DiscardGitDir: true}).File("selected.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "local 1\n", contents)
	empty, err := ws.Git().Uncommitted().IsEmpty(ctx)
	require.NoError(t, err)
	require.True(t, empty)
	recent, err := head.Log(ctx, core.GitRefLogOpts{Limit: 2})
	require.NoError(t, err)
	require.Len(t, recent, 2)
	parent, err := recent[1].Sha(ctx)
	require.NoError(t, err)
	require.Equal(t, f.shas[0], parent)
	return head
}

func (WorkspaceSuite) TestWorkspaceApprovedHostHistoryOrdinary(ctx context.Context, t *testctx.T) {
	fixture := newWorkspaceHostHistoryFixture(ctx, t)
	fixture.commit(ctx, t)
	require.NoError(t, fixture.client.Close())
	names, _ := workspaceRemoteHistoryTrace(fixture.sink)
	for _, name := range names {
		require.NotEqual(t, "git request approved host commit closure", name, "capture and ordinary consumers must not request a donor pack")
		require.NotEqual(t, "git import approved host commit closure", name)
		require.NotEqual(t, "git hydrate owned history", name)
		require.NotEqual(t, "pack host git checkout", name, "clean advertised capture must remain remote-backed")
	}
	shallow, full := workspaceRemoteHistoryFetches(fixture.sink, "")
	require.NotEmpty(t, shallow, "ordinary commit must exercise a real depth-one remote capture")
	require.Empty(t, full, "ordinary commit/source/status/short-log must not fetch complete history")
}

func (WorkspaceSuite) TestWorkspaceApprovedHostHistoryOffline(ctx context.Context, t *testctx.T) {
	fixture := newWorkspaceHostHistoryFixture(ctx, t)
	c := fixture.client
	head := fixture.commit(ctx, t)
	headSHA, err := head.CommitSHA(ctx)
	require.NoError(t, err)
	// Closing the real HTTP listener cannot be undone by service auto-restart.
	// The donor is still alive and has the exact captured SHA's full ancestry.
	fixture.origin.Close()
	commits, err := head.Log(ctx, core.GitRefLogOpts{Limit: 100})
	require.NoError(t, err, "approved host history must satisfy deep demand without the origin")
	want := append([]string{headSHA}, fixture.shas...)
	require.ElementsMatch(t, want, workspaceRemoteHistorySHAs(ctx, t, commits))
	require.Equal(t, fixture.shas[0], fixture.git("rev-parse", "HEAD"))
	require.Empty(t, fixture.git("status", "--porcelain"), "hydration must not modify the donor")

	// Once hydrated, the engine owns the closure. A different log selector and
	// retained full checkout must survive loss of both the donor and origin.
	require.NoError(t, os.RemoveAll(filepath.Join(fixture.checkout, ".git")))
	again, err := head.Log(ctx, core.GitRefLogOpts{Limit: 101})
	require.NoError(t, err)
	require.ElementsMatch(t, want, workspaceRemoteHistorySHAs(ctx, t, again))
	tree, err := head.Tree(core.GitRefTreeOpts{Depth: -1}).Sync(ctx)
	require.NoError(t, err)
	out, err := core.NewQuery(c).Container().From(alpineImage).WithExec([]string{"apk", "add", "git"}).
		WithDirectory("/checkout", tree).WithWorkdir("/checkout").
		WithEnvVariable("STRAY_SHA", fixture.straySHA).
		WithExec([]string{"sh", "-ec", `
 test "$(git rev-parse --is-shallow-repository)" = false
 test ! -s .git/objects/info/alternates
 git fsck --full --no-dangling >&2
 git show "$(git rev-list --max-parents=0 HEAD):ancient.txt" | grep -qx root
 if git cat-file -e "$STRAY_SHA" 2>/dev/null; then
   echo 'unrelated commit was imported' >&2; exit 1
 fi
 if git for-each-ref --format='%(refname)' | grep unrelated; then
   echo 'unrelated refs were imported' >&2; exit 1
 fi
 git rev-list --objects HEAD | cut -d ' ' -f 1 | sort -u > /reachable
 git cat-file --batch-all-objects --batch-check='%(objectname)' | sort -u > /actual
 diff -u /reachable /actual >&2
 git rev-list HEAD
`}).Stdout(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, want, strings.Fields(out))
	require.NoError(t, c.Close())
	_, full := workspaceRemoteHistoryFetches(fixture.sink, "")
	require.Empty(t, full, "host reuse must not even attempt a full remote fetch")
	names, parents := workspaceRemoteHistoryTrace(fixture.sink)
	imports := 0
	for id, name := range names {
		if name == "git import approved host commit closure" {
			for parent := parents[id]; parent != ""; parent = parents[parent] {
				if names[parent] == "GitRef.log" {
					imports++
					break
				}
			}
		}
		if name == "git hydrate owned history" {
			for parent := parents[id]; parent != ""; parent = parents[parent] {
				require.NotEqual(t, "Workspace.withCommit", names[parent], "ordinary commit must remain shallow")
				require.NotEqual(t, "Workspace.snapshot", names[parent], "capture must not hydrate history")
			}
		}
	}
	require.Equal(t, 1, imports, "deep demand must import once, then reuse its owned closure")
}

// Approval covers the captured commit's closure, not the checkout's refs.
// Committing, branching, tagging and switching branches after capture is the
// normal case and must not disable the donor, nor widen what it donates.
func (WorkspaceSuite) TestWorkspaceApprovedHostHistoryMovedCheckout(ctx context.Context, t *testctx.T) {
	fixture := newWorkspaceHostHistoryFixture(ctx, t)
	c := fixture.client
	head := fixture.commit(ctx, t)
	headSHA, err := head.CommitSHA(ctx)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture.checkout, "after.txt"), []byte(rand.Text()), 0o600))
	fixture.git("add", "after.txt")
	fixture.git("commit", "-m", "after capture")
	afterSHA := fixture.git("rev-parse", "HEAD")
	afterBlob := fixture.git("rev-parse", "HEAD:after.txt")
	fixture.git("tag", "after-capture")
	fixture.git("checkout", "-b", "moved")
	require.NotEqual(t, fixture.shas[0], fixture.git("rev-parse", "HEAD"))
	// Without the origin, only the donor can satisfy deep demand.
	fixture.origin.Close()
	commits, err := head.Log(ctx, core.GitRefLogOpts{Limit: 100})
	require.NoError(t, err, "a donor whose refs moved after capture must still donate")
	want := append([]string{headSHA}, fixture.shas...)
	require.ElementsMatch(t, want, workspaceRemoteHistorySHAs(ctx, t, commits))
	tree, err := head.Tree(core.GitRefTreeOpts{Depth: -1}).Sync(ctx)
	require.NoError(t, err)
	out, err := core.NewQuery(c).Container().From(alpineImage).WithExec([]string{"apk", "add", "git"}).
		WithDirectory("/checkout", tree).WithWorkdir("/checkout").
		WithEnvVariable("AFTER_SHA", afterSHA).
		WithEnvVariable("AFTER_BLOB", afterBlob).
		WithEnvVariable("STRAY_SHA", fixture.straySHA).
		WithExec([]string{"sh", "-ec", `
 test "$(git rev-parse --is-shallow-repository)" = false
 git fsck --full --no-dangling >&2
 for object in "$AFTER_SHA" "$AFTER_BLOB" "$STRAY_SHA"; do
   if git cat-file -e "$object" 2>/dev/null; then
     echo "object outside the captured closure was imported: $object" >&2; exit 1
   fi
 done
 if git for-each-ref --format='%(refname)' | grep -e after-capture -e moved; then
   echo 'refs moved after capture were imported' >&2; exit 1
 fi
 git rev-list --objects HEAD | cut -d ' ' -f 1 | sort -u > /reachable
 git cat-file --batch-all-objects --batch-check='%(objectname)' | sort -u > /actual
 diff -u /reachable /actual >&2
 git rev-list HEAD
`}).Stdout(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, want, strings.Fields(out))
	require.NoError(t, c.Close())
	_, full := workspaceRemoteHistoryFetches(fixture.sink, "")
	require.Empty(t, full, "a moved donor must not fall back to a full remote fetch")
	names, _ := workspaceRemoteHistoryTrace(fixture.sink)
	imported := false
	for _, name := range names {
		imported = imported || name == "git import approved host commit closure"
	}
	require.True(t, imported, "deep demand must import the moved donor's closure")
}

func (WorkspaceSuite) TestWorkspaceApprovedHostHistoryFallback(ctx context.Context, t *testctx.T) {
	for _, scenario := range []string{"missing donor", "replaced donor", "shallow donor"} {
		t.Run(scenario, func(ctx context.Context, t *testctx.T) {
			fixture := newWorkspaceHostHistoryFixture(ctx, t)
			head := fixture.commit(ctx, t)
			headSHA, err := head.CommitSHA(ctx)
			require.NoError(t, err)
			switch scenario {
			case "missing donor", "replaced donor":
				require.NoError(t, os.RemoveAll(filepath.Join(fixture.checkout, ".git")))
				if scenario == "replaced donor" {
					fixture.git("init", "-b", "replacement")
					fixture.git("-c", "user.name=Replacement", "-c", "user.email=replacement@example.com", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "replacement")
				}
			case "shallow donor":
				require.NoError(t, os.WriteFile(filepath.Join(fixture.checkout, ".git", "shallow"), []byte(fixture.shas[0]+"\n"), 0o600))
				require.Equal(t, "true", fixture.git("rev-parse", "--is-shallow-repository"))
			}
			commits, err := head.Log(ctx, core.GitRefLogOpts{Limit: 100})
			require.NoError(t, err, "unusable donor must fall back to the reconstructible remote recipe")
			require.ElementsMatch(t, append([]string{headSHA}, fixture.shas...), workspaceRemoteHistorySHAs(ctx, t, commits))
			require.NoError(t, fixture.client.Close())
			names, _ := workspaceRemoteHistoryTrace(fixture.sink)
			requested := false
			for _, name := range names {
				requested = requested || name == "git request approved host commit closure"
				require.NotEqual(t, "git import approved host commit closure", name, "invalid donor must not be imported")
			}
			require.True(t, requested, "must try the registered donor before remote fallback")
			_, full := workspaceRemoteHistoryFetches(fixture.sink, "GitRef.log")
			require.NotEmpty(t, full, "must exercise remote fallback, not previously hydrated history")
			_, commitFetches := workspaceRemoteHistoryFetches(fixture.sink, "Workspace.withCommit")
			require.Empty(t, commitFetches, "ordinary commit must not fetch complete history eagerly")
		})
	}
}
