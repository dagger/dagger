package gitutil

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTranslateErrorSHAFetch(t *testing.T) {
	for _, tc := range []struct {
		stderr string
		want   error
	}{
		// a protocol v0 server that serves no unadvertised object, refused by the client
		{"error: Server does not allow request for unadvertised object 0123456789", ErrSHAFetchUnsupported},
		{"fatal: couldn't find remote ref 0123456789", ErrSHAFetchUnsupported},
		// the server's reply for a missing commit, as GitHub, GitLab and git itself send it
		{"fatal: remote error: upload-pack: not our ref 0123456789", ErrCommitNotFound},
		{"fatal: git upload-pack: not our ref 0123456789", ErrCommitNotFound},
		{"fatal: expected 'packfile', received 'ERR upload-pack: not our ref 0123456789'", ErrCommitNotFound},
	} {
		t.Run(tc.stderr, func(t *testing.T) {
			require.ErrorIs(t, translateError(errors.New("exit status 128"), tc.stderr), tc.want)
		})
	}
}

func TestTranslateErrorPriority(t *testing.T) {
	err := translateError(errors.New("exit status 128"), "fatal: authentication failed and not our ref")
	require.ErrorIs(t, err, ErrGitAuthFailed, "auth classification should take precedence")
}

func TestTranslateErrorContextPassthrough(t *testing.T) {
	err := translateError(context.Canceled, "")
	require.ErrorIs(t, err, context.Canceled)
}

func TestTranslateErrorRemoteAccess(t *testing.T) {
	for _, tc := range []struct {
		stderr string
		want   error
	}{
		{"git@github.com: Permission denied (publickey).", ErrGitAuthFailed},
		{"fatal: Could not resolve host: git.example.test", ErrGitHostNotFound},
		{"ssh: Could not resolve hostname git.example.test", ErrGitHostNotFound},
		{"fatal: Failed to connect to localhost port 1: Could not connect to server", ErrGitConnectionFailed},
		{"ssh: connect to host localhost port 1: Connection refused", ErrGitConnectionFailed},
		{"ssh: connect to host localhost port 22: Connection timed out", ErrGitConnectionFailed},
	} {
		require.ErrorIs(t, translateError(errors.New("exit status 128"), tc.stderr), tc.want)
	}
}
