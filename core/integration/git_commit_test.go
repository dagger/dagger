package core

// These tests create commits through the generated Go SDK and inspect their
// trees, metadata, and history through the public Git APIs.

import (
	"context"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (GitSuite) TestGitRefWithCommit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	const date = "2026-09-05T12:00:00Z"
	const baseText = "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n"
	daemon, url := gitService(ctx, t, c, c.Directory().WithNewFile("file.txt", baseText).WithNewFile("delete.txt", "delete"))
	base := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Head()
	before := base.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
	commit := func(ref *dagger.GitRef, changes *dagger.Changeset, opts ...dagger.GitRefWithCommitOpts) *dagger.GitRef {
		return ref.WithCommit(changes, "edit", date, "Author", "author@example.com", opts...)
	}
	oursText := strings.Replace(baseText, "one", "OURS", 1)
	ours := commit(base, before.WithNewFile("file.txt", oursText).WithNewFile("ours.txt", "keep").Changes(before))
	theirsText := strings.Replace(baseText, "ten", "THEIRS", 1)
	changes := before.WithNewFile("file.txt", theirsText).WithoutFile("delete.txt").WithNewFile("added.txt", "new").Changes(before)
	result := commit(ours, changes, dagger.GitRefWithCommitOpts{
		CommitterName: "Committer", CommitterEmail: "committer@example.com", CommitterDate: "2026-09-06T12:00:00Z",
	})

	t.Run("merges independent edits and preserves inputs", func(ctx context.Context, t *testctx.T) {
		for file, want := range map[string]string{
			"file.txt": strings.Replace(theirsText, "one", "OURS", 1),
			"ours.txt": "keep", "added.txt": "new",
		} {
			got, err := result.Tree().File(file).Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, want, got, file)
		}
		entries, err := result.Tree().Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, entries, "delete.txt")
		log, err := result.Log(ctx)
		require.NoError(t, err)
		require.Len(t, log, 3)
		original, err := base.Tree().File("file.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, baseText, original)
		parent, err := ours.Tree().File("file.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, oursText, parent)
	})

	t.Run("records explicit identity dates and a single parent", func(ctx context.Context, t *testctx.T) {
		meta := result.TargetCommit()
		for _, field := range []struct {
			name string
			read func(context.Context) (string, error)
			want string
		}{
			{"author name", meta.AuthorName, "Author"},
			{"author email", meta.AuthorEmail, "author@example.com"},
			{"committer name", meta.CommitterName, "Committer"},
			{"committer email", meta.CommitterEmail, "committer@example.com"},
			{"author date", meta.AuthoredDate, date},
			{"committer date", meta.CommittedDate, "2026-09-06T12:00:00Z"},
		} {
			got, err := field.read(ctx)
			require.NoError(t, err, field.name)
			require.Equal(t, field.want, got, field.name)
		}
		parentSHA, err := ours.CommitSHA(ctx)
		require.NoError(t, err)
		parents, err := meta.ParentShas(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{parentSHA}, parents)
	})

	t.Run("does not sign off by default", func(ctx context.Context, t *testctx.T) {
		message, err := result.TargetCommit().Message(ctx)
		require.NoError(t, err)
		require.Equal(t, "edit", strings.TrimSpace(message))
	})

	t.Run("signs off as author not committer", func(ctx context.Context, t *testctx.T) {
		signed := commit(ours, changes, dagger.GitRefWithCommitOpts{
			Signoff: true, CommitterName: "Committer", CommitterEmail: "committer@example.com",
		})
		message, err := signed.TargetCommit().Message(ctx)
		require.NoError(t, err)
		require.Equal(t, "edit\n\nSigned-off-by: Author <author@example.com>", strings.TrimSpace(message))
		committer, err := signed.TargetCommit().CommitterName(ctx)
		require.NoError(t, err)
		require.Equal(t, "Committer", committer)
	})

	t.Run("rejects conflicting edits", func(ctx context.Context, t *testctx.T) {
		conflicting := before.WithNewFile("file.txt", strings.Replace(baseText, "one", "CONFLICT", 1)).Changes(before)
		_, err := commit(ours, conflicting).CommitSHA(ctx)
		require.ErrorContains(t, err, "CONFLICT")
	})

	// A nonempty changeset can still produce an empty commit when its edits are
	// already present in the parent tree.
	alreadyApplied := before.WithNewFile("file.txt", oursText).Changes(before)
	t.Run("rejects an unchanged result by default", func(ctx context.Context, t *testctx.T) {
		_, err := commit(ours, alreadyApplied).CommitSHA(ctx)
		require.ErrorContains(t, err, "nothing to commit")
	})
	t.Run("allows an explicitly empty commit", func(ctx context.Context, t *testctx.T) {
		empty := commit(ours, alreadyApplied, dagger.GitRefWithCommitOpts{AllowEmpty: true})
		emptySHA, err := empty.CommitSHA(ctx)
		require.NoError(t, err)
		parentSHA, err := ours.CommitSHA(ctx)
		require.NoError(t, err)
		require.NotEqual(t, parentSHA, emptySHA)
		unchanged, err := empty.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true}).Changes(ours.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})).IsEmpty(ctx)
		require.NoError(t, err)
		require.True(t, unchanged)
	})
}
