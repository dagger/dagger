package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
