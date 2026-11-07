package core

import (
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/core/provenance"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceSelectorOutputsRootCoordinates(t *testing.T) {
	p := newWorkspaceProvenance([]provenance.Selector{{Path: "docs/../sdk/go", Kind: provenance.WorkspaceRead}})
	require.Equal(t, WorkspacePath("/sdk/go"), p.Selectors[0].Path)
	payload, err := json.Marshal(p)
	require.NoError(t, err)
	var restored WorkspaceProvenance
	require.NoError(t, json.Unmarshal(payload, &restored))
	match, err := restored.AffectedByPath("/sdk/go/pkg", nil)
	require.NoError(t, err)
	require.True(t, match)
	match, err = restored.AffectedByPath("/sdk/python", nil)
	require.NoError(t, err)
	require.False(t, match)
}
