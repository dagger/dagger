package core

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"dagger.io/dagger/core"

	"dagger.io/dagger"
	"dagger.io/dagger/engineconn"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

type workspacePullPlanEntry struct {
	Commit         struct{ SHA, Message string }
	Status, Reason string
	ConflictPaths  []string
}

func planWorkspacePull(ctx context.Context, c *dagger.Client, receiver, source *core.Workspace, commits []string, maxCommits int) ([]workspacePullPlanEntry, error) {
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

func applyWorkspacePull(ctx context.Context, c *dagger.Client, receiver, source *core.Workspace, commits []string, maxCommits int) (*core.Workspace, error) {
	var result struct {
		Node struct{ WithCommitsFrom struct{ ID core.ID } }
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
	return core.Ref[*core.Workspace](core.NewQuery(c), result.Node.WithCommitsFrom.ID), nil
}

func (WorkspaceSuite) TestWorkspacePullFastForward(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	service, url := gitService(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("base.txt", "base"))
	base := snapshotWorkspace(ctx, t, c, core.NewQuery(c).Git(url, core.GitOpts{ExperimentalServiceHost: service}).Branch("main").AsWorkspace())
	source := base.WithNewFile("source.txt", "source").With(func(ws *core.Workspace) *core.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "source commit", workspaceCommitDate)
	}).WithNewFile("ignored.txt", "source WIP")
	receiver := base.WithNewFile("pending.txt", "receiver WIP").
		WithMountedDirectory("mount", core.NewQuery(c).Directory().WithNewFile("mounted.txt", "mount"))
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
	recipe, err := sink.captureLLMRecipe(ctx, t, c, core.NewQuery(c).LLM().WithWorkspace(pulled))
	require.NoError(t, err)
	var id call.ID
	require.NoError(t, id.Decode(string(recipe)))
	require.NotContains(t, id.Display(), "branch(name:")
	restored := core.Ref[*core.LLM](core.NewQuery(c), recipe).Workspace()
	sha, err = restored.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, sourceSHA, sha)
	// New commits after a pull resolve the calling client's Git identity.
	next := pulled.WithCommit(pulled.Git().Uncommitted(), "save WIP", workspaceCommitDate)
	name, err := next.Git().Head().TargetCommit().AuthorName(ctx)
	require.NoError(t, err)
	require.Equal(t, "Dagger", name)
}

// A pulled HEAD is several commits ahead of the receiver's HEAD, whether the
// pull fast-forwards or cherry-picks. Its source-only tree must still be a
// delta on the receiver's canonical tree, identical to a full checkout.
func (WorkspaceSuite) TestWorkspacePullIncrementalCheckout(ctx context.Context, t *testctx.T) {
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithLogOutput(io.Discard))...)
	fixture, inspector := gitIncrementalCheckoutFixture(c)
	commit := func(ws *core.Workspace, message string) *core.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), message, workspaceCommitDate)
	}
	base := snapshotWorkspace(ctx, t, c, fixture.AsGit().Head().AsWorkspace())
	source := commit(base.WithNewFile("selected.txt", "source\n"), "source one")
	source = commit(source.WithNewFile("added/deep.txt", "added\n").WithoutFile("delete.txt"), "source two")
	source = snapshotWorkspace(ctx, t, c, source)
	for name, receiver := range map[string]*core.Workspace{
		"fast-forward": base,
		"cherry-pick":  snapshotWorkspace(ctx, t, c, commit(base.WithNewFile("local.txt", "local\n"), "local")),
	} {
		// The receiver's canonical tree is warm, as it is in a live session.
		workspaceCommitManifest(ctx, t, inspector, receiver.Git().Head().Tree(core.GitRefTreeOpts{DiscardGitDir: true}))
		pulled, err := applyWorkspacePull(ctx, c, receiver, source, nil, 100)
		require.NoError(t, err, name)
		head := pulled.Git().Head()
		tree := head.Tree(core.GitRefTreeOpts{DiscardGitDir: true})
		require.Equal(t, workspaceCommitManifest(ctx, t, inspector, gitFullCheckoutOracle(head)), workspaceCommitManifest(ctx, t, inspector, tree), name)
		requireGitCheckoutTimes(ctx, t, inspector, tree)
	}
	require.NoError(t, c.Close()) // Drain finished spans.

	traces, _ := sink.capture()
	pulls := map[string]bool{}
	for _, request := range traces {
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if span.Name != "materialize incremental git checkout" || span.EndTimeUnixNano <= span.StartTimeUnixNano {
						continue
					}
					var supported bool
					var changed int64
					var fallback string
					for _, attr := range span.Attributes {
						switch attr.Key {
						case "dagger.git.checkout.incremental.supported":
							supported = attr.Value.GetBoolValue()
						case "dagger.git.checkout.incremental.changed_paths":
							changed = attr.Value.GetIntValue()
						case "dagger.git.checkout.incremental.fallback":
							fallback = attr.Value.GetStringValue()
						}
					}
					t.Logf("incremental checkout supported=%t changed_paths=%d fallback=%q", supported, changed, fallback)
					// Single-commit deltas are withCommit's; the pulled
					// HEADs are three paths away from their receivers.
					if supported && changed == 3 {
						pulls[string(span.TraceId)+string(span.SpanId)] = true
					}
				}
			}
		}
	}
	require.Len(t, pulls, 2, "each pulled HEAD must check out as a delta on its receiver")
}

// An agent session replayed on a cold engine: a remote-backed workspace, a
// few commits, a cherry-picking and a fast-forwarding pull from another
// workspace, and the uncommitted changes of each, which compare HEAD's
// source-only tree. Live, and replayed from recipes after pruning the cache,
// every local HEAD's tree must be a delta: freezing a receiver for a commit
// or a pull already materializes its HEAD's tree, so a parent is never cold.
func (WorkspaceSuite) TestWorkspacePullColdTrees(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	content := core.NewQuery(c).Directory().
		WithNewFile(".gitattributes", "*.txt text eol=lf\n").
		WithNewFile("base.txt", "base\n").
		WithNewFile("dir/keep.txt", "keep\n")
	// A replay on a cold engine rebuilds this repository and fetches pinned
	// commits by SHA, as from a hosted remote: keep the commit reproducible
	// and allow SHA wants, which git daemon refuses by default.
	service := core.NewQuery(c).Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git", "git-daemon"}).
		WithEnvVariable("GIT_AUTHOR_DATE", workspaceCommitDate).
		WithEnvVariable("GIT_COMMITTER_DATE", workspaceCommitDate).
		WithDirectory("/root/repo", content).
		WithExec([]string{"sh", "-ec", `
cd /root/repo
git init -q -b main
git -c user.name=Test -c user.email=test@localhost add -A
git -c user.name=Test -c user.email=test@localhost commit -q -m init
mkdir /root/srv
git clone -q --no-local --bare /root/repo /root/srv/repo.git
git -C /root/srv/repo.git config uploadpack.allowAnySHA1InWant true
`}).
		WithExposedPort(9418).
		WithDefaultArgs([]string{"sh", "-c", "git daemon --verbose --export-all --base-path=/root/srv"}).
		AsService()
	host, err := service.Hostname(ctx)
	require.NoError(t, err)
	url := fmt.Sprintf("git://%s/repo.git", host)
	base := snapshotWorkspace(ctx, t, c, core.NewQuery(c).Git(url, core.GitOpts{ExperimentalServiceHost: service}).Branch("main").AsWorkspace())
	commit := func(ws *core.Workspace, message string) *core.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), message, workspaceCommitDate)
	}
	agent := base
	for i := range 3 {
		agent = commit(agent.WithNewFile(fmt.Sprintf("agent/%d.txt", i), fmt.Sprintf("agent %d\n", i)), fmt.Sprintf("agent %d", i))
	}
	agent = snapshotWorkspace(ctx, t, c, agent.WithNewFile("pending.txt", "work in progress\n"))
	source := base
	for i := range 2 {
		source = commit(source.WithNewFile(fmt.Sprintf("worker/%d.txt", i), fmt.Sprintf("worker %d\n", i)).WithoutFile("dir/keep.txt"), fmt.Sprintf("worker %d", i))
	}
	source = snapshotWorkspace(ctx, t, c, source)
	picked, err := applyWorkspacePull(ctx, c, agent, source, nil, 100)
	require.NoError(t, err)
	// Commit only the new file: the pulled-in pending edit stays pending.
	picked = picked.WithNewFile("after-pick.txt", "picked\n")
	picked = snapshotWorkspace(ctx, t, c, picked.WithCommit(picked.Git().Uncommitted().Filter(core.ChangesetFilterOpts{Exclude: []string{"pending.txt"}}), "after pick", workspaceCommitDate))
	forwarded, err := applyWorkspacePull(ctx, c, base, source, nil, 100)
	require.NoError(t, err)
	forwarded = snapshotWorkspace(ctx, t, c, commit(forwarded.WithNewFile("after-ff.txt", "forwarded\n"), "after fast-forward"))
	requirePending := func(name string, ws *core.Workspace) {
		pending, err := ws.Git().Uncommitted().AddedPaths(ctx)
		require.NoError(t, err, name)
		if name == "cherry-pick" {
			require.Equal(t, []string{"pending.txt"}, pending)
		} else {
			require.Empty(t, pending, name)
		}
	}
	ids := map[string]core.ID{}
	for name, ws := range map[string]*core.Workspace{"cherry-pick": picked, "fast-forward": forwarded} {
		requirePending(name, ws)
		// A replayable recipe, as a resumed session's trace records it.
		ids[name], err = sink.captureLLMRecipe(ctx, t, c, core.NewQuery(c).LLM().WithWorkspace(ws))
		require.NoError(t, err)
	}
	require.NoError(t, c.Close()) // Drain finished spans.
	live := gitSourceTreePaths(t, sink)
	require.NotEmpty(t, live["incremental"])
	require.Empty(t, live["full"], "local HEADs must not need a full source checkout")

	// Replay both recipes on an engine that has forgotten everything, like a
	// resumed session: concurrently, as restored agents are.
	pruner := connect(ctx, t)
	require.NoError(t, core.NewQuery(pruner).Engine().LocalCache().Prune(ctx))
	require.NoError(t, pruner.Close())
	cold, coldSink := connectWithTrace(ctx, t)
	var eg errgroup.Group
	replayedPending := make(map[string][]string, len(ids))
	var mu sync.Mutex
	for name, id := range ids {
		eg.Go(func() error {
			pending, err := core.Ref[*core.LLM](core.NewQuery(cold), id).Workspace().Git().Uncommitted().AddedPaths(ctx)
			mu.Lock()
			replayedPending[name] = pending
			mu.Unlock()
			return err
		})
	}
	require.NoError(t, eg.Wait())
	require.Equal(t, []string{"pending.txt"}, replayedPending["cherry-pick"])
	require.Empty(t, replayedPending["fast-forward"])
	require.NoError(t, cold.Close())
	replayed := gitSourceTreePaths(t, coldSink)
	require.NotEmpty(t, replayed["incremental"])
	require.Empty(t, replayed["full"], "replayed local HEADs must not need a full source checkout")
}

// Sessions recorded before checkout bases were retained replay repositories
// opened with GitRepository.withContents over a retained checkout, over its
// .git, or over a pull's scratch repository. Their source-only trees must come
// from those checkouts, not from another full checkout, and match the trees
// of the same commits built incrementally.
func (WorkspaceSuite) TestWorkspaceRetainedCheckoutTrees(ctx context.Context, t *testctx.T) {
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithLogOutput(io.Discard))...)
	fixture, inspector := gitIncrementalCheckoutFixture(c)
	repo := fixture.AsGit()
	discard := core.GitRefTreeOpts{DiscardGitDir: true}
	before := repo.Head().Tree(discard)
	head := repo.Head().WithCommit(before.WithNewFile("selected.txt", "committed\n").Changes(before), "receiver", workspaceCommitDate, "Oracle", "oracle@example.com")
	headTree := head.Tree(discard)
	source := head.WithCommit(headTree.WithNewFile("added/deep.txt", "added\n").WithoutFile("delete.txt").WithNewFile("selected.txt", "source\n").Changes(headTree), "source", workspaceCommitDate, "Oracle", "oracle@example.com")
	headID, err := head.ID(ctx)
	require.NoError(t, err)
	headSHA, err := head.CommitSHA(ctx)
	require.NoError(t, err)
	sourceSHA, err := source.CommitSHA(ctx)
	require.NoError(t, err)
	asWorkspace := core.GitRefAsWorkspaceOpts{Cwd: "/"}
	sourceID, err := source.AsWorkspace(asWorkspace).ID(ctx)
	require.NoError(t, err)
	receiverID, err := head.AsWorkspace(asWorkspace).ID(ctx)
	require.NoError(t, err)
	var fullCheckout struct {
		Node struct {
			FullCheckout struct{ ID core.ID } `json:"__fullCheckout"`
		}
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!) { node(id: $id) { ... on GitRef { __fullCheckout { id } } } }`,
		Variables: map[string]any{"id": headID},
	}, &dagger.Response{Data: &fullCheckout}))
	full := core.Ref[*core.Directory](core.NewQuery(c), fullCheckout.Node.FullCheckout.ID)
	pulled := core.Ref[*core.Directory](core.NewQuery(c), selectHidden(ctx, t, c, receiverID, "Workspace", "__pullDirectory", map[string]any{
		"source":         sourceID,
		"committerName":  "Committer",
		"committerEmail": "committer@example.com",
	}))
	pulledSHA, err := pulled.AsGit().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, sourceSHA, pulledSHA, "fast-forward")
	for _, tc := range []struct {
		name     string
		contents *core.Directory
		sha      string
		want     *core.Directory
	}{
		{"checkout", full, headSHA, headTree},
		{"checkout .git", full.Directory(".git"), headSHA, headTree},
		{"pull", pulled, pulledSHA, source.Tree(discard)},
	} {
		ref := repo.WithContents(tc.contents).Ref(tc.sha)
		tree := ref.Tree(discard)
		require.Equal(t, workspaceCommitManifest(ctx, t, inspector, tc.want), workspaceCommitManifest(ctx, t, inspector, tree), tc.name)
		requireGitCheckoutTimes(ctx, t, inspector, tree)
		// Replayed workspaces reach the same tree through asWorkspace.
		exists, err := ref.AsWorkspace(asWorkspace).Directory("/").Exists(ctx, "selected.txt")
		require.NoError(t, err, tc.name)
		require.True(t, exists, tc.name)
	}
	require.NoError(t, c.Close()) // Drain finished spans.
	paths := gitSourceTreePaths(t, sink)
	require.NotEmpty(t, paths["checkout:checkout/strip"])
	require.NotEmpty(t, paths["checkout:checkout-git-dir/strip"])
	require.NotEmpty(t, paths["checkout:pull-receiver/delta"])
}

// gitSourceTreePaths groups the source-only trees a session materialized by
// the path they took (span "git source tree: <path>"), and by path:detail,
// logging each.
func gitSourceTreePaths(t *testctx.T, sink *agentTraceSink) map[string]map[string]bool {
	traces, _ := sink.capture()
	paths := map[string]map[string]bool{}
	for _, request := range traces {
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if !strings.HasPrefix(span.Name, "git source tree") || span.EndTimeUnixNano <= span.StartTimeUnixNano {
						continue
					}
					id := string(span.TraceId) + string(span.SpanId)
					var path, detail string
					var skipped []string
					for _, attr := range span.Attributes {
						switch attr.Key {
						case "dagger.git.tree.path":
							path = attr.Value.GetStringValue()
						case "dagger.git.tree.detail":
							detail = attr.Value.GetStringValue()
						case "dagger.git.tree.skipped":
							for _, v := range attr.Value.GetArrayValue().GetValues() {
								skipped = append(skipped, v.GetStringValue())
							}
						}
					}
					if paths[path] == nil {
						paths[path] = map[string]bool{}
					}
					if paths[path][id] {
						continue
					}
					paths[path][id] = true
					if detail != "" {
						if paths[path+":"+detail] == nil {
							paths[path+":"+detail] = map[string]bool{}
						}
						paths[path+":"+detail][id] = true
					}
					t.Logf("source tree path=%s detail=%q skipped=%v duration=%s", path, detail, skipped, time.Duration(span.EndTimeUnixNano-span.StartTimeUnixNano))
				}
			}
		}
	}
	return paths
}

func (WorkspaceSuite) TestWorkspacePullCherryPick(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	git("config", "user.name", "Source")
	git("config", "user.email", "source@example.com")
	sourceClient := connect(ctx, t, dagger.WithWorkdir(checkout))
	sourceID, err := snapshotWorkspace(ctx, t, sourceClient, core.NewQuery(sourceClient).CurrentWorkspace()).WithNewFile("from-source.txt", "source").With(func(ws *core.Workspace) *core.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "source", workspaceCommitDate)
	}).ID(ctx)
	require.NoError(t, err)

	git("config", "user.name", "Receiver")
	git("config", "user.email", "receiver@example.com")
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	base := snapshotWorkspace(ctx, t, c, core.NewQuery(c).CurrentWorkspace())
	source := core.Ref[*core.Workspace](core.NewQuery(c), sourceID)
	receiver := base.WithNewFile("local.txt", "local").With(func(ws *core.Workspace) *core.Workspace {
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
	base := snapshotWorkspace(ctx, t, c, core.NewQuery(c).CurrentWorkspace())
	source := base.WithNewFile("base.txt", "source").With(func(ws *core.Workspace) *core.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "conflicting", workspaceCommitDate)
	}).
		WithNewFile("independent.txt", "independent").With(func(ws *core.Workspace) *core.Workspace {
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
	same := base.WithNewFile("base.txt", "source").With(func(ws *core.Workspace) *core.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "equivalent local", workspaceCommitDate)
	})
	plan, err := planWorkspacePull(ctx, c, same, source, nil, 100)
	require.NoError(t, err)
	require.Equal(t, "REDUNDANT", plan[0].Status)
	require.Equal(t, "PICKABLE", plan[1].Status)
	// An empty-directory overlay must not hide a newly committed file.
	emptyDir := base.WithChanges(core.NewQuery(c).Directory().WithNewDirectory("empty").Changes(core.NewQuery(c).Directory()))
	dirtyPaths, err := emptyDir.Git().Uncommitted().AddedPaths(ctx)
	require.NoError(t, err)
	require.Contains(t, dirtyPaths, "empty/")
	incoming := base.WithNewFile("empty", "incoming file").With(func(ws *core.Workspace) *core.Workspace {
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

func (WorkspaceSuite) TestWorkspacePullDirtyDirectoryRename(ctx context.Context, t *testctx.T) {
	checkout, _ := workspaceExportCheckout(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	base := snapshotWorkspace(ctx, t, c, core.NewQuery(c).CurrentWorkspace()).WithNewFile("old/a.txt", "committed file\n")
	base = base.WithCommit(base.Git().Uncommitted(), "directory to rename", workspaceCommitDate)
	source := base.WithoutDirectory("old").WithNewFile("new/a.txt", "committed file\n")
	source = source.WithCommit(source.Git().Uncommitted(), "rename directory", workspaceCommitDate)

	for _, diverged := range []bool{false, true} {
		t.Run(fmt.Sprintf("diverged=%t", diverged), func(ctx context.Context, t *testctx.T) {
			receiver := base
			if diverged {
				receiver = receiver.WithNewFile("local.txt", "local commit")
				receiver = receiver.WithCommit(receiver.Git().Uncommitted(), "local", workspaceCommitDate)
			}
			receiver = receiver.WithNewFile("old/pending.txt", "pending new file").
				WithNewFile("base.txt", "unrelated pending edit")
			original, err := receiver.Git().Head().CommitSHA(ctx)
			require.NoError(t, err)

			_, err = applyWorkspacePull(ctx, c, receiver, source, nil, 100)
			require.ErrorContains(t, err, "merge uncommitted changes")
			require.ErrorContains(t, err, "CONFLICT (file location)")
			require.ErrorContains(t, err, "old/pending.txt")
			_, err = planWorkspacePull(ctx, c, receiver, source, nil, 100)
			require.ErrorContains(t, err, "merge uncommitted changes")
			require.ErrorContains(t, err, "old/pending.txt")

			sha, err := receiver.Git().Head().CommitSHA(ctx)
			require.NoError(t, err)
			require.Equal(t, original, sha)
			for path, want := range map[string]string{
				"old/pending.txt": "pending new file",
				"base.txt":        "unrelated pending edit",
			} {
				got, err := receiver.File(path).Contents(ctx)
				require.NoError(t, err)
				require.Equal(t, want, got)
			}

			// Removing the conflicting addition permits the rename while keeping
			// the unrelated pending edit out of the incoming commit.
			pulled, err := applyWorkspacePull(ctx, c, receiver.WithoutFile("old/pending.txt"), source, nil, 100)
			require.NoError(t, err)
			got, err := pulled.File("new/a.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "committed file\n", got)
			got, err = pulled.File("base.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "unrelated pending edit", got)
			modified, err := pulled.Git().Uncommitted().ModifiedPaths(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{"base.txt"}, modified)
		})
	}
}

func (WorkspaceSuite) TestWorkspacePullShortSHAs(ctx context.Context, t *testctx.T) {
	checkout, _ := workspaceExportCheckout(ctx, t)
	publishCheckpointRemote(ctx, t, checkout)
	c, sink := connectWithTrace(ctx, t, engineconn.Config{Workdir: checkout})
	base := snapshotWorkspace(ctx, t, c, core.NewQuery(c).CurrentWorkspace())
	source := base.WithNewFile("a", "a").With(func(ws *core.Workspace) *core.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "a", workspaceCommitDate)
	})
	a, err := source.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	source = source.WithNewFile("b", "b").With(func(ws *core.Workspace) *core.Workspace {
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
	var recipes []core.ID
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
		recipe, err := sink.captureLLMRecipe(ctx, t, c, core.NewQuery(c).LLM().WithWorkspace(pulled))
		require.NoError(t, err)
		recipes = append(recipes, recipe)
		var id call.ID
		require.NoError(t, id.Decode(string(recipe)))
		require.Contains(t, id.Display(), b)
		require.NotContains(t, id.Display(), fmt.Sprintf("%q", b[:7]))
		restored := core.Ref[*core.LLM](core.NewQuery(c), recipe).Workspace()
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
	source := snapshotWorkspace(ctx, t, c, core.NewQuery(c).CurrentWorkspace())
	base := source.WithReset(baseSHA, core.WorkspaceWithResetOpts{Hard: true})
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
	base := snapshotWorkspace(ctx, t, c, core.NewQuery(c).CurrentWorkspace())
	source := base.WithNewFile("a", "a").With(func(ws *core.Workspace) *core.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "a", workspaceCommitDate)
	})
	a, err := source.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	source = source.WithNewFile("b", "b").With(func(ws *core.Workspace) *core.Workspace {
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
	_, err = planWorkspacePull(ctx, c, core.NewQuery(c).CurrentWorkspace(), source, nil, 100)
	require.NoError(t, err)
	_, err = applyWorkspacePull(ctx, c, base, core.NewQuery(c).CurrentWorkspace(), nil, 100)
	require.ErrorContains(t, err, "call snapshot")
	// Public GitRef.log still rejects zero: pulling doesn't require unlimited history.
	// The SDK omits zero-valued optional ints, so use a direct query.
	baseID, err := base.ID(ctx)
	require.NoError(t, err)
	err = c.Do(ctx, &dagger.Request{Query: `query($id: ID!) { node(id:$id) { ... on Workspace { git { head { log(limit:0) { sha } } } } } }`, Variables: map[string]any{"id": baseID}}, &dagger.Response{})
	require.ErrorContains(t, err, "at least 1")
}
