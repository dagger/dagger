package core

import (
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

// The Artifacts visitor reports, and rewrites, every row a stored artifact
// references, in entries and collection roots alike.
func TestPersistedArtifactsVisitorWalksEveryReference(t *testing.T) {
	artifact := func(base uint64) persistedArtifact {
		return persistedArtifact{
			Workspace:              base + 1,
			ContextWorkspace:       base + 2,
			DimensionDisplaySource: base + 3,
			View:                   &persistedArtifactView{Sources: []uint64{base + 4, base + 5}},
		}
	}
	raw, err := json.Marshal(persistedArtifacts{
		Version:         artifactSnapshotVersion,
		Entries:         []persistedArtifact{artifact(10)},
		CollectionRoots: []persistedArtifact{artifact(20)},
	})
	require.NoError(t, err)

	refs := map[string]uint64{}
	rewritten, err := persistedArtifactsVisitor.VisitPersistedReferences(dagql.PersistedPayloadVisit{
		Path:    dagql.PersistedRefPath{}.Field("objectJSON"),
		Payload: raw,
	}, func(ref *dagql.PersistedRef) error {
		require.Equal(t, dagql.PersistedRefChild, ref.Kind)
		refs[ref.Path.String()] = ref.ResultID
		ref.ResultID += 100
		return nil
	})
	require.NoError(t, err)
	want := map[string]uint64{}
	for field, base := range map[string]uint64{"Entries": 10, "CollectionRoots": 20} {
		at := dagql.PersistedRefPath{}.Field("objectJSON").Field(field).Index(0)
		want[at.Field("Workspace").String()] = base + 1
		want[at.Field("ContextWorkspace").String()] = base + 2
		want[at.Field("DimensionDisplaySource").String()] = base + 3
		want[at.Field("View").Field("Sources").Index(0).String()] = base + 4
		want[at.Field("View").Field("Sources").Index(1).String()] = base + 5
	}
	require.Equal(t, want, refs)

	var payload persistedArtifacts
	require.NoError(t, json.Unmarshal(rewritten, &payload))
	require.Equal(t, artifact(110), payload.Entries[0], "a relocated row is written back")
	require.Equal(t, artifact(120), payload.CollectionRoots[0])
}
