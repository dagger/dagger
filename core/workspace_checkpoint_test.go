package core

import (
	"context"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceCheckpointMetadataPersists(t *testing.T) {
	t.Parallel()

	ws := &Workspace{}
	ws.SetSource(NewWorkspaceSourceDirectory(dagql.ObjectResult[*Directory]{}))
	ws.SetWorkspaceEnv("dev")
	ws.SetGitOrigin("git@github.com:acme/repo.git")

	clone := ws.Clone()
	require.Equal(t, "dev", clone.WorkspaceEnv())
	require.Equal(t, "github.com/acme/repo", clone.GitOrigin())

	encoded, err := clone.EncodePersistedObject(context.Background(), nil)
	require.NoError(t, err)
	require.NotContains(t, string(encoded.JSON), `"portableCheckpoint"`)
	require.Contains(t, string(encoded.JSON), `"workspaceEnv":"dev"`)
	require.Contains(t, string(encoded.JSON), `"gitOrigin":"github.com/acme/repo"`)

	// Older saved sessions may still carry the retired classification. Unknown
	// payload fields are ignored so those sessions remain decodable.
	legacyJSON := []byte(strings.Replace(string(encoded.JSON), "{", `{"portableCheckpoint":true,`, 1))
	decoded, err := (&Workspace{}).DecodePersistedObject(context.Background(), nil, 0, nil, legacyJSON)
	require.NoError(t, err)
	workspace := decoded.(*Workspace)
	require.Equal(t, "dev", workspace.WorkspaceEnv())
	require.Equal(t, "github.com/acme/repo", workspace.GitOrigin())
}

// TestWorkspaceExportTargetValidation asserts that only a client-owned local
// Git workspace can supply an export route. Frozen values carry no implicit
// destination, including in the session that captured them.
func TestWorkspaceExportTargetValidation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	value := &Workspace{}
	value.SetSource(NewWorkspaceSourceDirectory(dagql.ObjectResult[*Directory]{}))
	_, _, err := value.ExportTarget(ctx)
	require.ErrorContains(t, err, "cannot export a synthetic workspace")

	remote := &Workspace{}
	remote.SetSource(NewWorkspaceSourceGitRef(dagql.Result[*GitRef]{}, false))
	_, _, err = remote.ExportTarget(ctx)
	require.ErrorContains(t, err, "cannot export a remote Git workspace")

	local := &Workspace{ClientID: "local-client"}
	local.SetSource(NewWorkspaceSourceClientLocal("/work"))
	clientID, hostPath, err := local.ExportTarget(ctx)
	require.NoError(t, err)
	require.Equal(t, "local-client", clientID)
	require.Equal(t, "/work", hostPath)

	unowned := &Workspace{}
	unowned.SetSource(NewWorkspaceSourceClientLocal("/work"))
	_, _, err = unowned.ExportTarget(ctx)
	require.ErrorContains(t, err, "must be a client-local Git workspace")
}
