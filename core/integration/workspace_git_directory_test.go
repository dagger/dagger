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
	assertWorkspaceFullCheckout(ctx, t, c, base, []string{baseSHA})
	out, err = clean.WithExec([]string{"git", "remote", "get-url", "origin"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, url, strings.TrimSpace(out))

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
	assertWorkspaceFullCheckout(ctx, t, c, ws, []string{head, baseSHA})

	ctr := workspaceGitDirectoryContainer(c, ws)
	out, err = ctr.WithExec([]string{"git", "log", "--format=%H"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{head, baseSHA}, strings.Fields(out))
	// Commits rebuild the repository engine-side; the origin remote survives.
	out, err = ctr.WithExec([]string{"git", "remote", "get-url", "origin"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, url, strings.TrimSpace(out))
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

// Exercise the same internal result used by commits and saves. Selecting the
// public metadata view must not change the cached full checkout's root.
func assertWorkspaceFullCheckout(ctx context.Context, t *testctx.T, c *dagger.Client, ws *dagger.Workspace, history []string) {
	t.Helper()
	id, err := ws.ID(ctx)
	require.NoError(t, err)
	var result struct {
		Node struct {
			Git struct {
				Checkout struct{ ID dagger.ID } `json:"__checkout"`
			}
		}
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!) { node(id: $id) { ... on Workspace { git { __checkout { id } } } } }`,
		Variables: map[string]any{"id": id},
	}, &dagger.Response{Data: &result}))
	checkout := dagger.Ref[*dagger.Directory](c, result.Node.Git.Checkout.ID)
	_, err = ws.Git().Directory().Entries(ctx)
	require.NoError(t, err)
	entries, err := checkout.Entries(ctx)
	require.NoError(t, err)
	require.Contains(t, entries, ".git/")
	require.NotContains(t, entries, "HEAD", "checkout is not rooted at its metadata directory")
	ctr := c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithMountedDirectory("/src", checkout).WithWorkdir("/src")
	out, err := ctr.WithExec([]string{"git", "log", "--format=%H"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, history, strings.Fields(out))
	out, err = ctr.WithExec([]string{"git", "rev-parse", "--is-shallow-repository"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "false\n", out)
	out, err = ctr.WithExec([]string{"git", "status", "--porcelain"}).Stdout(ctx)
	require.NoError(t, err)
	require.Empty(t, out, "checkout excludes pending workspace edits")
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
	const originURL = "https://example.com/origin/repo.git"
	git("remote", "add", "origin", originURL)
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
	assertWorkspaceFullCheckout(ctx, t, c, frozen, strings.Fields(git("-C", linked, "log", "--format=%H")))
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
		// The host checkout's origin remote survives the canonical
		// reconstruction, so remote-aware tooling (gh, git fetch) can still
		// resolve the repository from the mounted metadata.
		out, err = ctr.WithExec([]string{"git", "remote", "get-url", "origin"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, originURL, strings.TrimSpace(out))
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

func (WorkspaceSuite) TestWorkspaceGitDirectoryOriginCredentials(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		name   string
		origin string
		keep   bool
	}{
		{"https", "https://example.invalid/repo.git", true},
		{"ssh username", "ssh://git@example.invalid/repo.git", true},
		{"scp username", "git@example.invalid:repo.git", true},
		{"https password", "https://user:FAKE_REVIEW_TOKEN@example.invalid/repo.git", false},
		{"https username token", "https://FAKE_REVIEW_TOKEN@example.invalid/repo.git", false},
		{"ssh password", "ssh://git:FAKE_REVIEW_TOKEN@example.invalid/repo.git", false},
		{"malformed URL", "https://user:FAKE_REVIEW_TOKEN@bad%host/repo.git", false},
		{"query token", "https://example.invalid/repo.git?token=FAKE_REVIEW_TOKEN", false},
		{"fragment token", "https://example.invalid/repo.git#FAKE_REVIEW_TOKEN", false},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			root, git := workspaceExportCheckout(ctx, t)
			git("remote", "add", "origin", tc.origin)
			c := connect(ctx, t, dagger.WithWorkdir(root))
			config, err := c.CurrentWorkspace().Git().Directory().File("config").Contents(ctx)
			require.NoError(t, err)
			require.NotContains(t, config, "FAKE_REVIEW_TOKEN")
			if tc.keep {
				require.Contains(t, config, "url = "+tc.origin)
			} else {
				require.NotContains(t, config, `[remote "origin"]`)
			}
			// Omitting reconstruction metadata never changes the host's routing.
			require.Equal(t, tc.origin, git("config", "--get", "remote.origin.url"))
		})
	}
}
