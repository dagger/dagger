package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestWorkspaceGitDirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().
		WithNewFile("base.txt", "base").WithNewFile("removed.txt", "remove me"))
	base := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Head().AsWorkspace()
	baseSHA, err := base.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	// Check the remote backend directly, before commits switch to a local one.
	clean := workspaceGitDirectoryContainer(c, base)
	out, err := clean.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, baseSHA, strings.TrimSpace(out))

	ws := base.WithNewFile("committed.txt", "agent commit").
		WithCommit("agent commit", workspaceCommitDate).
		WithNewFile("base.txt", "pending edit").
		WithoutFile("removed.txt").
		WithNewFile("untracked.txt", "pending addition")
	head, err := ws.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	entries, err := ws.Directory("/").Entries(ctx)
	require.NoError(t, err)
	require.NotContains(t, entries, ".git")
	require.NotContains(t, entries, ".git/")
	gitEntries, err := ws.Git().Directory().Entries(ctx)
	require.NoError(t, err)
	require.Contains(t, gitEntries, "HEAD")
	require.Contains(t, gitEntries, "objects/")
	require.Contains(t, gitEntries, "index")
	require.NotContains(t, gitEntries, ".git", "return metadata contents, not a checkout")
	require.NotContains(t, gitEntries, ".git/")

	ctr := workspaceGitDirectoryContainer(c, ws)
	out, err = ctr.WithExec([]string{"git", "log", "--format=%H"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{head, baseSHA}, strings.Fields(out))
	out, err = ctr.WithExec([]string{"git", "rev-parse", "--is-shallow-repository"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "false\n", out)
	out, err = ctr.WithExec([]string{"git", "status", "--porcelain"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, " M base.txt\n D removed.txt\n?? untracked.txt\n", out)
	_, err = ctr.WithExec([]string{"git", "bundle", "create", "/tmp/history.bundle", "HEAD"}).
		WithExec([]string{"git", "bundle", "verify", "/tmp/history.bundle"}).Sync(ctx)
	require.NoError(t, err)

	// Git can write into the mounted metadata, without changing the Workspace.
	_, err = ctr.WithExec([]string{"git", "add", "-A"}).
		WithExec([]string{"git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "container edit"}).Sync(ctx)
	require.NoError(t, err)
	again, err := ws.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, head, again)

	_, err = c.Directory().WithNewFile("file.txt", "no repository").AsWorkspace().Git().Directory().Entries(ctx)
	require.Error(t, err)
}

func workspaceGitDirectoryContainer(c *dagger.Client, ws *dagger.Workspace) *dagger.Container {
	return c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithMountedDirectory("/src", ws.Directory("/")).
		WithMountedDirectory("/src/.git", ws.Git().Directory()).
		WithWorkdir("/src")
}

func (WorkspaceSuite) TestWorkspaceGitDirectoryWorktree(ctx context.Context, t *testctx.T) {
	_, git := workspaceExportCheckout(ctx, t)
	linked := filepath.Join(t.TempDir(), "linked")
	git("worktree", "add", "-b", "feature", linked)
	require.NoError(t, os.WriteFile(filepath.Join(linked, "feature.txt"), []byte("feature"), 0o644))
	git("-C", linked, "add", ".")
	git("-C", linked, "commit", "-m", "feature")
	head := git("-C", linked, "rev-parse", "HEAD")
	// A staged edit must become an unstaged edit against the reconstructed HEAD.
	require.NoError(t, os.WriteFile(filepath.Join(linked, "base.txt"), []byte("dirty"), 0o644))
	git("-C", linked, "add", "base.txt")
	c := connect(ctx, t, dagger.WithWorkdir(linked))
	live := c.CurrentWorkspace()
	frozen := snapshotWorkspace(ctx, t, c, live)
	for _, tc := range []struct {
		name string
		ws   *dagger.Workspace
	}{{"live", live}, {"snapshot", frozen}} {
		t.Logf("checking %s workspace", tc.name)
		ctr := workspaceGitDirectoryContainer(c, tc.ws)
		out, err := ctr.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, head, strings.TrimSpace(out))
		out, err = ctr.WithExec([]string{"git", "rev-list", "--count", "HEAD"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "2\n", out)
		out, err = ctr.WithExec([]string{"git", "status", "--porcelain"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, " M base.txt\n", out)
		// This is the nested CLI flow needed by tui-qa: capture a real Git
		// workspace, with no pointer back to the original host checkout.
		out, err = ctr.WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			With(daggerQuery(`{ currentWorkspace { snapshot { git { head { commit } } } } }`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"snapshot":{"git":{"head":{"commit":"`+head+`"}}}}}`, out)
	}
	// The metadata remains bound to the captured HEAD when the source advances.
	git("-C", linked, "commit", "-m", "later")
	out, err := workspaceGitDirectoryContainer(c, frozen).WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, head, strings.TrimSpace(out))
	require.NotEqual(t, head, git("-C", linked, "rev-parse", "HEAD"))
}
