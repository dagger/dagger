package core

import (
	"context"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (GitSuite) TestGitRefAsRepository(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().WithNewFile("base.txt", "base"))
	original := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon})
	first := original.Head()
	checkout := first.Tree(dagger.GitRefTreeOpts{Depth: 0})
	ctr := gitRefactorContainer(c, checkout).
		WithExec([]string{"git", "checkout", "-b", "feature"}).
		WithExec([]string{"sh", "-c", "echo feature > feature.txt; git add .; git commit -m feature"})
	repo := original.WithDirectory(ctr.Directory("/src"))
	// Resolve a full SHA: GitRepository.ref accepts named refs or commit IDs,
	// rather than arbitrary revision expressions.
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

func gitRefactorContainer(c *dagger.Client, dir *dagger.Directory) *dagger.Container {
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
	ctr := gitRefactorContainer(c, original.Head().Tree(dagger.GitRefTreeOpts{Depth: 0})).
		WithExec([]string{"sh", "-c", "echo staged > sub/staged.txt; git add sub/staged.txt; echo unstaged > sub/unstaged.txt; rm removed.txt; echo added > added.txt; git remote set-url origin https://different.invalid/repo"})
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
	clean, err := repo.Head().AsWorkspace().File("sub/staged.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "old staged", clean)
	routing, err := repo.URL(ctx)
	require.NoError(t, err)
	require.Equal(t, url, routing)
	config, err := ctr.WithExec([]string{"git", "remote", "get-url", "origin"}).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, config, "different.invalid")
	for _, dir := range []*dagger.Directory{ctr.Directory("/src/.git"), ctr.WithExec([]string{"git", "clone", "--bare", ".", "/bare.git"}).Directory("/bare.git")} {
		clean := original.WithDirectory(dir).AsWorkspace()
		got, err := clean.File("sub/staged.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "old staged", got)
		empty, err := clean.Git().Uncommitted().IsEmpty(ctx)
		require.NoError(t, err)
		require.True(t, empty)
	}
	originalFile, err := original.AsWorkspace().File("sub/staged.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "old staged", originalFile)
	_, err = original.WithDirectory(c.Directory().WithNewFile("plain.txt", "not git")).Head().CommitSHA(ctx)
	require.Error(t, err)
}

func (GitSuite) TestGitRefWithCommit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	baseText := "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n"
	daemon, url := gitService(ctx, t, c, c.Directory().WithNewFile("file.txt", baseText).WithNewFile("delete.txt", "delete"))
	base := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Head()
	before := base.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
	commit := func(ref *dagger.GitRef, changes *dagger.Changeset, opts ...dagger.GitRefWithCommitOpts) *dagger.GitRef {
		return ref.WithCommit(changes, "edit", workspaceCommitDate, "Author", "author@example.com", opts...)
	}
	ours := commit(base, before.WithNewFile("file.txt", strings.Replace(baseText, "one", "OURS", 1)).WithNewFile("ours.txt", "keep").Changes(before))
	theirsText := strings.Replace(baseText, "ten", "THEIRS", 1)
	changes := before.WithNewFile("file.txt", theirsText).WithoutFile("delete.txt").WithNewFile("added.txt", "new").Changes(before)
	result := commit(ours, changes, dagger.GitRefWithCommitOpts{CommitterName: "Committer", CommitterEmail: "committer@example.com", CommitterDate: "2026-09-06T12:00:00Z"})
	got, err := result.Tree().File("file.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, strings.Replace(theirsText, "one", "OURS", 1), got)
	keep, err := result.Tree().File("ours.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "keep", keep)
	entries, err := result.Tree().Entries(ctx)
	require.NoError(t, err)
	require.NotContains(t, entries, "delete.txt")
	require.Contains(t, entries, "added.txt")
	log, err := result.Log(ctx)
	require.NoError(t, err)
	require.Len(t, log, 3)
	// Committed metadata and parentage come from explicit inputs.
	meta, err := workspaceGitDirectoryContainer(c, result.AsWorkspace()).WithExec([]string{"git", "show", "-s", "--format=%an|%ae|%cn|%ce|%aI|%cI|%P", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	parentSHA, err := ours.CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, "Author|author@example.com|Committer|committer@example.com|2026-09-05T12:00:00Z|2026-09-06T12:00:00Z|"+parentSHA+"\n", meta)
	_, err = commit(ours, before.WithNewFile("file.txt", strings.Replace(baseText, "one", "CONFLICT", 1)).Changes(before)).CommitSHA(ctx)
	require.ErrorContains(t, err, "CONFLICT")
	// The requested edit is already present, despite a nonempty changeset.
	already := before.WithNewFile("file.txt", strings.Replace(baseText, "one", "OURS", 1)).Changes(before)
	_, err = commit(ours, already).CommitSHA(ctx)
	require.ErrorContains(t, err, "nothing to commit")
	emptySHA, err := commit(ours, already, dagger.GitRefWithCommitOpts{AllowEmpty: true}).CommitSHA(ctx)
	require.NoError(t, err)
	require.NotEqual(t, parentSHA, emptySHA)
	original, err := base.Tree().File("file.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, baseText, original)
}

func (ChangesetSuite) TestFilter(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	before := c.Directory().WithNewFile("src/edit.txt", "old").WithNewFile("src/delete.txt", "delete").WithNewFile("docs/readme.txt", "docs")
	all := before.WithNewFile("src/edit.txt", "new").WithoutFile("src/delete.txt").WithNewFile("src/add.txt", "added").WithNewFile("docs/readme.txt", "new docs").Changes(before)
	selected := all.Filter(dagger.ChangesetFilterOpts{Include: []string{"src/**"}, Exclude: []string{"src/add.txt"}})
	added, err := selected.AddedPaths(ctx)
	require.NoError(t, err)
	require.Empty(t, added)
	removed, err := selected.RemovedPaths(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"src/delete.txt"}, removed)
	modified, err := selected.ModifiedPaths(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"src/edit.txt"}, modified)
	// Filtering retains unrelated baseline content, not just matching files.
	baseline, err := selected.Before().File("docs/readme.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "docs", baseline)
	after, err := selected.After().File("docs/readme.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "docs", after)
}

func (GitSuite) TestGitRepositoryWithDirectoryRejectsExternalStorage(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().WithNewFile("file.txt", "base"))
	repo := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon})
	source := gitRefactorContainer(c, repo.Head().Tree()).
		WithExec([]string{"git", "worktree", "add", "-b", "linked", "/linked"})
	_, err := repo.WithDirectory(source.Directory("/linked")).Head().CommitSHA(ctx)
	require.Error(t, err, "a worktree pointer cannot reference the original container")
	// The selected Directory is a subdirectory of a larger snapshot: an external
	// dependency may still exist physically, but must not be accepted.
	source = source.WithExec([]string{"sh", "-ec", "mkdir -p /src/external; cp -a .git /src/external/repo.git; printf '../../external/repo.git/objects\\n' > .git/objects/info/alternates"})
	_, err = repo.WithDirectory(source.Directory("/src/.git")).Head().CommitSHA(ctx)
	require.Error(t, err)
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
