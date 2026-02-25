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
