package core

import (
	"testing"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/stretchr/testify/require"
)

func TestArtifactDirectNestedCollectionTypeColumns(t *testing.T) {
	rowKey, columnKey := "top", "left"
	module := &ModTreeNode{Name: "grid", Parent: &ModTreeNode{}}
	rows := &ModTreeNode{Name: "rows", Parent: module}
	row := &ModTreeNode{Name: "get", Parent: rows, CollectionKey: &rowKey, CollectionDimension: &ArtifactDimension{
		Identifier: "/grid/rows", CollectionType: "Rows", ItemType: "Row", KeyName: "row",
	}}
	cell := &ModTreeNode{Name: "get", Parent: row, CollectionKey: &columnKey, CollectionDimension: &ArtifactDimension{
		Identifier: "/grid/rows", CollectionType: "Row", ItemType: "Cell", KeyName: "column",
	}}
	entry := &Artifact{ModuleName: "grid", TypeName: "Cell", Path: []string{"grid", "rows"}, Node: cell}
	field := entry.ProjectionField()
	require.Equal(t, flattening.Field{
		Module: "grid", Type: "Cell", LocalPath: []string{},
		Collections: []flattening.Collection{
			{Fields: []string{"rows"}, Type: "Rows", ItemType: "Row", KeyName: "row"},
			{Fields: []string{}, Type: "Row", ItemType: "Cell", KeyName: "column"},
		},
	}, field)
	schema, err := flattening.NewSchema([]flattening.Field{field})
	require.NoError(t, err)
	want := []flattening.Coordinate{
		{Dimension: "module", Key: "grid"}, {Dimension: "Rows", Key: "top"},
		{Dimension: "Row", Key: "left"}, {Dimension: "name", Key: "."},
	}
	projected, err := schema.Coordinates(field, rowKey, columnKey)
	require.NoError(t, err)
	require.Equal(t, want, projected)
	require.Equal(t, want, entry.CanonicalCoordinates())

	// Two source fields can have equal projected rows. Source addresses must
	// still retain the field path that selects each artifact.
	other := *entry
	other.Path = []string{"grid", "otherRows"}
	require.Equal(t, entry.CanonicalCoordinates(), other.CanonicalCoordinates())
	require.NotEqual(t, entry.AddressIdentity(), other.AddressIdentity())
}

func TestArtifactCollectionTypeFilterKeepsModuleLoadFailure(t *testing.T) {
	failed := &Artifact{
		ModuleName: "app", TypeName: "Check", Path: []string{"app", "load"},
		LoadFailure: &ModuleLoadFailure{Name: "app", Message: "app failed", Entrypoint: false},
	}
	all := &Artifacts{Entries: []*Artifact{failed}}
	selected, err := all.FilterDimensionKeys("AppTests", []string{"unit"}).SchemaSelection()
	require.NoError(t, err)
	require.Equal(t, []*Artifact{failed}, selected.Entries)
}

func TestArtifactURIRequiresAbsentDimensionProof(t *testing.T) {
	entry := &Artifact{
		ModuleName: "grid", TypeName: "Check", Path: []string{"grid", "rows", "check"},
		DimensionKeys: []*ArtifactDimensionKey{{Dimension: "Rows", Key: "top"}},
		links:         &artifact.Links{PathNeedsAbsence: true, CompleteSchema: true},
	}
	opts := ArtifactURIOpts{Prune: true, DimensionKeys: true, TypeAssertion: true}
	uri, err := entry.URI(opts)
	require.ErrorContains(t, err, "absent-dimension")
	require.Empty(t, uri)

	// A complete runtime snapshot can prove the child collection is empty.
	// An empty key set is a valid proof that the type alone selects this row.
	target := entry.addressRow(true)
	index := artifact.NewAddressIndex([]artifact.AddressRow{target}, true)
	keys, proven := index.Keys(target)
	require.True(t, proven)
	require.Empty(t, keys)
	entry.addressPlans = &artifactAddressPlans{Typed: &artifactAddressKeys{Keys: keys}}
	uri, err = entry.URI(opts)
	require.NoError(t, err)
	require.Equal(t, "dag+check://", uri)
}
