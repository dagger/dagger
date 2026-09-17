package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func TestGitCheckoutIndependentAfterFetch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		refName string
		depth   int
		discard bool
	}{
		{name: "branch full history", refName: "refs/heads/main"},
		{name: "detached shallow", depth: 1},
		{name: "discard git dir", depth: 1, discard: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			source := t.TempDir()
			git := gitutil.NewGitCLI(gitutil.WithDir(source), gitutil.WithConfig(map[string]string{
				"user.name": "Test User", "user.email": "test@example.com",
			}))
			_, err := git.Run(ctx, "init", "-b", "main")
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(source, "file.txt"), []byte("content"), 0600))
			_, err = git.Run(ctx, "add", ".")
			require.NoError(t, err)
			_, err = git.Run(ctx, "commit", "-m", "initial")
			require.NoError(t, err)
			out, err := git.Run(ctx, "rev-parse", "HEAD")
			require.NoError(t, err)
			ref := &gitutil.Ref{SHA: strings.TrimSpace(string(out)), Name: tc.refName}
			checkout := t.TempDir()
			checkoutGit := gitutil.NewGitCLI(gitutil.WithWorkTree(checkout), gitutil.WithGitDir(filepath.Join(checkout, ".git")))
			tmpref, err := fetchGitCheckout(ctx, checkoutGit, "file://"+source, ref, tc.depth)
			require.NoError(t, err)

			// No alternates, borrowed paths or further reads may depend on the
			// mirror once its locks and mount have been released.
			require.NoError(t, os.RemoveAll(source))
			require.NoError(t, finishGitCheckout(ctx, checkoutGit, "https://example.com/repo", source, ref, tmpref, tc.discard))
			contents, err := os.ReadFile(filepath.Join(checkout, "file.txt"))
			require.NoError(t, err)
			require.Equal(t, "content", string(contents))
			if tc.discard {
				require.NoDirExists(t, filepath.Join(checkout, ".git"))
			} else {
				_, err := checkoutGit.Run(ctx, "fsck", "--full")
				require.NoError(t, err)
				out, err := checkoutGit.Run(ctx, "remote", "get-url", "origin")
				require.NoError(t, err)
				require.Equal(t, "https://example.com/repo\n", string(out))
			}
		})
	}
}

func TestParseGitCommitMetadata(t *testing.T) {
	raw := `tree 5209ad308282b6d6c7d6e4888cd807e29079248b
parent 85896b2097b208dfb0ed2abc937612a7f3bd64b7
parent d8fd70f701d906d86ef0ad0504d3869b42114124
author Andrea Luzzardi <aluzzardi@gmail.com> 1667499276 -0700
committer GitHub <noreply@github.com> 1667499276 -0700

Merge pull request #3661 from aluzzardi/docs-go-sdk-remove-mkdir

docs: go: remove unnecessary MkdirAll from snippets
`

	meta, err := parseGitCommitMetadata("c80ac2c13df7d573a069938e01ca13f7a81f0345", raw)
	require.NoError(t, err)
	require.Equal(t, "c80ac2c13df7d573a069938e01ca13f7a81f0345", meta.SHA)
	require.Equal(t, "c80ac2c", meta.ShortSHA)
	require.Equal(t, "Andrea Luzzardi", meta.AuthorName)
	require.Equal(t, "aluzzardi@gmail.com", meta.AuthorEmail)
	require.Equal(t, "GitHub", meta.CommitterName)
	require.Equal(t, "noreply@github.com", meta.CommitterEmail)
	require.Equal(t, "2022-11-03T11:14:36-07:00", meta.AuthoredDate)
	require.Equal(t, "2022-11-03T11:14:36-07:00", meta.CommittedDate)
	require.Equal(t, []string{
		"85896b2097b208dfb0ed2abc937612a7f3bd64b7",
		"d8fd70f701d906d86ef0ad0504d3869b42114124",
	}, meta.ParentSHAs)
	require.Equal(t, "Merge pull request #3661 from aluzzardi/docs-go-sdk-remove-mkdir\n\ndocs: go: remove unnecessary MkdirAll from snippets", meta.Message)
}

func TestParseGitCommitMetadataLenientTimezone(t *testing.T) {
	// git accepts timezone offsets stricter parsers reject (+051800 is real,
	// common in history imported from CVS/SVN); one such commit must not fail
	// metadata parsing, it just renders in UTC
	raw := `tree 5209ad308282b6d6c7d6e4888cd807e29079248b
author Imported History <import@example.com> 1667499276 +051800
committer Imported History <import@example.com> 1667499276 junk

imported commit
`

	meta, err := parseGitCommitMetadata("c80ac2c13df7d573a069938e01ca13f7a81f0345", raw)
	require.NoError(t, err)
	require.Equal(t, "2022-11-03T18:14:36Z", meta.AuthoredDate)
	require.Equal(t, "2022-11-03T18:14:36Z", meta.CommittedDate)
}

func TestParseGitTimezoneOffset(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		offset int
		ok     bool
	}{
		{"+0000", 0, true},
		{"-0700", -7 * 60 * 60, true},
		{"+0530", 5*60*60 + 30*60, true},
		{"+2359", 23*60*60 + 59*60, true},
		{"-2359", -(23*60*60 + 59*60), true},
		// git reads the digits as hours*100+minutes, whatever the width
		{"+0575", 5*60*60 + 75*60, true},
		// valid for git, but unrepresentable in RFC3339
		{"+051800", 0, false},
		{"+2400", 0, false},
		{"0500", 0, false},
		{"+05x0", 0, false},
		{"+-500", 0, false},
		{"+", 0, false},
		{"", 0, false},
	} {
		offset, ok := parseGitTimezoneOffset(tc.raw)
		require.Equal(t, tc.ok, ok, "offset %q", tc.raw)
		require.Equal(t, tc.offset, offset, "offset %q", tc.raw)
	}
}
