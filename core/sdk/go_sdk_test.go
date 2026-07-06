package sdk

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitCredentialHostsFromGoPrivate(t *testing.T) {
	t.Parallel()

	require.Equal(t,
		[]string{"github.com", "gitlab.example.com"},
		gitCredentialHostsFromGoPrivate("github.com/myorg/*, gitlab.example.com/repo.git, *.corp.example.com, github.com"),
	)
	require.Empty(t, gitCredentialHostsFromGoPrivate(""))
}
