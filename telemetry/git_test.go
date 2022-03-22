package telemetry

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseGit(t *testing.T) {
	var (
		endpoint string
		err      error
	)

	endpoint, err = parseGitURL("https://github.com/dagger/dagger")
	require.NoError(t, err)
	require.Equal(t, endpoint, "github.com/dagger/dagger")

	endpoint, err = parseGitURL("git@github.com:dagger/dagger.git")
	require.NoError(t, err)
	require.Equal(t, endpoint, "github.com/dagger/dagger")
}
