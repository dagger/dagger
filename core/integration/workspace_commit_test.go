package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

const workspaceCommitDate = "2026-09-05T12:00:00Z"

type workspaceCommitState struct {
	ID       dagger.ID
	Portable bool
	Git      struct {
		Repository struct{ URL *string }
		Head       struct {
			Commit       string
			TargetCommit struct{ Message, AuthorName, AuthorEmail, AuthoredDate, CommittedDate string }
		}
		Uncommitted struct{ AddedPaths, ModifiedPaths, RemovedPaths []string }
	}
}

func commitWorkspace(ctx context.Context, c *dagger.Client, ws *dagger.Workspace, message string, paths []string) (workspaceCommitState, error) {
	var got struct {
		Node struct{ WithCommit workspaceCommitState }
	}
	id, err := ws.ID(ctx)
	if err != nil {
		return got.Node.WithCommit, err
	}
	if paths == nil {
		paths = []string{}
	}
	err = c.Do(ctx, &dagger.Request{
		Query: `query($id: ID!, $message: String!, $paths: [String!]!, $date: String!) {
			node(id: $id) { ... on Workspace { withCommit(message: $message, paths: $paths, date: $date) {
				id portable git {
					repository: __repository { url }
					head { commit targetCommit { message authorName authorEmail authoredDate committedDate } }
					uncommitted { addedPaths modifiedPaths removedPaths }
				}
			} } }
		}`,
		Variables: map[string]any{"id": id, "message": message, "paths": paths, "date": workspaceCommitDate},
	}, &dagger.Response{Data: &got})
	return got.Node.WithCommit, err
}

func (WorkspaceSuite) TestWorkspaceWithCommitScopedHistory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().
		WithNewFile("src/a.txt", "old-a").WithNewFile("src/b.txt", "old-b").
		WithNewFile("keep.txt", "untouched"))
	// Even keepGitDir:false must support commits; commit materialization needs
	// a full-history repository, independently of ordinary tree-read options.
	serviceID, err := daemon.ID(ctx)
	require.NoError(t, err)
	var source struct {
		Git struct {
			Branch struct{ AsWorkspace struct{ ID dagger.ID } }
		}
	}
	// Use GraphQL directly: the Go SDK omits zero-valued optional booleans.
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query: `query($url: String!, $service: ID!) {
			git(url: $url, experimentalServiceHost: $service, keepGitDir: false) {
				branch(name: "main") { asWorkspace(cwd: "src") { id } }
			}
		}`,
		Variables: map[string]any{"url": url, "service": serviceID},
	}, &dagger.Response{Data: &source}))
	ws := dagger.Ref[*dagger.Workspace](c, source.Git.Branch.AsWorkspace.ID).
		WithNewFile("a.txt", "new-a").WithNewFile("b.txt", "new-b")
	baseSHA, err := ws.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	first, err := commitWorkspace(ctx, c, ws, "same message", []string{"a.txt"})
	require.NoError(t, err)
	require.NotEqual(t, baseSHA, first.Git.Head.Commit)
	require.Equal(t, []string{"src/b.txt"}, first.Git.Uncommitted.ModifiedPaths)
	require.Equal(t, "Dagger", first.Git.Head.TargetCommit.AuthorName)
	require.Equal(t, "dagger@localhost", first.Git.Head.TargetCommit.AuthorEmail)
	require.Equal(t, workspaceCommitDate, first.Git.Head.TargetCommit.AuthoredDate)
	require.Equal(t, workspaceCommitDate, first.Git.Head.TargetCommit.CommittedDate)
	require.True(t, first.Portable)
	require.NotNil(t, first.Git.Repository.URL)
	require.Equal(t, url, *first.Git.Repository.URL)
	frozen := dagger.Ref[*dagger.Workspace](c, first.ID)
	for file, expected := range map[string]string{"a.txt": "new-a", "b.txt": "new-b", "/keep.txt": "untouched"} {
		contents, err := frozen.File(file).Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, expected, contents)
	}
	repeated, err := commitWorkspace(ctx, c, ws, "same message", []string{"a.txt"})
	require.NoError(t, err)
	require.Equal(t, first.Git.Head.Commit, repeated.Git.Head.Commit)
	// Force a different recipe with the same resolved identity and tree, so
	// this checks Git determinism rather than just a cache hit.
	equivalent, err := commitWorkspace(ctx, c, ws.WithGitAuthor("Dagger", "dagger@localhost"), "same message", []string{"a.txt"})
	require.NoError(t, err)
	require.Equal(t, first.Git.Head.Commit, equivalent.Git.Head.Commit)
	laterSHA, err := ws.WithCommit("same message", "2026-09-05T12:00:01Z", dagger.WorkspaceWithCommitOpts{Paths: []string{"a.txt"}}).Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.NotEqual(t, first.Git.Head.Commit, laterSHA)
	second, err := commitWorkspace(ctx, c, frozen, "same message", nil)
	require.NoError(t, err)
	require.NotEqual(t, first.Git.Head.Commit, second.Git.Head.Commit)
	require.NotNil(t, second.Git.Repository.URL)
	require.Equal(t, url, *second.Git.Repository.URL)
	require.Empty(t, second.Git.Uncommitted.ModifiedPaths)
	require.Empty(t, second.Git.Uncommitted.AddedPaths)
	require.Empty(t, second.Git.Uncommitted.RemovedPaths)
	log, err := dagger.Ref[*dagger.Workspace](c, second.ID).Git().Head().Log(ctx)
	require.NoError(t, err)
	require.Len(t, log, 3)
	_, err = commitWorkspace(ctx, c, dagger.Ref[*dagger.Workspace](c, second.ID), "same message", nil)
	require.ErrorContains(t, err, "nothing to commit")
	// Input values are immutable, even after a path-scoped and a full commit.
	oldSHA, err := ws.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, baseSHA, oldSHA)

	recipe, err := c.LLM().WithWorkspace(dagger.Ref[*dagger.Workspace](c, second.ID)).PortableID(ctx)
	require.NoError(t, err)
	id := new(call.ID)
	require.NoError(t, id.Decode(string(recipe)))
	dag, err := id.ToProto()
	require.NoError(t, err)
	for _, vertex := range dag.GetRecipe().CallsByDigest {
		require.NotContains(t, []string{"currentWorkspace", "checkpoint", "withCommit", "branch"}, vertex.Field)
	}
	restored := dagger.Ref[*dagger.LLM](c, recipe).Workspace()
	restoredSHA, err := restored.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, second.Git.Head.Commit, restoredSHA)
}

func (WorkspaceSuite) TestWorkspaceWithCommitFreezesHostAndAuthor(ctx context.Context, t *testctx.T) {
	checkout := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = checkout
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	git("config", "user.name", "Original Author")
	git("config", "user.email", "original@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "a.txt"), []byte("old"), 0o644))
	git("add", ".")
	git("commit", "-m", "initial")
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "a.txt"), []byte("new"), 0o644))
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	ws := c.CurrentWorkspace()
	_, err := ws.ID(ctx)
	require.NoError(t, err)
	// Identity is captured at load, not sampled again during checkpoint/commit.
	git("config", "user.name", "Later Author")
	git("config", "user.email", "later@example.com")
	headBefore, statusBefore := git("rev-parse", "HEAD"), git("status", "--porcelain")
	committed, err := commitWorkspace(ctx, c, ws, "engine commit", nil)
	require.NoError(t, err)
	require.False(t, committed.Portable)
	require.NotEqual(t, headBefore, committed.Git.Head.Commit)
	require.Equal(t, "Original Author", committed.Git.Head.TargetCommit.AuthorName)
	require.Equal(t, "original@example.com", committed.Git.Head.TargetCommit.AuthorEmail)
	require.Empty(t, committed.Git.Uncommitted.ModifiedPaths)
	require.Equal(t, headBefore, git("rev-parse", "HEAD"))
	require.Equal(t, statusBefore, git("status", "--porcelain"))
	contents, err := os.ReadFile(filepath.Join(checkout, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "new", string(contents))

	require.NoError(t, os.WriteFile(filepath.Join(checkout, "untracked.txt"), []byte("not approved"), 0o644))
	_, err = commitWorkspace(ctx, c, ws, "must request approval", nil)
	require.ErrorContains(t, err, "untracked.txt")
	require.NotContains(t, err.Error(), "not approved")
	require.Equal(t, headBefore, git("rev-parse", "HEAD"))
}

func (WorkspaceSuite) TestWorkspaceWithCommitValidation(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().WithNewFile("old.txt", strings.Repeat("rename me\n", 20)))
	ws := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Branch("main").AsWorkspace().
		WithoutFile("old.txt").WithNewFile("new.txt", strings.Repeat("rename me\n", 20))
	_, err := commitWorkspace(ctx, c, ws, "rename", []string{"new.txt"})
	require.ErrorContains(t, err, "split the rename")
	committed, err := commitWorkspace(ctx, c, ws, "rename", []string{"old.txt", "new.txt"})
	require.NoError(t, err)
	require.Empty(t, committed.Git.Uncommitted.AddedPaths)
	require.Empty(t, committed.Git.Uncommitted.RemovedPaths)
	_, err = commitWorkspace(ctx, c, ws, "", nil)
	require.ErrorContains(t, err, "message must be nonempty")
	_, err = commitWorkspace(ctx, c, ws, "bad path", []string{"../outside"})
	require.Error(t, err)
	_, err = commitWorkspace(ctx, c, ws, "metadata", []string{".git/config"})
	require.ErrorContains(t, err, "Git metadata")
	_, err = commitWorkspace(ctx, c, c.Directory().WithNewFile("a", "no Git").AsWorkspace(), "no repo", nil)
	require.ErrorContains(t, err, "not in a git repository")
}

func (WorkspaceSuite) TestWorkspaceWithCommitRestoresWithoutClient(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := checkpointCheckoutBase(ctx, t, c)
	recipe, err := base.With(daggerShell(`llm | with-workspace --workspace $(current-workspace | with-commit --message "frozen commit" --date "2026-09-05T12:00:00Z") | portable-id`)).Stdout(ctx)
	require.NoError(t, err)
	// The CLI's owning client and checkout are gone. Restore the recipe from
	// the outer client, then create another commit using its carried identity.
	restored := dagger.Ref[*dagger.LLM](c, dagger.ID(strings.TrimSpace(recipe))).Workspace()
	message, err := restored.Git().Head().TargetCommit().Message(ctx)
	require.NoError(t, err)
	require.Equal(t, "frozen commit", strings.TrimSpace(message))
	contents, err := restored.File("tracked.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "base\ndirty\n", contents)
	log, err := restored.Git().Head().Log(ctx)
	require.NoError(t, err)
	require.Len(t, log, 3)
	portable, err := restored.Portable(ctx)
	require.NoError(t, err)
	require.True(t, portable)
	next, err := commitWorkspace(ctx, c, restored.WithNewFile("next.txt", "next"), "next commit", nil)
	require.NoError(t, err)
	require.Equal(t, "Checkpoint", next.Git.Head.TargetCommit.AuthorName)
	require.Equal(t, "checkpoint@example.com", next.Git.Head.TargetCommit.AuthorEmail)
}

func (WorkspaceSuite) TestWorkspaceWithCommitFileKindsAndMetadata(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().
		WithNewFile("delete/a", "a").WithNewFile("delete/b", "b").
		WithNewFile("binary", "before\x00binary"))
	ws := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Branch("main").AsWorkspace().
		WithoutDirectory("delete").
		WithNewFile("binary", "after\x00binary").
		WithNewFile("run", "#!/bin/sh\n", dagger.WorkspaceWithNewFileOpts{Permissions: 0o755}).
		WithDirectory("/", c.Directory().WithSymlink("binary", "link")).
		WithNewFile(":literal", "pathspecs are literal").
		WithGitAuthor("Carried", "carried@example.com").
		WithConfigEnvironment("testing").
		WithMountedDirectory("/mounted", c.Directory().WithNewFile("readme", "read-only"))
	partial, err := commitWorkspace(ctx, c, ws, "literal path", []string{":literal"})
	require.NoError(t, err)
	committed, err := commitWorkspace(ctx, c, dagger.Ref[*dagger.Workspace](c, partial.ID), "file kinds", nil)
	require.NoError(t, err)
	require.Equal(t, "Carried", committed.Git.Head.TargetCommit.AuthorName)
	require.Empty(t, committed.Git.Uncommitted.AddedPaths)
	require.Empty(t, committed.Git.Uncommitted.ModifiedPaths)
	require.Empty(t, committed.Git.Uncommitted.RemovedPaths)
	frozen := dagger.Ref[*dagger.Workspace](c, committed.ID)
	tree := frozen.Git().Head().Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
	out, err := c.Container().From(alpineImage).WithDirectory("/tree", tree).WithWorkdir("/tree").
		WithExec([]string{"sh", "-ec", "test -x run; test ! -e delete; test ! -e mounted; test -L link; readlink link"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "binary\n", out)
	data, err := frozen.File("binary").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "after\x00binary", data)
	mounted, err := frozen.File("/mounted/readme").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "read-only", mounted)
	override := frozen.WithNewFile("override", "override").WithCommit("override", workspaceCommitDate,
		dagger.WorkspaceWithCommitOpts{AuthorName: "Explicit", AuthorEmail: "explicit@example.com"})
	name, err := override.Git().Head().TargetCommit().AuthorName(ctx)
	require.NoError(t, err)
	require.Equal(t, "Explicit", name)
	next, err := commitWorkspace(ctx, c, override.WithNewFile("next", "next"), "carried again", nil)
	require.NoError(t, err)
	require.Equal(t, "Carried", next.Git.Head.TargetCommit.AuthorName)
}

func (WorkspaceSuite) TestWorkspaceWithCommitDirectoryRepository(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().WithNewFile("base.txt", "base"))
	directory := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon, KeepGitDir: true}).
		Branch("main").Tree().WithNewFile("base.txt", "changed")
	committed, err := commitWorkspace(ctx, c, directory.AsWorkspace(), "directory repo", nil)
	require.NoError(t, err)
	require.True(t, committed.Portable)
	require.Empty(t, committed.Git.Uncommitted.ModifiedPaths)
	frozen := dagger.Ref[*dagger.Workspace](c, committed.ID)
	contents, err := frozen.File("base.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "changed", contents)
	log, err := frozen.Git().Head().Log(ctx)
	require.NoError(t, err)
	require.Len(t, log, 2)
}

func (WorkspaceSuite) TestWorkspaceWithCommitLoadsCommittedModules(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().
		WithNewFile("dagger.toml", "[modules.probe]\nsource = \"modules/probe\"\n").
		WithNewFile("modules/probe/dagger.json", `{"name":"probe","engineVersion":"v1.0.0","sdk":"go"}`).
		WithNewFile("modules/probe/main.go", "package main\ntype Probe struct{}\n"))
	ws := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Branch("main").AsWorkspace().
		WithNewFile("modules/probe/main.go", `package main
type Probe struct{}
// +check
func (*Probe) Committed() error { return nil }
`)
	committed, err := commitWorkspace(ctx, c, ws, "add module", nil)
	require.NoError(t, err)
	checks, err := dagger.Ref[*dagger.Workspace](c, committed.ID).Checks(dagger.WorkspaceChecksOpts{NoGenerate: true}).List(ctx)
	require.NoError(t, err)
	require.Len(t, checks, 1)
	name, err := checks[0].Name(ctx)
	require.NoError(t, err)
	require.Equal(t, "probe:committed", name)
}
