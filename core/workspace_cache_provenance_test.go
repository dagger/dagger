package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceCacheProvenanceSurvivesPersistence(t *testing.T) {
	ws := &Workspace{Cwd: "tools/agent"}
	ws.SetGitOrigin("https://username:password@github.com/acme/repo.git")
	require.Equal(t, "github.com/acme/repo", ws.GitOrigin())
	clone := ws.Clone()
	encoded, err := clone.EncodePersistedObject(t.Context(), nil)
	require.NoError(t, err)
	require.NotContains(t, string(encoded.JSON), "password")
	decoded, err := (&Workspace{}).DecodePersistedObject(t.Context(), nil, 0, nil, encoded.JSON)
	require.NoError(t, err)
	require.Equal(t, ws.GitOrigin(), decoded.(*Workspace).GitOrigin())
}
