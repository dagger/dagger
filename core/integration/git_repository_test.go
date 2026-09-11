package core

// These tests cover repository conversion, storage replacement, and preservation
// of pending edits when creating a workspace.

import (
	"context"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (GitSuite) TestGitRefAsRepository(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().WithNewFile("base.txt", "base"))
	original := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon})
	first := original.Head()
	checkout := first.Tree()
	ctr := gitCheckoutContainer(c, checkout).
		WithExec([]string{"git", "checkout", "-b", "feature"}).
		WithExec([]string{"sh", "-ec", "echo feature > feature.txt; git add .; git commit -m feature"})
	repo := original.WithDirectory(ctr.Directory("/src"))
	// Pin HEAD to the base commit while keeping the feature branch available.
	firstSHA, err := first.CommitSHA(ctx)
	require.NoError(t, err)
	pinned := repo.Ref(firstSHA).AsRepository()
	head, err := pinned.Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, firstSHA, head)
	feature, err := pinned.Branch("feature").Tree().File("feature.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "feature\n", feature)
	unchanged, err := repo.Head().Tree().File("feature.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, feature, unchanged)
	routing, err := pinned.URL(ctx)
	require.NoError(t, err)
	require.Equal(t, url, routing)
	// A remote-backed conversion must retain its service binding.
	remoteHead, err := first.AsRepository().Head().Tree().File("base.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "base", remoteHead)
}

func gitCheckoutContainer(c *dagger.Client, dir *dagger.Directory) *dagger.Container {
	return c.Container().From(alpineImage).WithExec([]string{"apk", "add", "git"}).
		WithDirectory("/src", dir).WithWorkdir("/src").
		WithExec([]string{"git", "config", "user.name", "Test"}).
		WithExec([]string{"git", "config", "user.email", "test@example.com"})
}

func (GitSuite) TestGitRepositoryWithDirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().
		WithNewFile("sub/staged.txt", "old staged").WithNewFile("sub/unstaged.txt", "old unstaged").WithNewFile("removed.txt", "removed"))
	original := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon})
	ctr := gitCheckoutContainer(c, original.Head().Tree()).
		WithExec([]string{"sh", "-ec", "echo staged > sub/staged.txt; git add sub/staged.txt; echo unstaged > sub/unstaged.txt; rm removed.txt; echo added > added.txt; git remote set-url origin https://different.invalid/repo"})
	repo := original.WithDirectory(ctr.Directory("/src"))
	ws := repo.AsWorkspace(dagger.GitRepositoryAsWorkspaceOpts{Cwd: "sub"})
	for file, want := range map[string]string{"staged.txt": "staged\n", "unstaged.txt": "unstaged\n", "/added.txt": "added\n"} {
		got, err := ws.File(file).Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	entries, err := ws.Directory("/").Entries(ctx)
	require.NoError(t, err)
	require.NotContains(t, entries, "removed.txt")
	require.NotContains(t, entries, ".git/")
	clean, err := repo.Head().AsWorkspace().File("sub/staged.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "old staged", clean)
	routing, err := repo.URL(ctx)
	require.NoError(t, err)
	require.Equal(t, url, routing)
	config, err := repo.Uncommitted().After().File(".git/config").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, config, "different.invalid")
	for _, tc := range []struct {
		name      string
		directory *dagger.Directory
	}{
		{"git metadata", ctr.Directory("/src/.git")},
		{"bare repository", ctr.WithExec([]string{"git", "clone", "--bare", ".", "/bare.git"}).Directory("/bare.git")},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			clean := original.WithDirectory(tc.directory).AsWorkspace()
			got, err := clean.File("sub/staged.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "old staged", got)
			empty, err := clean.Git().Uncommitted().IsEmpty(ctx)
			require.NoError(t, err)
			require.True(t, empty)
		})
	}
	originalFile, err := original.AsWorkspace().File("sub/staged.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "old staged", originalFile)
	_, err = original.WithDirectory(c.Directory().WithNewFile("plain.txt", "not git")).Head().CommitSHA(ctx)
	require.Error(t, err)
}

func (GitSuite) TestGitRepositoryWithDirectoryRejectsExternalStorage(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().WithNewFile("file.txt", "base"))
	repo := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon})
	source := gitCheckoutContainer(c, repo.Head().Tree()).
		WithExec([]string{"git", "worktree", "add", "-b", "linked", "/linked"})
	_, err := repo.WithDirectory(source.Directory("/linked")).Head().CommitSHA(ctx)
	require.Error(t, err, "a worktree pointer cannot reference the original container")
	// The selected Directory is a subdirectory of a larger snapshot: an external
	// dependency may still exist physically, but must not be accepted.
	source = source.WithExec([]string{"sh", "-ec", "mkdir -p /src/external; cp -a .git /src/external/repo.git; printf '../../external/repo.git/objects\\n' > .git/objects/info/alternates"})
	_, err = repo.WithDirectory(source.Directory("/src/.git")).Head().CommitSHA(ctx)
	require.ErrorContains(t, err, "self-contained")
}
