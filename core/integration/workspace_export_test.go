package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func saveWorkspaceTo(ctx context.Context, c *dagger.Client, source, from *dagger.Workspace, path string) error {
	id, err := source.ID(ctx)
	if err != nil {
		return err
	}
	var fromID any
	if from != nil {
		fromID, err = from.ID(ctx)
		if err != nil {
			return err
		}
	}
	return c.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!, $from: ID, $path: String!) { node(id: $id) { ... on Workspace { export(path: $path, from: $from) } } }`,
		Variables: map[string]any{"id": id, "from": fromID, "path": path},
	}, &dagger.Response{})
}

func (WorkspaceSuite) TestWorkspaceExportReusesOriginBase(ctx context.Context, t *testctx.T) {
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		t.Skip("needs its own CLI session to inspect export call structure")
	}
	for _, scenario := range []string{"matching routing", "changed push routing"} {
		t.Run(scenario, func(ctx context.Context, t *testctx.T) {
			sink := newAgentTraceSink(t)
			c := connect(ctx, t, sink.clientOpts()...)
			origin, originURL := gitService(ctx, t, c, c.Directory().
				WithNewFile("source.txt", "base source\n").
				WithNewFile("destination.txt", "base destination\n"))
			_, err := origin.Start(ctx)
			require.NoError(t, err)
			// Both spellings reach the real daemon, but differing captured push
			// routing must not be silently replaced with the source's spelling.
			pushURL := strings.Replace(originURL, "/repo.git", ":9418/repo.git", 1)
			destinationPush := pushURL
			if scenario == "changed push routing" {
				destinationPush = originURL
			}
			checkout := c.Container().From(golangImage).
				WithExec([]string{"apk", "add", "git"}).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithServiceBinding("origin", origin).
				WithExec([]string{"git", "clone", originURL, "/source"}).
				WithExec([]string{"git", "clone", originURL, "/destination"}).
				WithExec([]string{"git", "-C", "/source", "config", "remote.origin.pushurl", pushURL}).
				WithExec([]string{"git", "-C", "/destination", "config", "remote.origin.pushurl", destinationPush}).
				// Dirty capture imports a bundle and hence owns LOCAL storage,
				// rather than leaving the clean advertised HEAD remote-backed.
				WithNewFile("/source/source.txt", "source edit\n").
				WithNewFile("/destination/destination.txt", "destination dirt\n").
				WithWorkdir("/source").
				With(daggerShell(`
base=$(current-workspace | snapshot)
$base | with-new-file pending.txt saved | export --path /destination
`))
			_, err = checkout.Sync(ctx)
			require.NoError(t, err)
			for file, want := range map[string]string{"source.txt": "source edit\n", "destination.txt": "destination dirt\n", "pending.txt": "saved"} {
				got, err := checkout.File("/destination/" + file).Contents(ctx)
				require.NoError(t, err)
				require.Equal(t, want, got)
			}
			fetch, err := checkout.WithExec([]string{"git", "-C", "/destination", "remote", "get-url", "origin"}).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, originURL, strings.TrimSpace(fetch))
			push, err := checkout.WithExec([]string{"git", "-C", "/destination", "remote", "get-url", "--push", "origin"}).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, destinationPush, strings.TrimSpace(push))
			require.NoError(t, c.Close())
			counts := workspaceExportOperationCounts(sink)
			require.Equal(t, 1, counts["compose workspace export destination"])
			if scenario == "matching routing" {
				require.Equal(t, 1, counts["checkpoint reuse owned repository"])
				require.Equal(t, 1, counts["validate workspace export base"])
				require.Zero(t, counts["checkpoint reconstruct repository"])
			} else {
				require.Zero(t, counts["checkpoint reuse owned repository"])
				require.Zero(t, counts["validate workspace export base"], "routing mismatch must not even qualify source storage")
				require.Equal(t, 1, counts["checkpoint reconstruct repository"])
			}
			require.Zero(t, counts["pack host git checkout"])
			require.Zero(t, counts["reconstruct host git checkout"])
		})
	}
}

// Count completed operation spans under explicit export requests, excluding
// fixture setup and snapshot. Live start/final records share trace+span identity.
func workspaceExportOperationCounts(sink *agentTraceSink) map[string]int {
	traces, _ := sink.capture()
	parents, names := map[string]string{}, map[string]string{}
	for _, request := range traces {
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if span.EndTimeUnixNano < span.StartTimeUnixNano {
						continue
					}
					id := string(span.TraceId) + string(span.SpanId)
					parents[id], names[id] = string(span.TraceId)+string(span.ParentSpanId), span.Name
				}
			}
		}
	}
	counts := map[string]int{}
	for id, name := range names {
		for parent := parents[id]; parent != ""; parent = parents[parent] {
			if names[parent] == "Workspace.export" {
				counts[name]++
				break
			}
		}
	}
	return counts
}

func (WorkspaceSuite) TestWorkspaceExportBaseReadinessCacheIsolation(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	checkout := c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithWorkdir("/repo").
		WithExec([]string{"sh", "-ec", `
			git init -q
			git config user.name Test
			git config user.email test@example.com
			printf base > base.txt
			git add base.txt
			git commit -qm base
			git rev-parse HEAD HEAD:base.txt
		`})
	out, err := checkout.Stdout(ctx)
	require.NoError(t, err)
	objects := strings.Fields(out)
	require.Len(t, objects, 2)
	head, blob := objects[0], objects[1]
	complete := checkout.Directory("/repo")
	incomplete := complete.WithoutFile(".git/objects/" + blob[:2] + "/" + blob[2:])
	for _, tc := range []struct {
		directory *dagger.Directory
		ready     bool
	}{{complete, true}, {incomplete, false}, {complete, true}} {
		id, err := tc.directory.AsGit().Ref(head).ID(ctx)
		require.NoError(t, err)
		var result struct {
			Node struct {
				Ready bool `json:"__workspaceExportBaseReady"`
			}
		}
		err = c.Do(ctx, &dagger.Request{
			Query:     `query($id: ID!) { node(id: $id) { ... on GitRef { __workspaceExportBaseReady } } }`,
			Variables: map[string]any{"id": id},
		}, &dagger.Response{Data: &result})
		require.NoError(t, err)
		require.Equal(t, tc.ready, result.Node.Ready, "same SHA in different immutable storage must not share a positive readiness proof")
	}
}

func (WorkspaceSuite) TestWorkspaceExportReusesCapturedBase(ctx context.Context, t *testctx.T) {
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		t.Skip("needs its own CLI session to inspect export call structure")
	}
	checkout, git := workspaceExportCheckout(ctx, t)
	for _, name := range []string{"mode.txt", "deleted.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(checkout, name), []byte(name), 0o644))
	}
	require.NoError(t, os.Symlink("base.txt", filepath.Join(checkout, "link")))
	git("add", ".")
	git("commit", "-m", "Git edge fixtures")
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithWorkdir(checkout))...)
	base := snapshotWorkspace(ctx, t, c, c.CurrentWorkspace())
	baseID, err := base.ID(ctx)
	require.NoError(t, err)
	base = dagger.Ref[*dagger.Workspace](c, baseID)
	// Destination edits are newer than the frozen source. Reuse must borrow
	// only committed history, never replace captured dirt with the source tree.
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "base.txt"), []byte("destination dirt"), 0o644))
	require.NoError(t, os.Chmod(filepath.Join(checkout, "mode.txt"), 0o755))
	require.NoError(t, os.Remove(filepath.Join(checkout, "deleted.txt")))
	require.NoError(t, os.Remove(filepath.Join(checkout, "link")))
	require.NoError(t, os.Symlink("mode.txt", filepath.Join(checkout, "link")))
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "staged.txt"), []byte("staged destination"), 0o644))
	git("add", "staged.txt")
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "staged.txt"), []byte("unstaged destination"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "private.txt"), []byte("never captured"), 0o600))
	index, dirt, head := git("diff", "--cached", "--binary"), git("diff", "--binary"), git("rev-parse", "HEAD")
	first := base.WithNewFile("pending.txt", "first save")
	firstID, err := first.ID(ctx)
	require.NoError(t, err)
	first = dagger.Ref[*dagger.Workspace](c, firstID)
	require.NoError(t, saveWorkspaceTo(ctx, c, first, base, checkout))
	second := first.WithNewFile("pending.txt", "second save")
	require.NoError(t, saveWorkspaceTo(ctx, c, second, first, checkout))
	require.NoError(t, saveWorkspaceTo(ctx, c, second, first, checkout), "retry remains idempotent")
	require.Equal(t, index, git("diff", "--cached", "--binary"))
	require.Equal(t, dirt, git("diff", "--binary"))
	require.Equal(t, head, git("rev-parse", "HEAD"))
	pending, err := os.ReadFile(filepath.Join(checkout, "pending.txt"))
	require.NoError(t, err)
	require.Equal(t, "second save", string(pending))
	// The source now has a different HEAD: only the previous-save value is an
	// eligible base for the captured destination.
	committed := second.WithCommit(second.Git().Uncommitted(), "commit saved pending", workspaceCommitDate)
	require.NoError(t, saveWorkspaceTo(ctx, c, committed, second, checkout))
	require.Equal(t, "second save", git("show", "HEAD:pending.txt"))
	require.Equal(t, index, git("diff", "--cached", "--binary"))
	require.Equal(t, dirt, git("diff", "--binary"))
	private, err := os.ReadFile(filepath.Join(checkout, "private.txt"))
	require.NoError(t, err)
	require.Equal(t, "never captured", string(private))
	original, err := base.File("base.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "base", original)
	require.NoError(t, c.Close()) // flush telemetry, not a timing assertion
	counts := workspaceExportOperationCounts(sink)
	require.Equal(t, 1, counts["validate workspace export base"], "immutable base readiness is validated once, then cached across saves")
	require.Equal(t, 4, counts["compose workspace export destination"])
	require.Equal(t, 4, counts["checkpoint reuse owned repository"], "every matching export must omit repository reconstruction")
	require.Zero(t, counts["pack host git checkout"])
	require.Zero(t, counts["reconstruct host git checkout"])
}

func (WorkspaceSuite) TestWorkspaceExportToCheckoutIncrementally(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	base := snapshotWorkspace(ctx, t, c, c.CurrentWorkspace())
	original := git("rev-parse", "HEAD")
	destination := filepath.Join(t.TempDir(), "destination")
	git("clone", checkout, destination)
	targetGit := func(args ...string) string {
		return git(append([]string{"-C", destination}, args...)...)
	}
	targetGit("config", "user.name", "Destination Committer")
	targetGit("config", "user.email", "destination@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(destination, "host.txt"), []byte("independent host commit"), 0o644))
	targetGit("add", ".")
	targetGit("commit", "-m", "destination diverges")
	hostHead := targetGit("rev-parse", "HEAD")
	require.NoError(t, os.WriteFile(filepath.Join(destination, "private.txt"), []byte("untracked host file"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(destination, "staged.txt"), []byte("staged host file"), 0o644))
	targetGit("add", "staged.txt")
	pin := func(ws *dagger.Workspace) *dagger.Workspace {
		id, err := ws.ID(ctx)
		require.NoError(t, err)
		return dagger.Ref[*dagger.Workspace](c, id)
	}
	first := pin(base.WithNewFile("agent.txt", "agent").With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "agent first", workspaceCommitDate)
	}).
		WithNewFile("pending.txt", "first pending").
		WithMountedDirectory("mount", c.Directory().WithNewFile("private", "not exported")))
	require.NoError(t, saveWorkspaceTo(ctx, c, first, base, destination))
	require.NoError(t, saveWorkspaceTo(ctx, c, first, base, destination), "retry with the old source baseline")
	require.NoError(t, saveWorkspaceTo(ctx, c, first, first, filepath.Join(t.TempDir(), "absent")), "identical source and comparator need no destination capture")
	firstHost := targetGit("rev-parse", "HEAD")
	firstSource, err := first.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.NotEqual(t, firstSource, firstHost, "divergent history is cherry-picked")
	require.Equal(t, hostHead, targetGit("rev-parse", "HEAD^"))
	require.Equal(t, "Destination Committer", targetGit("log", "-1", "--format=%cn"))
	second := pin(first.WithNewFile("pending.txt", "second pending"))
	require.NoError(t, saveWorkspaceTo(ctx, c, second, first, destination))
	require.NoError(t, saveWorkspaceTo(ctx, c, second, first, destination))
	require.Equal(t, firstHost, targetGit("rev-parse", "HEAD"))
	data, err := os.ReadFile(filepath.Join(destination, "pending.txt"))
	require.NoError(t, err)
	require.Equal(t, "second pending", string(data))
	third := pin(second.WithCommit(second.Git().Uncommitted(), "commit saved pending", workspaceCommitDate))
	require.NoError(t, saveWorkspaceTo(ctx, c, third, second, destination))
	require.NoError(t, saveWorkspaceTo(ctx, c, third, second, destination))
	require.Equal(t, "2", targetGit("rev-list", "--count", hostHead+"..HEAD"))
	require.Equal(t, "second pending", targetGit("show", "HEAD:pending.txt"))
	require.Equal(t, "A  staged.txt\n?? private.txt", targetGit("status", "--porcelain"))
	require.Equal(t, "staged host file", targetGit("show", ":staged.txt"))
	require.Equal(t, original, git("rev-parse", "HEAD"), "explicit export never modifies the source checkout")
	require.Empty(t, git("status", "--porcelain"))
	_, err = os.Stat(filepath.Join(destination, "mount"))
	require.ErrorIs(t, err, os.ErrNotExist)
	pending, err := second.File("pending.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "second pending", pending, "source values are unchanged")
}

func (WorkspaceSuite) TestWorkspaceExportToCheckoutPendingSafety(ctx context.Context, t *testctx.T) {
	for _, operation := range []string{"delete", "rename", "conflict", "missing"} {
		t.Run(operation, func(ctx context.Context, t *testctx.T) {
			checkout, git := workspaceExportCheckout(ctx, t)
			c := connect(ctx, t, dagger.WithWorkdir(checkout))
			base := snapshotWorkspace(ctx, t, c, c.CurrentWorkspace())
			first := base.WithNewFile("pending.txt", "saved pending")
			id, err := first.ID(ctx)
			require.NoError(t, err)
			first = dagger.Ref[*dagger.Workspace](c, id)
			require.NoError(t, saveWorkspaceTo(ctx, c, first, nil, checkout))
			next := first.WithoutFile("pending.txt")
			if operation == "rename" {
				next = next.WithNewFile("renamed.txt", "saved pending")
			}
			if operation == "conflict" {
				require.NoError(t, os.WriteFile(filepath.Join(checkout, "pending.txt"), []byte("host edit"), 0o644))
			}
			if operation == "missing" {
				require.NoError(t, os.Remove(filepath.Join(checkout, "pending.txt")))
				next = first.WithNewFile("pending.txt", "next pending")
			}
			head, status := git("rev-parse", "HEAD"), git("status", "--porcelain")
			err = saveWorkspaceTo(ctx, c, next, first, checkout)
			if operation == "conflict" || operation == "missing" {
				require.Error(t, err)
				require.Equal(t, status, git("status", "--porcelain"))
			} else {
				require.NoError(t, err)
				require.NoError(t, saveWorkspaceTo(ctx, c, next, first, checkout), "retry deletion/rename")
				_, err := os.Stat(filepath.Join(checkout, "pending.txt"))
				require.ErrorIs(t, err, os.ErrNotExist)
			}
			require.Equal(t, head, git("rev-parse", "HEAD"))
		})
	}
}

func workspaceExportCheckout(ctx context.Context, t *testctx.T) (string, func(...string) string) {
	t.Helper()
	checkout := t.TempDir()
	initGitRepo(ctx, t, checkout)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = checkout
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		return strings.TrimSpace(string(out))
	}
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "base.txt"), []byte("base"), 0o644))
	git("add", ".")
	git("commit", "-m", "initial")
	return checkout, git
}

func (WorkspaceSuite) TestWorkspaceExportIndependentAgents(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	baseID, err := snapshotWorkspace(ctx, t, c, c.CurrentWorkspace()).ID(ctx)
	require.NoError(t, err)
	base := dagger.Ref[*dagger.Workspace](c, baseID)
	a := base.WithNewFile("agent-a.txt", "a").With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "agent A", workspaceCommitDate)
	})
	b := base.WithNewFile("agent-b.txt", "b").With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "agent B", workspaceCommitDate)
	})
	aID, err := a.ID(ctx)
	require.NoError(t, err)
	bID, err := b.ID(ctx)
	require.NoError(t, err)
	a, b = dagger.Ref[*dagger.Workspace](c, aID), dagger.Ref[*dagger.Workspace](c, bID)
	// Both sources were frozen before either export. Each export captures the
	// current checkout and integrates the other agent's work.
	require.NoError(t, saveWorkspaceTo(ctx, c, a, nil, checkout))
	first := git("rev-parse", "HEAD")
	require.NoError(t, saveWorkspaceTo(ctx, c, b, nil, checkout))
	second := git("rev-parse", "HEAD")
	require.Contains(t, git("log", "--format=%H"), first)
	sourceSHA, err := b.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.NotEqual(t, sourceSHA, second)
	require.Equal(t, "agent B", git("log", "-1", "--format=%B"), "cherry-picking preserves the original commit message")
	require.NoError(t, saveWorkspaceTo(ctx, c, b, nil, checkout))
	require.Equal(t, second, git("rev-parse", "HEAD"))
	require.Empty(t, git("status", "--porcelain"))
	for _, name := range []string{"agent-a.txt", "agent-b.txt"} {
		_, err := os.Stat(filepath.Join(checkout, name))
		require.NoError(t, err)
	}
}

func (WorkspaceSuite) TestWorkspaceExportCapturedDirt(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "base.txt"), []byte("captured dirt"), 0o644))
	agentID, err := snapshotWorkspace(ctx, t, c, c.CurrentWorkspace()).With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "commit captured cleanup", workspaceCommitDate)
	}).ID(ctx)
	require.NoError(t, err)
	agent := dagger.Ref[*dagger.Workspace](c, agentID)
	require.NoError(t, saveWorkspaceTo(ctx, c, agent, nil, checkout))
	require.Empty(t, git("status", "--porcelain"))
	require.Equal(t, "captured dirt", git("show", "HEAD:base.txt"))
	// Export includes pending source edits as well as commits.
	agent = agent.WithNewFile("pending.txt", "not committed")
	require.NoError(t, saveWorkspaceTo(ctx, c, agent, nil, checkout))
	contents, err := os.ReadFile(filepath.Join(checkout, "pending.txt"))
	require.NoError(t, err)
	require.Equal(t, "not committed", string(contents))
}

func (WorkspaceSuite) TestWorkspaceExportIntegrationConflicts(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	agentID, err := snapshotWorkspace(ctx, t, c, c.CurrentWorkspace()).WithNewFile("base.txt", "agent").With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "agent", workspaceCommitDate)
	}).ID(ctx)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "base.txt"), []byte("user"), 0o644))
	git("commit", "-am", "user")
	head := git("rev-parse", "HEAD")
	require.Error(t, saveWorkspaceTo(ctx, c, dagger.Ref[*dagger.Workspace](c, agentID), nil, checkout))
	require.Equal(t, head, git("rev-parse", "HEAD"))
	require.Empty(t, git("status", "--porcelain"))
}

func (WorkspaceSuite) TestWorkspaceExportPendingGitEdges(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	require.NoError(t, os.WriteFile(filepath.Join(checkout, ".gitignore"), []byte("ignored.txt\n"), 0o644))
	git("add", ".gitignore")
	git("commit", "-m", "ignore file")
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	baseID, err := snapshotWorkspace(ctx, t, c, c.CurrentWorkspace()).ID(ctx)
	require.NoError(t, err)
	base := dagger.Ref[*dagger.Workspace](c, baseID)
	head := git("rev-parse", "HEAD")
	require.NoError(t, saveWorkspaceTo(ctx, c, base.WithNewFile("ignored.txt", "intentional edit"), nil, checkout))
	contents, err := os.ReadFile(filepath.Join(checkout, "ignored.txt"))
	require.NoError(t, err)
	require.Equal(t, "intentional edit", string(contents))
	require.Equal(t, head, git("rev-parse", "HEAD"))
	require.Empty(t, git("status", "--porcelain"))
	empty := c.Directory().WithNewDirectory("empty").Changes(c.Directory())
	err = saveWorkspaceTo(ctx, c, base.WithChanges(empty), nil, checkout)
	require.ErrorContains(t, err, "cannot export empty directories")
	_, err = os.Stat(filepath.Join(checkout, "empty"))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Equal(t, head, git("rev-parse", "HEAD"))
}

func (WorkspaceSuite) TestWorkspaceExportCommitsAndOverlay(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	target := c.CurrentWorkspace()
	base, err := target.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	// Prime host reads before export; verify the actual disk writes below.
	_, err = target.File("base.txt").Contents(ctx)
	require.NoError(t, err)
	ws := snapshotWorkspace(ctx, t, c, target).WithNewFile("base.txt", "committed").
		WithNewFile("pending.txt", "pending").
		WithMountedDirectory("mounted", c.Directory().WithNewFile("private.txt", "mount"))
	committed, err := commitWorkspace(ctx, c, ws, "engine commit", []string{"base.txt"})
	require.NoError(t, err)
	frozen := dagger.Ref[*dagger.Workspace](c, committed.ID)
	require.Equal(t, base, git("rev-parse", "HEAD"))
	require.NoError(t, saveWorkspaceTo(ctx, c, frozen, nil, checkout))
	require.Equal(t, committed.Git.Head.Commit, git("rev-parse", "HEAD"))
	require.Equal(t, "engine commit", git("log", "-1", "--format=%s"))
	require.Equal(t, "?? pending.txt", git("status", "--porcelain"))
	for name, want := range map[string]string{"base.txt": "committed", "pending.txt": "pending"} {
		data, err := os.ReadFile(filepath.Join(checkout, name))
		require.NoError(t, err)
		require.Equal(t, want, string(data))
	}
	_, err = os.Stat(filepath.Join(checkout, "mounted"))
	require.ErrorIs(t, err, os.ErrNotExist)
	// Repeated exports are effectful and do not produce duplicate commits.
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "pending.txt"), []byte("changed outside"), 0o644))
	require.Error(t, saveWorkspaceTo(ctx, c, frozen, nil, checkout))
	data, err := os.ReadFile(filepath.Join(checkout, "pending.txt"))
	require.NoError(t, err)
	require.Equal(t, "changed outside", string(data))
	require.Equal(t, committed.Git.Head.Commit, git("rev-parse", "HEAD"))
	// The frozen source remains the same after saving.
	sha, err := frozen.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, committed.Git.Head.Commit, sha)
}

func (WorkspaceSuite) TestWorkspaceExportExplicitTargetAndCwd(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	require.NoError(t, os.MkdirAll(filepath.Join(checkout, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "sub", "initial.txt"), []byte("initial"), 0o644))
	git("add", ".")
	git("commit", "-m", "subdirectory")
	linked := filepath.Join(t.TempDir(), "linked")
	git("worktree", "add", "-b", "linked", linked)
	c := connect(ctx, t, dagger.WithWorkdir(filepath.Join(checkout, "sub")))
	committed, err := commitWorkspace(ctx, c, c.CurrentWorkspace().WithNewFile("a.txt", "a").WithNewFile("b.txt", "b"), "nested", []string{"sub/a.txt"})
	require.NoError(t, err)
	frozen := dagger.Ref[*dagger.Workspace](c, committed.ID)
	// Destination is another checkout; cwd must not shift repo-root paths.
	destination := connect(ctx, t, dagger.WithWorkdir(linked))
	require.NoError(t, saveWorkspaceTo(ctx, destination, dagger.Ref[*dagger.Workspace](destination, committed.ID), nil, "."))
	for _, name := range []string{"a.txt", "b.txt"} {
		data, err := os.ReadFile(filepath.Join(linked, "sub", name))
		require.NoError(t, err)
		require.Equal(t, strings.TrimSuffix(name, ".txt"), string(data))
	}
	require.NotEqual(t, committed.Git.Head.Commit, git("rev-parse", "HEAD"))
	require.Equal(t, committed.Git.Head.Commit, git("rev-parse", "refs/heads/linked"))
	err = frozen.Export(ctx)
	require.ErrorContains(t, err, "export frozen workspaces with an explicit path")
	err = saveWorkspaceTo(ctx, c, frozen, nil, t.TempDir())
	require.Error(t, err)
}

func (WorkspaceSuite) TestWorkspaceExportCheckpointWithoutCommits(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	target := c.CurrentWorkspace()
	frozen := snapshotWorkspace(ctx, t, c, target).WithNewFile("base.txt", "pending")
	_, err := frozen.ID(ctx)
	require.NoError(t, err)
	head := git("rev-parse", "HEAD")
	require.NoError(t, saveWorkspaceTo(ctx, c, frozen, nil, checkout))
	require.Equal(t, head, git("rev-parse", "HEAD"))
	require.Equal(t, "M base.txt", git("status", "--porcelain"))
}

func (WorkspaceSuite) TestWorkspaceExportUnrelatedAndUnborn(ctx context.Context, t *testctx.T) {
	for _, unborn := range []bool{false, true} {
		name := "unrelated"
		if unborn {
			name = "unborn"
		}
		t.Run(name, func(ctx context.Context, t *testctx.T) {
			sourcePath, sourceGit := workspaceExportCheckout(ctx, t)
			// Ensure the independent root commit cannot match the destination's.
			require.NoError(t, os.WriteFile(filepath.Join(sourcePath, "base.txt"), []byte("different root"), 0o644))
			sourceGit("checkout", "--orphan", "independent")
			sourceGit("add", ".")
			sourceGit("commit", "-m", "independent history")
			c := connect(ctx, t, dagger.WithWorkdir(sourcePath))
			frozen := snapshotWorkspace(ctx, t, c, c.CurrentWorkspace()).WithNewFile("pending.txt", "must not export")
			sourceID, err := frozen.ID(ctx)
			require.NoError(t, err)
			targetPath := t.TempDir()
			if unborn {
				initGitRepo(ctx, t, targetPath)
			} else {
				targetPath, _ = workspaceExportCheckout(ctx, t)
			}
			destination := connect(ctx, t, dagger.WithWorkdir(targetPath))
			err = saveWorkspaceTo(ctx, destination, dagger.Ref[*dagger.Workspace](destination, sourceID), nil, ".")
			require.Error(t, err)
			_, err = os.Stat(filepath.Join(targetPath, "pending.txt"))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func (WorkspaceSuite) TestExportCLI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := c.Directory().
		WithNewFile("dagger.toml", "[modules.broken]\nsource = \"does-not-exist\"\n").
		WithNewFile("root.txt", "from root\n").
		WithNewFile("items/.hidden", "hidden\n").
		WithNewFile("items/a.txt", "a\n").
		WithNewFile("items/skip.txt", "skip\n").
		WithNewFile("items/file with spaces.txt", "spaces\n").
		WithNewFile("items/bin/tool", "#!/bin/sh\n", dagger.DirectoryWithNewFileOpts{Permissions: 0o755}).
		WithNewFile("items/sub/deep.txt", "deep\n")
	base := c.Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithDirectory("/selected", source.WithNewDirectory(".git")).
		WithNewFile("/caller/merge/unrelated.txt", "unrelated\n").
		WithNewFile("/caller/existing/marker.txt", "marker\n").
		WithWorkdir("/caller")
	workspace := "/selected/items"

	t.Run("default cwd and directory merge", func(ctx context.Context, t *testctx.T) {
		result := base.With(workspaceSelectionDaggerExec(
			"-W", workspace, "ws", "export", "-o", "/caller/merge",
		))
		contents, err := result.File("/caller/merge/a.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "a\n", contents)
		contents, err = result.File("/caller/merge/.hidden").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "hidden\n", contents)
		contents, err = result.File("/caller/merge/sub/deep.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "deep\n", contents)
		contents, err = result.File("/caller/merge/unrelated.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "unrelated\n", contents)

		mode, err := result.WithExec([]string{"stat", "-c", "%a", "/caller/merge/bin/tool"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "755\n", mode)

		stderr, err := result.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, `Saved to "/caller/merge".`)
	})

	t.Run("absolute root file to file", func(ctx context.Context, t *testctx.T) {
		result := base.With(workspaceSelectionDaggerExec(
			"-W", workspace, "workspace", "export", "/root.txt", "-o", "/caller/root-copy.txt",
		))
		contents, err := result.File("/caller/root-copy.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "from root\n", contents)
		stderr, err := result.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, `Saved to "/caller/root-copy.txt".`)
	})

	t.Run("relative destination starts at client cwd", func(ctx context.Context, t *testctx.T) {
		result := base.With(workspaceSelectionDaggerExec(
			"-W", workspace, "ws", "export", "a.txt", "-o", "relative.txt",
		))
		contents, err := result.File("/caller/relative.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "a\n", contents)
	})

	t.Run("file to existing directory", func(ctx context.Context, t *testctx.T) {
		result := base.With(workspaceSelectionDaggerExec(
			"-W", workspace, "ws", "export", "a.txt", "-o", "/caller/existing",
		))
		contents, err := result.File("/caller/existing/a.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "a\n", contents)
		contents, err = result.File("/caller/existing/marker.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "marker\n", contents)
		stderr, err := result.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, `Saved to "/caller/existing".`)
	})

	t.Run("include and exclude", func(ctx context.Context, t *testctx.T) {
		result := base.With(workspaceSelectionDaggerExec(
			"-W", workspace, "ws", "export", ".", "-o", "/caller/filtered",
			"--include=**/*.txt", "--exclude=skip.txt",
		))
		contents, err := result.File("/caller/filtered/a.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "a\n", contents)
		contents, err = result.File("/caller/filtered/sub/deep.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "deep\n", contents)
		_, err = result.File("/caller/filtered/skip.txt").Sync(ctx)
		require.Error(t, err)
		_, err = result.File("/caller/filtered/bin/tool").Sync(ctx)
		require.Error(t, err)
	})

	t.Run("path and destination with spaces", func(ctx context.Context, t *testctx.T) {
		result := base.With(workspaceSelectionDaggerExec(
			"-W", workspace, "ws", "export", "file with spaces.txt", "-o", "/caller/copy with spaces.txt",
		))
		contents, err := result.File("/caller/copy with spaces.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "spaces\n", contents)
	})

	t.Run("file rejects filters", func(ctx context.Context, t *testctx.T) {
		result := base.WithExec([]string{
			"dagger", "-W", workspace, "ws", "export", "a.txt", "-o", "/caller/rejected", "--include=*.txt",
		}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
		stderr, err := result.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, "--include and --exclude can only be used with a directory")
	})

	t.Run("missing path", func(ctx context.Context, t *testctx.T) {
		result := base.WithExec([]string{
			"dagger", "-W", workspace, "ws", "export", "missing", "-o", "/caller/missing",
		}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
		stderr, err := result.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, "no such file or directory")
	})

	t.Run("output is required", func(ctx context.Context, t *testctx.T) {
		result := base.WithExec([]string{
			"dagger", "-W", workspace, "ws", "export", "a.txt",
		}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
		stderr, err := result.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, "--output is required")
	})

	t.Run("remote workspace exports to caller", func(ctx context.Context, t *testctx.T) {
		ref := workspaceSelectionRemoteRef(ctx, t, c, source)
		remote := strings.Replace(ref, "/repo.git@", "/repo.git/items@", 1)
		result := base.With(workspaceSelectionDaggerExec(
			"-W", remote, "ws", "export", "sub", "-o", "/caller/remote",
		))
		contents, err := result.File("/caller/remote/deep.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "deep\n", contents)
	})
}

// Export always applies root-relative changes at the stored host root, even
// when invoked from a module directory and compared with a workspace at the root.
func (WorkspaceSuite) TestWorkspaceExportLocalWorkdirAndFrom(ctx context.Context, t *testctx.T) {
	for _, incremental := range []bool{false, true} {
		name := "cumulative"
		if incremental {
			name = "from baseline"
		}
		t.Run(name, func(ctx context.Context, t *testctx.T) {
			checkout, git := workspaceExportCheckout(ctx, t)
			modulePath := filepath.Join(".dagger", "modules", "foo")
			moduleDir := filepath.Join(checkout, modulePath)
			require.NoError(t, os.MkdirAll(moduleDir, 0o755))
			for name, contents := range map[string]string{"prior.txt": "host prior", "modified.txt": "before", "removed.txt": "remove me"} {
				require.NoError(t, os.WriteFile(filepath.Join(moduleDir, name), []byte(contents), 0o644))
			}
			git("add", ".")
			git("commit", "-m", "module files")
			head := git("rev-parse", "HEAD")
			c := connect(ctx, t, dagger.WithWorkdir(moduleDir))
			baseline := c.CurrentWorkspace().WithNewFile("prior.txt", "earlier overlay")
			baselineID, err := baseline.ID(ctx)
			require.NoError(t, err)
			baseline = dagger.Ref[*dagger.Workspace](c, baselineID)
			after := baseline.WithNewFile("generated.txt", "generated").
				WithNewFile("modified.txt", "after").WithoutFile("removed.txt").
				WithNewFile("/root-generated.txt", "root generated").
				WithMountedDirectory("mount", c.Directory().WithNewFile("private.txt", "private"))
			opts := dagger.WorkspaceExportOpts{}
			if incremental {
				// The comparator's cwd does not change the coordinate system used
				// to export this workspace's changes.
				opts.From = baseline.WithWorkdir(".")
			}
			require.NoError(t, after.Export(ctx, opts))
			wantPrior := "earlier overlay"
			if incremental {
				wantPrior = "host prior"
			}
			for name, want := range map[string]string{"generated.txt": "generated", "modified.txt": "after", "prior.txt": wantPrior} {
				contents, err := os.ReadFile(filepath.Join(moduleDir, name))
				require.NoError(t, err)
				require.Equal(t, want, string(contents))
			}
			contents, err := os.ReadFile(filepath.Join(checkout, "root-generated.txt"))
			require.NoError(t, err)
			require.Equal(t, "root generated", string(contents))
			for _, path := range []string{filepath.Join(moduleDir, modulePath), filepath.Join(moduleDir, "removed.txt"), filepath.Join(moduleDir, "mount"), filepath.Join(checkout, "generated.txt")} {
				_, err := os.Stat(path)
				require.ErrorIs(t, err, os.ErrNotExist)
			}
			require.Equal(t, head, git("rev-parse", "HEAD"))
			require.Empty(t, git("diff", "--cached"))
			if incremental {
				require.NoError(t, after.Export(ctx, dagger.WorkspaceExportOpts{From: after}), "equal source and baseline are a no-op")
			}
		})
	}
}

// A session caches host reads per client for the client's whole lifetime, so
// an export that changes the files on disk must invalidate them: both the
// reads an agent makes afterwards and the host baseline the next export diffs
// its overlay against. Without that, exporting VERSION=2 and then VERSION=1
// leaves 2 on disk because the second export compares against a baseline that
// still says 1.
func (WorkspaceSuite) TestWorkspaceExportRereadsHost(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	versionPath := filepath.Join(checkout, "VERSION")
	require.NoError(t, os.WriteFile(versionPath, []byte("1"), 0o644))
	git("add", ".")
	git("commit", "-m", "version 1")
	c := connect(ctx, t, dagger.WithWorkdir(checkout))

	// Prime the per-client host cache, as an agent reading before editing does.
	contents, err := c.CurrentWorkspace().File("VERSION").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "1", contents)

	require.NoError(t, c.CurrentWorkspace().WithNewFile("VERSION", "2").Export(ctx))
	onDisk, err := os.ReadFile(versionPath)
	require.NoError(t, err)
	require.Equal(t, "2", string(onDisk))

	// Reads after the export observe the exported contents, not the snapshot
	// cached by the earlier read.
	contents, err = c.CurrentWorkspace().File("VERSION").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "2", contents)

	// A second export diffs against what is on disk now, so reverting to the
	// original contents is a real change and is written.
	require.NoError(t, c.CurrentWorkspace().WithNewFile("VERSION", "1").Export(ctx))
	onDisk, err = os.ReadFile(versionPath)
	require.NoError(t, err)
	require.Equal(t, "1", string(onDisk))
	contents, err = c.CurrentWorkspace().File("VERSION").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "1", contents)
}
