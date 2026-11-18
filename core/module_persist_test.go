package core

import (
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

// A persisted module's references are relocated together when its rows are
// renumbered, as a transfer between engines or the merge of the engine image's
// entries renumbers them: its entrypoint's dependencies with the rest.
func TestPersistedModuleRelocatesEntrypointDependencies(t *testing.T) {
	t.Parallel()
	payload, err := json.Marshal(persistedModulePayload{
		SourceResultID:               1,
		DepModuleResultIDs:           []uint64{2},
		HasEntrypointDeps:            true,
		EntrypointDepModuleResultIDs: []uint64{3, 4},
	})
	require.NoError(t, err)
	rewritten, err := persistedModuleVisitor.VisitPersistedReferences(dagql.PersistedPayloadVisit{Payload: payload}, func(ref *dagql.PersistedRef) error {
		ref.ResultID += 100
		return nil
	})
	require.NoError(t, err)
	var got persistedModulePayload
	require.NoError(t, json.Unmarshal(rewritten, &got))
	require.Equal(t, uint64(101), got.SourceResultID)
	require.Equal(t, []uint64{102}, got.DepModuleResultIDs)
	require.True(t, got.HasEntrypointDeps)
	require.Equal(t, []uint64{103, 104}, got.EntrypointDepModuleResultIDs)
}
