package core

import (
	"context"
	"fmt"
	"testing"

	"github.com/dagger/dagger/core/dagaddress"
	"github.com/stretchr/testify/require"
)

func TestArtifactModuleSelectionBeforeExpansion(t *testing.T) {
	all := &Artifacts{Entries: []*Artifact{
		{ModuleName: "go", Path: []string{"test"}, TypeName: "Check"},
		{ModuleName: "playwright", Path: []string{"playwright", "test"}, TypeName: "Check"},
		{ModuleName: "go-tests", Path: []string{"go-tests", "test"}, TypeName: "Check", Node: &ModTreeNode{Name: "get", Parent: &ModTreeNode{Name: "tests"}, CollectionDimension: &ArtifactDimension{Kind: "COLLECTION", Identifier: "other/tests", Name: "test"}}},
	}}
	selected, err := all.FilterDimensionKeys("module", []string{"go", "playwright"}).expand(t.Context(), func(context.Context, *Artifact) ([]collectionKey, error) {
		return nil, fmt.Errorf("module selection must not load collection keys")
	})
	require.NoError(t, err)
	require.Len(t, selected.Entries, 2)
	require.Equal(t, []string{"go", "playwright"}, selected.DimensionKeys("module"))
	uri, err := selected.Entries[0].URI(ArtifactURIOpts{DimensionKeys: true})
	require.NoError(t, err)
	require.Equal(t, "dag://test", uri, "the path already identifies the module")
	address, err := dagaddress.Parse("dag://?module=playwright")
	require.NoError(t, err)
	selected, err = all.FilterURI(address)
	require.NoError(t, err)
	selected, err = selected.SchemaSelection()
	require.NoError(t, err)
	require.Len(t, selected.Entries, 1)
	require.Equal(t, "playwright", selected.Entries[0].ModuleName)
}

func TestArtifactStaticDimensionExclusions(t *testing.T) {
	all := &Artifacts{Entries: []*Artifact{
		{Path: []string{"lint"}, TypeName: "Check"},
		{Path: []string{"test"}, TypeName: "Check"},
		{Path: []string{"build"}, TypeName: "Container"},
	}}
	for _, tc := range []struct {
		uri  string
		want []string
	}{
		{"dag://?type:Check=lint", []string{"test", "build"}},
		{"dag://?type:Check=missing", []string{"lint", "test", "build"}},
		{"dag://?type:Check", []string{"build"}},
		{"dag://?unknown=value", []string{"lint", "test", "build"}},
	} {
		t.Run(tc.uri, func(t *testing.T) {
			address, err := dagaddress.Parse(tc.uri)
			require.NoError(t, err)
			selected, err := all.WithoutURI(address)
			require.NoError(t, err)
			selected, err = selected.Expand(t.Context())
			require.NoError(t, err)
			var paths []string
			for _, entry := range selected.Entries {
				paths = append(paths, entry.Path[0])
			}
			require.Equal(t, tc.want, paths)
		})
	}
}

func TestArtifactTypeDimensionPrunesBeforeExpansion(t *testing.T) {
	all := &Artifacts{Entries: []*Artifact{{Path: []string{"static"}, TypeName: "Container"}}}
	for _, field := range []string{"items", "other"} {
		all.Entries = append(all.Entries, &Artifact{
			Path: []string{field}, TypeName: "Item",
			Node: &ModTreeNode{Name: "get", Parent: &ModTreeNode{Name: field},
				CollectionDimension: &ArtifactDimension{Identifier: "app/" + field, Name: "item", QualifiedName: "app-" + field}},
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
			selected = selected.FilterDimensionKeys("type:Item", []string{"items"}).FilterDimensionKeys("app/items", []string{"a"})
		} else {
			selected = selected.FilterDimensionKeys("app/items", []string{"a"}).FilterDimensionKeys("type:Item", []string{"items"})
		}
		expanded, err := selected.expand(t.Context(), keys)
		require.NoError(t, err)
		require.Len(t, expanded.Entries, 1)
		for _, item := range expanded.Entries {
			require.Equal(t, []*ArtifactDimensionKey{{Dimension: "app/items", Key: "a"}, {Dimension: "type:Item", Key: "items"}}, item.DimensionKeys)
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

func TestArtifactCollectionExclusionsPruneSchemaPaths(t *testing.T) {
	all, _ := parallelCollectionFixture(false)
	for _, item := range all.Entries {
		item.TypeName = "Check"
	}
	for _, uri := range []string{"dag://a/**", "dag://?type:Check=a/check"} {
		t.Run(uri, func(t *testing.T) {
			address, err := dagaddress.Parse(uri)
			require.NoError(t, err)
			selected, err := all.WithoutURI(address)
			require.NoError(t, err)
			schema, err := selected.SchemaSelection()
			require.NoError(t, err)
			require.Len(t, schema.Entries, 1)
			require.Equal(t, []string{"b", "check"}, schema.Entries[0].Path)
			expanded, err := selected.expand(t.Context(), func(_ context.Context, receiver *Artifact) ([]collectionKey, error) {
				require.Equal(t, "b", receiver.Node.Name, "excluded collection must not be read")
				return []collectionKey{{text: "one"}}, nil
			})
			require.NoError(t, err)
			require.Len(t, expanded.Entries, 1)
		})
	}
}
