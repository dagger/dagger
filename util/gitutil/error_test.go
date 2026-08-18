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

func TestAnnotateWithStderr(t *testing.T) {
	err := annotateWithStderr(errors.New("exit status 128"), "remote: Counting objects\nfatal: shallow file has changed since we read it\n")
	require.ErrorContains(t, err, "exit status 128")
	require.ErrorContains(t, err, "shallow file has changed since we read it")

	// already-classified errors don't get their own text repeated
	sentinel := annotateWithStderr(ErrGitAuthFailed, "fatal: Authentication failed")
	require.ErrorIs(t, sentinel, ErrGitAuthFailed)
}

func TestAnnotateWithStderrRedactsCredentials(t *testing.T) {
	cases := []string{
		"fatal: Authentication failed for 'https://user:hunter2@github.com/org/repo.git/'",
		"remote: fatal: unable to access 'https://x-access-token:ghp_hunter2@github.com/org/repo/': 403",
		"fatal: could not read Password for 'https://hunter2@gitlab.com'",
	}

	for _, stderr := range cases {
		t.Run(stderr, func(t *testing.T) {
			err := annotateWithStderr(errors.New("exit status 128"), stderr)
			require.NotContains(t, err.Error(), "hunter2")
			require.Contains(t, err.Error(), "xxxxx@")
		})
	}
}
