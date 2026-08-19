package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceCheckpointMetadataPersists(t *testing.T) {
	t.Parallel()

	ws := &Workspace{}
	ws.SetSource(NewWorkspaceSourceDirectory(dagql.ObjectResult[*Directory]{}))
	ws.SetPortableCheckpoint()
	ws.SetWorkspaceEnv("dev")
	ws.SetGitOrigin("git@github.com:acme/repo.git")

	clone := ws.Clone()
	require.True(t, clone.IsPortableCheckpoint())
	require.Equal(t, "dev", clone.WorkspaceEnv())
	require.Equal(t, "github.com/acme/repo", clone.GitOrigin())

	encoded, err := clone.EncodePersistedObject(context.Background(), nil)
	require.NoError(t, err)
	require.Contains(t, string(encoded.JSON), `"portableCheckpoint":true`)
	require.Contains(t, string(encoded.JSON), `"workspaceEnv":"dev"`)
	require.Contains(t, string(encoded.JSON), `"gitOrigin":"github.com/acme/repo"`)

	decoded, err := (&Workspace{}).DecodePersistedObject(context.Background(), nil, 0, nil, encoded.JSON)
	require.NoError(t, err)
	workspace := decoded.(*Workspace)
	require.True(t, workspace.IsPortableCheckpoint())
	require.Equal(t, "dev", workspace.WorkspaceEnv())
	require.Equal(t, "github.com/acme/repo", workspace.GitOrigin())
}
