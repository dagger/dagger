package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitRemoteHeadURL(t *testing.T) {
	for remote, want := range map[string]string{
		"https://github.com/dagger/dagger":        "https://github.com/dagger/dagger",
		"https://user@example.com/repo.git":       "https://example.com/repo.git",
		"http://example.com/repo":                 "http://example.com/repo",
		"git://example.com/repo.git":              "git://example.com/repo.git",
		"git@github.com:dagger/dagger.git":        "https://github.com/dagger/dagger",
		"ssh://git@example.com:2222/org/repo.git": "https://example.com/org/repo",
	} {
		url, err := gitRemoteHeadURL(remote)
		require.NoError(t, err, remote)
		require.Equal(t, want, url.Remote(), remote)
	}
	for _, remote := range []string{"entire://example.com/org/repo", "not a url"} {
		_, err := gitRemoteHeadURL(remote)
		require.ErrorIs(t, err, ErrGitRemoteUnreachable, remote)
	}
}

func TestParseGitRemoteHead(t *testing.T) {
	require.Equal(t, "refs/heads/main", parseGitRemoteHead("ref: refs/heads/main\tHEAD\n1111111111111111111111111111111111111111\tHEAD\n"))
	require.Empty(t, parseGitRemoteHead("1111111111111111111111111111111111111111\tHEAD\n"), "no symref advertised")
	require.Empty(t, parseGitRemoteHead(""), "an empty repository")
	require.Empty(t, parseGitRemoteHead("ref: refs/tags/v1\tHEAD\n"), "not a branch")
	require.Empty(t, parseGitRemoteHead("ref: refs/heads/main\trefs/remotes/origin/HEAD\n"))
}
