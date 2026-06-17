package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspacePersistedObjectCarriesIgnorePatterns(t *testing.T) {
	ctx := context.Background()
	ws := &Workspace{
		Address:    "file:///repo",
		Cwd:        ".",
		ConfigFile: "dagger.toml",
	}
	ws.SetIgnorePatterns([]string{"ignored/**", "!ignored/keep"})

	encoded, err := ws.EncodePersistedObject(ctx, nil)
	require.NoError(t, err)

	decoded, err := (&Workspace{}).DecodePersistedObject(ctx, nil, 0, nil, encoded.JSON)
	require.NoError(t, err)

	decodedWorkspace, ok := decoded.(*Workspace)
	require.True(t, ok)
	require.Equal(t, []string{"ignored/**", "!ignored/keep"}, decodedWorkspace.IgnorePatterns())
}
