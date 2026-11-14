package core

import (
	"encoding/json"
	"fmt"
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
		{Dimension: "Row", Key: "left"}, {Dimension: "name", Key: "rows"},
	}
	projected, err := schema.Coordinates(field, rowKey, columnKey)
	require.NoError(t, err)
	require.Equal(t, want, projected)
	require.Equal(t, want, entry.CanonicalCoordinates())

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

func TestArtifactURIUsesAbsentDimensionProof(t *testing.T) {
	entry := &Artifact{
		ModuleName: "grid", TypeName: "Check", Path: []string{"grid", "rows", "check"},
		DimensionKeys: []*ArtifactDimensionKey{{Dimension: "Rows", Key: "top"}},
		links:         &artifact.Links{CompleteSchema: true, TypedAbsent: []string{"Cell"}},
	}
	key := "top"
	module := &ModTreeNode{Name: "grid", Parent: &ModTreeNode{}}
	rows := &ModTreeNode{Name: "rows", Parent: module}
	item := &ModTreeNode{Name: "get", Parent: rows, CollectionKey: &key,
		CollectionDimension: &ArtifactDimension{Identifier: "Rows", CollectionType: "Rows", ItemType: "Row"}}
	entry.Node = &ModTreeNode{Name: "check", Parent: item}
	opts := ArtifactURIOpts{Canonical: true, DimensionKeys: true, TypeAssertion: true}
	uri, err := entry.URI(opts)
	require.NoError(t, err)
	require.Equal(t, "dag+check://grid/rows/check?!Cell&Rows=top", uri)

	target := entry.addressRow(true)
	index := artifact.NewAddressIndex([]artifact.AddressRow{target}, true)
	selectors, proven := index.Selectors(target)
	require.True(t, proven)
	require.Empty(t, selectors)
	entry.addressPlans = &artifactAddressPlans{Typed: &artifactAddressPlan{Selectors: selectors}}
	uri, err = entry.URI(opts)
	require.NoError(t, err)
	require.Equal(t, "dag+check://", uri)
}

func TestArtifactSnapshotRejectsStaleProjectionMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, version string
	}{
		{"old snapshot without version", ""},
		{"unsupported future snapshot", fmt.Sprintf(`"Version":%d,`, artifactSnapshotVersion+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := json.RawMessage(fmt.Sprintf(`{%s"Entries":[{
  "Node":12345,"ModuleName":"app","TypeName":"Check","Path":["app","tests","check"],
  "DimensionNames":{"Tests":"old-label"},
  "AddressPlans":{"Typed":{"Keys":[{"Dimension":"name","Key":"old-local-name"}]}}
}]}`, tc.version))
			decoded, err := decodeArtifacts(t.Context(), nil, payload)
			require.ErrorContains(t, err, "unsupported artifact snapshot version")
			require.Nil(t, decoded)
		})
	}
}
