package gitutil

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTranslateErrorSHAFetchUnsupported(t *testing.T) {
	cases := []string{
		"fatal: remote error: upload-pack: not our ref 0123456789",
		"fatal: Server does not allow request for unadvertised object 0123456789",
		"fatal: couldn't find remote ref 0123456789",
	}

	for _, stderr := range cases {
		stderr := stderr
		t.Run(stderr, func(t *testing.T) {
			err := translateError(errors.New("exit status 128"), stderr)
			require.ErrorIs(t, err, ErrSHAFetchUnsupported)
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
