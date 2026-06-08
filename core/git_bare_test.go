package core

import (
	"testing"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func TestBareOriginRemoteRemovesHTTPUserinfo(t *testing.T) {
	gitURL, err := gitutil.ParseURL("https://user:secret@example.com/acme/repo.git#main")
	require.NoError(t, err)

	require.Equal(t, "https://example.com/acme/repo.git", bareOriginRemote(gitURL))
}

func TestBareOriginRemoteKeepsSSHUser(t *testing.T) {
	gitURL, err := gitutil.ParseURL("git@example.com:acme/repo.git#main")
	require.NoError(t, err)

	require.Equal(t, "git@example.com:acme/repo.git", bareOriginRemote(gitURL))
}

func TestBareOriginRemoteRemovesSSHPassword(t *testing.T) {
	gitURL, err := gitutil.ParseURL("ssh://git:secret@example.com/acme/repo.git#main")
	require.NoError(t, err)

	require.Equal(t, "ssh://git@example.com/acme/repo.git", bareOriginRemote(gitURL))
}
