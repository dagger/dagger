package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestArtifactDimensionDisplay(t *testing.T) {
	coordinates := []*ArtifactDimensionKey{{Dimension: "module", Key: "app"}, {Dimension: "item", Key: "a"}}
	source := &Artifact{DimensionKeys: coordinates}
	selection := &Artifacts{Entries: []*Artifact{source, {DimensionKeys: []*ArtifactDimensionKey{{Dimension: "module", Key: "app"}, {Dimension: "item", Key: "b"}}}}}
	require.Equal(t, []string{"item"}, selection.Dimensions())
	row := source.WithDimensionDisplay(selection.Dimensions())
	require.Equal(t, coordinates[1:], row.DisplayDimensionKeys(false))
	require.Equal(t, coordinates, row.DisplayDimensionKeys(true))
	require.Equal(t, coordinates, source.DisplayDimensionKeys(false))
	require.Equal(t, coordinates, row.DimensionKeys)
	clone := row.Clone()
	clone.dimensionDisplay.Dimensions[0] = "module"
	require.Equal(t, coordinates[1:], row.DisplayDimensionKeys(false))
	returned := row.DisplayDimensionKeys(false)
	returned[0].Key = "changed"
	require.Equal(t, "a", source.DimensionKeys[1].Key)
	one, err := (&Artifacts{Entries: []*Artifact{source}}).One()
	require.NoError(t, err)
	require.Empty(t, one.DisplayDimensionKeys(false))
	require.Equal(t, coordinates, one.DisplayDimensionKeys(true))
	require.Equal(t, coordinates, source.DimensionKeys)
}

func TestArtifactSchemaDimensionsPreserved(t *testing.T) {
	source := &Artifacts{
		Entries:       []*Artifact{{ModuleName: "app", TypeName: "Check", Path: []string{"app", "check"}}},
		AllDimensions: artifact.Dimensions{{Identifier: "module"}, {Identifier: "/app/empty"}, {Identifier: "type:Check"}},
	}
	want := []string{"/app/empty", "module", "type:Check"}
	for _, selection := range []*Artifacts{
		source, source.FilterTypes([]string{}, false), source.FilterPath([]string{"missing"}), source.FilterDimensionKeys("/app/empty", []string{}),
	} {
		require.Equal(t, want, selection.SchemaDimensions())
	}
	fallback := &Artifacts{Entries: source.Entries}
	empty := fallback.FilterTypes([]string{}, false)
	require.Equal(t, []string{"module", "type:Check"}, empty.SchemaDimensions())
	require.Empty(t, empty.Dimensions())
	require.Empty(t, (&Artifacts{}).SchemaDimensions())
}

func TestArtifactDimensionDisplayPersistence(t *testing.T) {
	ctx := context.Background()
	source := &Artifact{LoadFailure: &ModuleLoadFailure{}, DimensionKeys: []*ArtifactDimensionKey{{Dimension: "x", Key: "a"}}}
	for _, tc := range []struct {
		name       string
		dimensions []string
		hasView    bool
	}{
		{name: "legacy"}, {name: "empty", hasView: true}, {name: "visible", dimensions: []string{"x"}, hasView: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := source.Clone()
			if tc.hasView {
				row = row.WithDimensionDisplay(tc.dimensions)
			}
			encoded, err := row.EncodePersistedObject(ctx, nil)
			require.NoError(t, err)
			decoded, err := (&Artifact{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(nil, 0, nil), encoded.JSON)
			require.NoError(t, err)
			restored := decoded.(*Artifact)
			require.Equal(t, row.dimensionDisplay, restored.dimensionDisplay)
			require.Equal(t, row.DisplayDimensionKeys(false), restored.DisplayDimensionKeys(false))
			require.Equal(t, source.DimensionKeys, restored.DimensionKeys)
			if !tc.hasView {
				var payload persistedArtifacts
				require.NoError(t, json.Unmarshal(encoded.JSON, &payload))
				require.Nil(t, payload.Entries[0].DimensionDisplay)
			}
		})
	}
}
