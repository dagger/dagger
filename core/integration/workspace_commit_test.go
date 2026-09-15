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
	ID  dagger.ID
	Git struct {
		Repository struct{ URL *string }
		Head       struct {
			Commit       string
			TargetCommit struct{ Message, AuthorName, AuthorEmail, AuthoredDate, CommittedDate string }
		}
		Uncommitted struct{ AddedPaths, ModifiedPaths, RemovedPaths []string }
	}
}

func commitWorkspace(ctx context.Context, c *dagger.Client, ws *dagger.Workspace, message string, paths []string) (got workspaceCommitState, err error) {
	got.ID, err = ws.WithCommit(message, workspaceCommitDate, dagger.WorkspaceWithCommitOpts{Paths: paths}).ID(ctx)
	if err != nil {
		return got, err
	}
	// Resolve the result once before reading its fields: metadata reads must
	// not repeat the workspace's host capture or commit operation.
	committed := dagger.Ref[*dagger.Workspace](c, got.ID)
	head := committed.Git().Head()
	meta := head.TargetCommit()
	for _, field := range []struct {
		target *string
		read   func(context.Context) (string, error)
	}{
		{&got.Git.Head.Commit, head.CommitSHA},
		{&got.Git.Head.TargetCommit.Message, meta.Message},
		{&got.Git.Head.TargetCommit.AuthorName, meta.AuthorName},
		{&got.Git.Head.TargetCommit.AuthorEmail, meta.AuthorEmail},
		{&got.Git.Head.TargetCommit.AuthoredDate, meta.AuthoredDate},
		{&got.Git.Head.TargetCommit.CommittedDate, meta.CommittedDate},
	} {
		*field.target, err = field.read(ctx)
		if err != nil {
			return got, err
		}
	}
	url, err := head.AsRepository().URL(ctx)
	if err != nil {
		return got, err
	}
	if url != "" {
		got.Git.Repository.URL = &url
	}
	pending := committed.Git().Uncommitted()
	for _, field := range []struct {
		target *[]string
		read   func(context.Context) ([]string, error)
	}{
		{&got.Git.Uncommitted.AddedPaths, pending.AddedPaths},
		{&got.Git.Uncommitted.ModifiedPaths, pending.ModifiedPaths},
		{&got.Git.Uncommitted.RemovedPaths, pending.RemovedPaths},
	} {
		*field.target, err = field.read(ctx)
		if err != nil {
			return got, err
		}
	}
	return got, nil
}

func (WorkspaceSuite) TestWorkspaceWithCommitScopedHistory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().
		WithNewFile("src/a.txt", "old-a").WithNewFile("src/b.txt", "old-b").
		WithNewFile("keep.txt", "untouched"))
	ws := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).
		Branch("main").AsWorkspace(dagger.GitRefAsWorkspaceOpts{Cwd: "src"}).
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
	equivalent, err := commitWorkspace(ctx, c, ws.WithConfigEnvironment(""), "same message", []string{"a.txt"})
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
	// Commit authorship is sampled at commit time, even if the workspace
	// was loaded before the client changed its Git config.
	git("config", "user.name", "Later Author")
	git("config", "user.email", "later@example.com")
	headBefore, statusBefore := git("rev-parse", "HEAD"), git("status", "--porcelain")
	committed, err := commitWorkspace(ctx, c, ws, "engine commit", nil)
	require.NoError(t, err)
	require.Contains(t, workspaceRecipeFields(ctx, t, c, string(committed.ID)), "__gitDir")
	require.NotEqual(t, headBefore, committed.Git.Head.Commit)
	require.Equal(t, "Later Author", committed.Git.Head.TargetCommit.AuthorName)
	require.Equal(t, "later@example.com", committed.Git.Head.TargetCommit.AuthorEmail)
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

func (WorkspaceSuite) TestWorkspaceWithCommitSignoff(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	git("config", "user.name", "Inherited Author")
	git("config", "user.email", "inherited@example.com")
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	ws := c.CurrentWorkspace().WithNewFile("base.txt", "changed")
	const message = "subject\n\nCommit body."

	t.Run("does not sign off by default", func(ctx context.Context, t *testctx.T) {
		got, err := ws.WithCommit(message, workspaceCommitDate).Git().Head().TargetCommit().Message(ctx)
		require.NoError(t, err)
		require.Equal(t, message, strings.TrimSpace(got))
	})

	for _, tc := range []struct {
		name        string
		opts        dagger.WorkspaceWithCommitOpts
		authorName  string
		authorEmail string
	}{
		{
			name:       "inherited identity",
			opts:       dagger.WorkspaceWithCommitOpts{Signoff: true},
			authorName: "Inherited Author", authorEmail: "inherited@example.com",
		},
		{
			name:       "explicit identity",
			opts:       dagger.WorkspaceWithCommitOpts{Signoff: true, AuthorName: "Explicit Author", AuthorEmail: "explicit@example.com"},
			authorName: "Explicit Author", authorEmail: "explicit@example.com",
		},
		{
			name:       "explicit name with inherited email",
			opts:       dagger.WorkspaceWithCommitOpts{Signoff: true, AuthorName: "Explicit Author"},
			authorName: "Explicit Author", authorEmail: "inherited@example.com",
		},
		{
			name:       "inherited name with explicit email",
			opts:       dagger.WorkspaceWithCommitOpts{Signoff: true, AuthorEmail: "explicit@example.com"},
			authorName: "Inherited Author", authorEmail: "explicit@example.com",
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			id, err := ws.WithCommit(message, workspaceCommitDate, tc.opts).ID(ctx)
			require.NoError(t, err)
			meta := dagger.Ref[*dagger.Workspace](c, id).Git().Head().TargetCommit()
			got, err := meta.Message(ctx)
			require.NoError(t, err)
			require.Equal(t, message+"\n\nSigned-off-by: "+tc.authorName+" <"+tc.authorEmail+">", strings.TrimSpace(got))
			name, err := meta.AuthorName(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.authorName, name)
			email, err := meta.AuthorEmail(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.authorEmail, email)
		})
	}
}

func (WorkspaceSuite) TestWorkspaceWithResetAmendsHistory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().WithNewFile("base.txt", "base"))
	ws := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Branch("main").AsWorkspace()
	baseSHA, err := ws.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)

	committed := ws.
		WithNewFile("feature.txt", "feature").
		WithNewFile("pending.txt", "pending").
		WithCommit("draft mesage", workspaceCommitDate, dagger.WorkspaceWithCommitOpts{Paths: []string{"feature.txt"}})
	draftSHA, err := committed.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.NotEqual(t, baseSHA, draftSHA)

	// A mixed reset moves HEAD back while the reverted commit's changes and
	// the still-pending edit both stay uncommitted.
	reset := committed.WithReset(baseSHA)
	resetSHA, err := reset.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, baseSHA, resetSHA)
	added, err := reset.Git().Uncommitted().AddedPaths(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"feature.txt", "pending.txt"}, added)
	contents, err := reset.File("feature.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "feature", contents)
	// Reapplying the same paths with a corrected message is the amend flow.
	amended, err := commitWorkspace(ctx, c, reset, "draft message, amended", []string{"feature.txt"})
	require.NoError(t, err)
	require.NotEqual(t, draftSHA, amended.Git.Head.Commit)
	require.Equal(t, "draft message, amended", strings.TrimSpace(amended.Git.Head.TargetCommit.Message))
	require.Equal(t, "Dagger", amended.Git.Head.TargetCommit.AuthorName)
	require.Equal(t, []string{"pending.txt"}, amended.Git.Uncommitted.AddedPaths)
	require.NotNil(t, amended.Git.Repository.URL)
	require.Equal(t, url, *amended.Git.Repository.URL)
	log, err := dagger.Ref[*dagger.Workspace](c, amended.ID).Git().Head().Log(ctx)
	require.NoError(t, err)
	require.Len(t, log, 2)
	feature, err := dagger.Ref[*dagger.Workspace](c, amended.ID).Git().Head().Tree().File("feature.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "feature", feature)

	// A hard reset discards everything since the commit, pending edits included.
	hard := committed.WithReset(baseSHA, dagger.WorkspaceWithResetOpts{Hard: true})
	hardSHA, err := hard.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, baseSHA, hardSHA)
	hardAdded, err := hard.Git().Uncommitted().AddedPaths(ctx)
	require.NoError(t, err)
	require.Empty(t, hardAdded)
	_, err = hard.File("feature.txt").Contents(ctx)
	require.Error(t, err)

	// Input values are immutable.
	oldSHA, err := committed.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, draftSHA, oldSHA)

	// Resetting orphans the commits after the target: the frozen repository
	// keeps reachable history only, so the reverted draft cannot be returned
	// to. The amend flow recreates it from the uncommitted changes instead.
	_, err = reset.WithReset(draftSHA).Git().Head().CommitSHA(ctx)
	require.ErrorContains(t, err, "is not in this workspace's repository")

	_, err = committed.WithReset("main").Git().Head().CommitSHA(ctx)
	require.ErrorContains(t, err, "full lowercase commit hash")
	missing := strings.Repeat("ab", 20)
	_, err = committed.WithReset(missing).Git().Head().CommitSHA(ctx)
	require.ErrorContains(t, err, "is not in this workspace's repository")
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
	// the outer client, then create another commit using a new client identity.
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
	checkout, git := workspaceExportCheckout(ctx, t)
	git("config", "user.name", "Restoring Author")
	git("config", "user.email", "restoring@example.com")
	restoring := connect(ctx, t, dagger.WithWorkdir(checkout))
	restored = dagger.Ref[*dagger.LLM](restoring, dagger.ID(strings.TrimSpace(recipe))).Workspace()
	next, err := commitWorkspace(ctx, restoring, restored.WithNewFile("next.txt", "next"), "next commit", nil)
	require.NoError(t, err)
	require.Equal(t, "Restoring Author", next.Git.Head.TargetCommit.AuthorName)
	require.Equal(t, "restoring@example.com", next.Git.Head.TargetCommit.AuthorEmail)
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
		WithConfigEnvironment("testing").
		WithMountedDirectory("/mounted", c.Directory().WithNewFile("readme", "read-only"))
	partial, err := commitWorkspace(ctx, c, ws, "literal path", []string{":literal"})
	require.NoError(t, err)
	committed, err := commitWorkspace(ctx, c, dagger.Ref[*dagger.Workspace](c, partial.ID), "file kinds", nil)
	require.NoError(t, err)
	require.Equal(t, "Dagger", committed.Git.Head.TargetCommit.AuthorName)
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
	next, err := commitWorkspace(ctx, c, frozen.WithNewFile("next", "next"), "next commit", nil)
	require.NoError(t, err)
	require.Equal(t, committed.Git.Head.TargetCommit.AuthorName, next.Git.Head.TargetCommit.AuthorName)
}

func (WorkspaceSuite) TestWorkspaceWithCommitDirectoryRepository(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().WithNewFile("base.txt", "base"))
	directory := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).
		Branch("main").Tree().WithNewFile("base.txt", "changed")
	committed, err := commitWorkspace(ctx, c, directory.AsWorkspace(), "directory repo", nil)
	require.NoError(t, err)
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

func (WorkspaceSuite) TestWorkspaceWithCommitLiteralPaths(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().WithNewFile("a*.txt", "old").WithNewFile("abc.txt", "old").WithNewFile("dir[1]/file", "old"))
	ws := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Head().AsWorkspace().
		WithNewFile("a*.txt", "literal").WithNewFile("abc.txt", "unselected").WithNewFile("dir[1]/file", "selected directory")
	result := ws.WithCommit("literal paths", workspaceCommitDate, dagger.WorkspaceWithCommitOpts{Paths: []string{"a*.txt", "dir[1]"}})
	for file, want := range map[string]string{"a*.txt": "literal", "abc.txt": "old", "dir[1]/file": "selected directory"} {
		got, err := result.Git().Head().Tree().File(file).Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	modified, err := result.Git().Uncommitted().ModifiedPaths(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"abc.txt"}, modified)
}
