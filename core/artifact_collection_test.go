package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core/dagaddress"
	"github.com/stretchr/testify/require"
)

func collectionArtifactFixture() *Artifacts {
	artifacts := &Artifacts{}
	for _, field := range []string{"items", "other"} {
		dim := &ArtifactDimension{Identifier: "App." + field, Name: "item", QualifiedName: "app-" + field}
		collection := &ModTreeNode{Name: field}
		artifacts.Entries = append(artifacts.Entries, &Artifact{Path: []string{field}, Node: collection, TypeName: "Items"})
		for _, key := range []string{"b", "a"} {
			artifacts.Entries = append(artifacts.Entries, &Artifact{
				Path: []string{field}, TypeName: "Item",
				Node: &ModTreeNode{Parent: collection, Name: "get", CollectionDimension: dim, CollectionKey: &key},
			})
		}
	}
	return artifacts
}

func TestArtifactCollectionDimensionBinding(t *testing.T) {
	all := collectionArtifactFixture()
	_, err := all.FilterDimensionKeys("item", []string{"a"}).BindDimensions()
	require.ErrorContains(t, err, "ambiguous dimension")
	for _, selected := range []*Artifacts{
		all.FilterDimensionKeys("item", []string{"a"}).FilterPath([]string{"items"}),
		all.FilterPath([]string{"items"}).FilterDimensionKeys("item", []string{"a"}),
		all.FilterPath([]string{"items"}).FilterDimensionKeys("app-items", []string{"a"}),
		all.FilterPath([]string{"items"}).FilterDimensionKeys("App.items", []string{"a"}),
	} {
		expanded, err := selected.Expand(context.Background())
		require.NoError(t, err)
		require.Len(t, expanded.Entries, 1)
		require.Equal(t, []*ArtifactDimensionKey{{Dimension: "App.items", Key: "a"}}, expanded.Entries[0].DimensionKeys)
		uri, err := expanded.Entries[0].URI(ArtifactURIOpts{DimensionKeys: true})
		require.NoError(t, err)
		require.Equal(t, "dag://items?item=a", uri)
		addr, err := dagaddress.Parse(expanded.URI())
		require.NoError(t, err)
		roundTrip, err := all.FilterURI(addr)
		require.NoError(t, err)
		roundTrip, err = roundTrip.Expand(context.Background())
		require.NoError(t, err)
		require.Len(t, roundTrip.Entries, 1)
		require.Equal(t, expanded.Entries[0].DimensionKeys, roundTrip.Entries[0].DimensionKeys)
	}
	require.Empty(t, all.Selector.Dimensions)
}

func TestArtifactCollectionDimensionAlternatives(t *testing.T) {
	all := collectionArtifactFixture()
	selected := all.FilterDimensions([]string{"app-items", "app-other"})
	expanded, err := selected.Expand(context.Background())
	require.NoError(t, err)
	require.Len(t, expanded.Entries, 4)
	narrowed, err := selected.FilterPath([]string{"items"}).FilterDimensionKeys("item", []string{"a"}).Expand(context.Background())
	require.NoError(t, err)
	require.Len(t, narrowed.Entries, 1)
	for _, names := range [][]string{nil, {}, {"missing"}} {
		empty, err := all.FilterDimensions(names).Expand(context.Background())
		require.NoError(t, err)
		require.Empty(t, empty.Entries)
	}
}

func TestArtifactCollectionExactEndpoints(t *testing.T) {
	all := collectionArtifactFixture()
	collection, err := all.FilterPath([]string{"items"}).Expand(context.Background())
	require.NoError(t, err)
	require.Len(t, collection.Entries, 1)
	require.Equal(t, "Items", collection.Entries[0].TypeName)
	wildcard, err := all.FilterPattern("item*")
	require.NoError(t, err)
	wildcard, err = wildcard.FilterPattern("*s")
	require.NoError(t, err)
	items, err := wildcard.Expand(context.Background())
	require.NoError(t, err)
	require.Len(t, items.Entries, 3)
	addr, err := dagaddress.Parse(wildcard.URI())
	require.NoError(t, err)
	roundTrip, err := all.FilterURI(addr)
	require.NoError(t, err)
	roundTrip, err = roundTrip.Expand(context.Background())
	require.NoError(t, err)
	require.Len(t, roundTrip.Entries, 3)
}
