package core

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArtifactTypeDimensionPrunesBeforeExpansion(t *testing.T) {
	all := &Artifacts{Entries: []*Artifact{{Path: []string{"static"}, TypeName: "Container"}}}
	for _, field := range []string{"items", "other"} {
		all.Entries = append(all.Entries, &Artifact{
			Path: []string{field}, TypeName: "Item",
			Node: &ModTreeNode{Name: "get", Parent: &ModTreeNode{Name: field},
				CollectionDimension: &ArtifactDimension{Identifier: "App." + field, Name: "item", QualifiedName: "app-" + field}},
		})
	}
	keys := func(_ context.Context, receiver *Artifact) ([]collectionKey, error) {
		if receiver.Node.Name != "items" {
			return nil, fmt.Errorf("excluded collection %s was read", receiver.Node.Name)
		}
		return []collectionKey{{text: "a"}}, nil
	}
	for _, typeFirst := range []bool{true, false} {
		selected := all
		if typeFirst {
			selected = selected.FilterDimensionKeys("type:Item", []string{"items"}).FilterDimensionKeys("App.items", []string{"a"})
		} else {
			selected = selected.FilterDimensionKeys("App.items", []string{"a"}).FilterDimensionKeys("type:Item", []string{"items"})
		}
		expanded, err := selected.expand(t.Context(), keys)
		require.NoError(t, err)
		require.Len(t, expanded.Entries, 1)
		for _, item := range expanded.Entries {
			require.Equal(t, []*ArtifactDimensionKey{{Dimension: "App.items", Key: "a"}, {Dimension: "type:Item", Key: "items"}}, item.DimensionKeys)
			uri, err := item.URI(ArtifactURIOpts{DimensionKeys: true})
			require.NoError(t, err)
			require.Equal(t, "dag://items?item=a", uri)
		}
	}
	static, err := all.FilterDimensionKeys("type:Container", []string{"static"}).expand(t.Context(), func(context.Context, *Artifact) ([]collectionKey, error) {
		return nil, fmt.Errorf("static type filter read a collection")
	})
	require.NoError(t, err)
	require.Len(t, static.Entries, 1)
	require.Equal(t, []*ArtifactDimensionKey{{Dimension: "type:Container", Key: "static"}}, static.Entries[0].DimensionKeys)
}
