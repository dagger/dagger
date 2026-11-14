package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestArtifactDimensionDisplay(t *testing.T) {
	item := func(key string) *Artifact {
		module := &ModTreeNode{Name: "app", Parent: &ModTreeNode{}}
		items := &ModTreeNode{Name: "items", Parent: module}
		return &Artifact{ModuleName: "app", TypeName: "Item", Path: []string{"app", "items"},
			Node: &ModTreeNode{Name: "get", Parent: items, CollectionKey: &key,
				CollectionDimension: &ArtifactDimension{Identifier: "Items", CollectionType: "Items", ItemType: "Item"}},
			DimensionKeys: []*ArtifactDimensionKey{{Dimension: "module", Key: "app"}, {Dimension: "Items", Key: key}},
		}
	}
	source := item("a")
	complete := []*ArtifactDimensionKey{{Dimension: "module", Key: "app"}, {Dimension: "Items", Key: "a"}, {Dimension: "name", Key: "items"}}
	visible := []*ArtifactDimensionKey{{Dimension: "Items", Key: "a"}}
	selection := &Artifacts{Entries: []*Artifact{source, item("b")}}
	require.Equal(t, []string{"Items"}, selection.Dimensions())
	row := source.WithDimensionDisplay(selection.Dimensions())
	require.Equal(t, visible, row.DisplayDimensionKeys(false))
	require.Equal(t, complete, row.DisplayDimensionKeys(true))
	require.Equal(t, complete, source.DisplayDimensionKeys(false))
	clone := row.Clone()
	clone.dimensionDisplay.Dimensions[0] = "module"
	require.Equal(t, visible, row.DisplayDimensionKeys(false))
	returned := row.DisplayDimensionKeys(false)
	returned[0].Key = "changed"
	require.Equal(t, "a", source.DimensionKeys[1].Key)
	one, err := (&Artifacts{Entries: []*Artifact{source}}).One()
	require.NoError(t, err)
	require.Empty(t, one.DisplayDimensionKeys(false))
	require.Equal(t, complete, one.DisplayDimensionKeys(true))
}

func TestArtifactSchemaDimensionsPreserved(t *testing.T) {
	source := &Artifacts{
		Entries:       []*Artifact{{ModuleName: "app", TypeName: "Check", Path: []string{"app", "check"}}},
		AllDimensions: artifact.Dimensions{{Identifier: "module"}, {Kind: "COLLECTION", Identifier: "EmptyItems", CollectionType: "EmptyItems", ItemType: "Item"}, {Identifier: "type:Check"}},
	}
	want := []string{"EmptyItems", "module", "name"}
	for _, selection := range []*Artifacts{
		source, source.FilterTypes([]string{}, false), source.FilterPath([]string{"missing"}), source.FilterDimensionKeys("EmptyItems", []string{}),
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
	source := &Artifact{ModuleName: "broken", TypeName: "Check", Path: []string{"broken", "load"}, LoadFailure: &ModuleLoadFailure{Name: "broken", Message: "module unavailable"}, DimensionKeys: []*ArtifactDimensionKey{}}
	for _, tc := range []struct {
		name       string
		dimensions []string
		hasView    bool
	}{
		{name: "legacy"}, {name: "empty", hasView: true}, {name: "visible", dimensions: []string{"module"}, hasView: true},
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
