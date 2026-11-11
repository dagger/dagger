package core

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestArtifactDimensionDisplay(t *testing.T) {
	coordinates := []*ArtifactDimensionKey{{Dimension: "module", Key: "app"}, {Dimension: "/app/items", Key: "a"}}
	source := &Artifact{ModuleName: "app", DimensionKeys: coordinates}
	complete := append(slices.Clone(coordinates), &ArtifactDimensionKey{Dimension: "name", Key: "."})
	selection := &Artifacts{Entries: []*Artifact{source, {ModuleName: "app", DimensionKeys: []*ArtifactDimensionKey{{Dimension: "module", Key: "app"}, {Dimension: "/app/items", Key: "b"}}}}}
	require.Equal(t, []string{"/app/items"}, selection.Dimensions())
	row := source.WithDimensionDisplay(selection.Dimensions())
	require.Equal(t, coordinates[1:], row.DisplayDimensionKeys(false))
	require.Equal(t, complete, row.DisplayDimensionKeys(true))
	require.Equal(t, complete, source.DisplayDimensionKeys(false))
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
	require.Equal(t, complete, one.DisplayDimensionKeys(true))
	require.Equal(t, coordinates, source.DimensionKeys)
}

func TestArtifactSchemaDimensionsPreserved(t *testing.T) {
	source := &Artifacts{
		Entries:       []*Artifact{{ModuleName: "app", TypeName: "Check", Path: []string{"app", "check"}}},
		AllDimensions: artifact.Dimensions{{Identifier: "module"}, {Identifier: "/app/empty"}, {Identifier: "type:Check"}},
	}
	want := []string{"/app/empty", "module", "name"}
	for _, selection := range []*Artifacts{
		source, source.FilterTypes([]string{}, false), source.FilterPath([]string{"missing"}), source.FilterDimensionKeys("/app/empty", []string{}),
	} {
		require.Equal(t, want, selection.SchemaDimensions())
	}
	fallback := &Artifacts{Entries: source.Entries}
	empty := fallback.FilterTypes([]string{}, false)
	require.Equal(t, []string{"module", "name"}, empty.SchemaDimensions())
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

func TestArtifactDimensionDisplaySourcePersistence(t *testing.T) {
	ctx := t.Context()
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "artifact-display-source", SessionID: "artifact-display-source"})
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = dagql.ContextWithCache(ctx, cache)
	srv := newCoreDagqlServerForTest(t, &Query{})
	srv.InstallObject(dagql.NewClass[*Artifacts](srv))
	dagql.Fields[*Query]{
		dagql.Func("displaySource", func(context.Context, *Query, struct{}) (*Artifacts, error) {
			return &Artifacts{}, nil
		}),
		dagql.Func("otherDisplaySource", func(context.Context, *Query, struct{}) (*Artifacts, error) {
			return &Artifacts{AllDimensions: artifact.Dimensions{{Identifier: "name"}}}, nil
		}),
	}.Install(srv)
	var source, other dagql.ObjectResult[*Artifacts]
	require.NoError(t, srv.Select(ctx, srv.Root(), &source, dagql.Selector{Field: "displaySource"}))
	require.NoError(t, srv.Select(ctx, srv.Root(), &other, dagql.Selector{Field: "otherDisplaySource"}))
	row := (&Artifact{LoadFailure: &ModuleLoadFailure{}}).WithDimensionDisplaySource(source)
	encoded, err := row.EncodePersistedObject(ctx, dagql.NewPersistEncodeContext(cache, 0, nil))
	require.NoError(t, err)
	decoded, err := (&Artifact{}).DecodePersistedObject(ctx, dagql.NewPersistDecodeContext(srv, 0, nil), encoded.JSON)
	require.NoError(t, err)
	restored := decoded.(*Artifact)
	sourceID, err := source.ID()
	require.NoError(t, err)
	restoredID, err := restored.DimensionDisplaySource().ID()
	require.NoError(t, err)
	require.Equal(t, sourceID.EngineResultID(), restoredID.EngineResultID())

	// Dependency attachment rewrites only the clone's wrapper.
	clone := restored.Clone()
	owned, err := clone.AttachDependencyResults(ctx, nil, func(dagql.AnyResult) (dagql.AnyResult, error) { return other, nil })
	require.NoError(t, err)
	require.Len(t, owned, 1)
	otherID, err := other.ID()
	require.NoError(t, err)
	require.NotEqual(t, sourceID.EngineResultID(), otherID.EngineResultID())
	cloneID, err := clone.DimensionDisplaySource().ID()
	require.NoError(t, err)
	require.Equal(t, otherID.EngineResultID(), cloneID.EngineResultID())
	restoredID, err = restored.DimensionDisplaySource().ID()
	require.NoError(t, err)
	require.Equal(t, sourceID.EngineResultID(), restoredID.EngineResultID())
	require.Nil(t, restored.WithDimensionDisplay([]string{}).DimensionDisplaySource().Self())
}
