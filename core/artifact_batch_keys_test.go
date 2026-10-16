package core

import (
	"context"
	"fmt"
	"testing"

	"github.com/dagger/dagger/core/dagaddress"
	"github.com/stretchr/testify/require"
)

// An already selected batch receiver exercises Batch's actual grouping path
// without requiring module registration to resolve a replacement method.
func artifactBatchKeyFixture(group, key string) *Artifact {
	dimension := &ArtifactDimension{Identifier: "App.items"}
	receiver := &ModTreeNode{
		Name:                "batch",
		CollectionDimension: dimension,
		CollectionKey:       &key,
		Parent:              &ModTreeNode{Name: "items"},
	}
	return &Artifact{
		Path: []string{"items", "check"},
		DimensionKeys: []*ArtifactDimensionKey{
			{Dimension: "Workspace.group", Key: group},
			{Dimension: dimension.Identifier, Key: key},
		},
		Node: &ModTreeNode{Name: "check", Directives: []string{"check"}, Parent: receiver},
	}
}

func TestArtifactBatchKeyGrouping(t *testing.T) {
	first := artifactBatchKeyFixture("a", "z")
	otherPath := artifactBatchKeyFixture("a", "z")
	otherPath.Path = []string{"other", "check"}
	passthrough := &Artifact{Path: []string{"unbatched"}}
	all := &Artifacts{Entries: []*Artifact{
		first,
		artifactBatchKeyFixture("b", "q"),
		passthrough,
		artifactBatchKeyFixture("a", "a"),
		otherPath,
		artifactBatchKeyFixture("a", "z"), // A non-adjacent duplicate.
		artifactBatchKeyFixture("b", "q"),
		artifactBatchKeyFixture("a", ""),  // Empty strings are valid keys too.
		artifactBatchKeyFixture("b", "z"), // The same key in another parent.
		artifactBatchKeyFixture("a", "m"),
		artifactBatchKeyFixture("a", ""),
	}}

	// Snapshot values independently so mutations to shared node pointers or key
	// slices cannot also mutate the expected source values.
	var originalKeys []string
	var originalDimensions [][]ArtifactDimensionKey
	for _, entry := range all.Entries {
		if entry == passthrough {
			continue
		}
		originalKeys = append(originalKeys, *entry.Node.Parent.CollectionKey)
		var dimensions []ArtifactDimensionKey
		for _, key := range entry.DimensionKeys {
			dimensions = append(dimensions, *key)
		}
		originalDimensions = append(originalDimensions, dimensions)
	}

	// Reusing the selection must produce the same result: grouping state belongs
	// to one invocation, and batching must leave the expanded selection intact.
	for range 2 {
		result, err := all.Batch(t.Context())
		require.NoError(t, err)
		require.Len(t, result, 4)
		require.Equal(t, []string{"z", "a", "", "m"}, result[0].Node.Parent.CollectionKeys)
		require.Equal(t, []string{"q", "z"}, result[1].Node.Parent.CollectionKeys)
		require.Same(t, passthrough, result[2])
		require.Equal(t, otherPath.Path, result[3].Path)
		require.Equal(t, []string{"z"}, result[3].Node.Parent.CollectionKeys)
		require.Equal(t, []*ArtifactDimensionKey{
			{Dimension: "Workspace.group", Key: "a"},
			{Dimension: "App.items", Key: "z"},
			{Dimension: "App.items", Key: "a"},
			{Dimension: "App.items", Key: ""},
			{Dimension: "App.items", Key: "m"},
		}, result[0].DimensionKeys)
		require.Equal(t, []*ArtifactDimensionKey{
			{Dimension: "Workspace.group", Key: "b"},
			{Dimension: "App.items", Key: "q"},
			{Dimension: "App.items", Key: "z"},
		}, result[1].DimensionKeys)
		for _, index := range []int{0, 1, 3} {
			require.Nil(t, result[index].Node.Parent.CollectionKey)
		}
		require.NotSame(t, first.Node, result[0].Node)
		require.NotSame(t, first.Node.Parent, result[0].Node.Parent)

		index := 0
		for _, entry := range all.Entries {
			if entry == passthrough {
				continue
			}
			require.NotNil(t, entry.Node.Parent.CollectionKey)
			require.Equal(t, originalKeys[index], *entry.Node.Parent.CollectionKey)
			require.Nil(t, entry.Node.Parent.CollectionKeys)
			var dimensions []ArtifactDimensionKey
			for _, key := range entry.DimensionKeys {
				dimensions = append(dimensions, *key)
			}
			require.Equal(t, originalDimensions[index], dimensions)
			index++
		}
	}
}

func TestArtifactBatchKeyIndexThreshold(t *testing.T) {
	for _, unique := range []int{7, 8, 9, 16} {
		t.Run(fmt.Sprint(unique), func(t *testing.T) {
			all := &Artifacts{}
			want := make([]string, unique)
			var sourceKeys []string
			for i := range unique {
				want[i] = fmt.Sprintf("key%d", i)
				// Revisit the first key before each new insertion. This includes
				// non-adjacent duplicates on both sides of index promotion.
				if i > 0 {
					all.Entries = append(all.Entries, artifactBatchKeyFixture("a", want[0]))
					sourceKeys = append(sourceKeys, want[0])
				}
				all.Entries = append(all.Entries, artifactBatchKeyFixture("a", want[i]))
				sourceKeys = append(sourceKeys, want[i])
			}
			// The key that triggers promotion must also be in the new index.
			all.Entries = append(all.Entries, artifactBatchKeyFixture("a", want[unique-1]))
			sourceKeys = append(sourceKeys, want[unique-1])
			result, err := all.Batch(t.Context())
			require.NoError(t, err)
			require.Len(t, result, 1)
			require.Equal(t, want, result[0].Node.Parent.CollectionKeys)
			require.Len(t, result[0].DimensionKeys, unique+1)
			for i, key := range want {
				require.Equal(t, &ArtifactDimensionKey{Dimension: "App.items", Key: key}, result[0].DimensionKeys[i+1])
			}
			for i, entry := range all.Entries {
				require.Equal(t, sourceKeys[i], *entry.Node.Parent.CollectionKey)
				require.Nil(t, entry.Node.Parent.CollectionKeys)
				require.Equal(t, []*ArtifactDimensionKey{
					{Dimension: "Workspace.group", Key: "a"},
					{Dimension: "App.items", Key: sourceKeys[i]},
				}, entry.DimensionKeys)
			}
		})
	}
}

func BenchmarkArtifactBatchUniqueKeys(b *testing.B) {
	for _, size := range []int{1, 10, 100, 1000, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			all := &Artifacts{}
			want := make([]string, size)
			for i := range size {
				want[i] = fmt.Sprintf("key%06d", i)
				all.Entries = append(all.Entries, artifactBatchKeyFixture("group", want[i]))
			}
			b.ReportAllocs()
			var result []*Artifact
			for b.Loop() {
				var err error
				result, err = all.Batch(b.Context())
				if err != nil {
					b.Fatal(err)
				}
			}
			require.Len(b, result, 1)
			require.Equal(b, want, result[0].Node.Parent.CollectionKeys)
		})
	}
}

// batchNameFixture is a check beneath a batch receiver nested in a parent
// collection, as go-package > go-test is.
func batchNameFixture() (*Artifacts, artifactCollectionKeyFunc) {
	module := &ArtifactDimension{Identifier: "app/modules", Name: "module"}
	test := &ArtifactDimension{Identifier: "app/modules/tests", Name: "test"}
	root := &ModTreeNode{Name: "modules"}
	item := &ModTreeNode{Name: "get", Parent: root, CollectionDimension: module}
	tests := &ModTreeNode{Name: "tests", Parent: item}
	receiver := &ModTreeNode{Name: "batch", Parent: tests, CollectionDimension: test}
	all := &Artifacts{Entries: []*Artifact{{
		Path: []string{"modules", "tests", "check"},
		Node: &ModTreeNode{Name: "check", Directives: []string{"check"}, Parent: receiver},
	}}}
	all.AllDimensions = all.DimensionDefinitions()
	return all, func(_ context.Context, receiver *Artifact) ([]collectionKey, error) {
		switch receiver.Node.Name {
		case "modules":
			return []collectionKey{{text: "b"}, {text: "a"}}, nil
		case "tests":
			return []collectionKey{{text: "z"}, {text: "x"}}, nil
		}
		return nil, fmt.Errorf("unexpected collection receiver %q", receiver.Node.Name)
	}
}

func TestArtifactBatchName(t *testing.T) {
	all, keys := batchNameFixture()
	exclusion, err := dagaddress.Parse("modules/tests/check?test=x")
	require.NoError(t, err)
	excluded, err := all.WithoutURI(exclusion)
	require.NoError(t, err)
	for _, tc := range []struct {
		name      string
		selection *Artifacts
		want      []string
	}{
		{
			name:      "whole collection",
			selection: all,
			want:      []string{"dag://modules/tests/check?module=b", "dag://modules/tests/check?module=a"},
		},
		{
			name:      "parent filter",
			selection: all.FilterDimensionKeys("module", []string{"a"}),
			want:      []string{"dag://modules/tests/check?module=a"},
		},
		{
			// A cloud engine selects a batch by its full address.
			name:      "every key named",
			selection: all.FilterDimensionKeys("test", []string{"x", "z"}),
			want:      []string{"dag://modules/tests/check?module=b", "dag://modules/tests/check?module=a"},
		},
		{
			name:      "key filter",
			selection: all.FilterDimensionKeys("test", []string{"z"}),
			want:      []string{"dag://modules/tests/check?module=b&test=z", "dag://modules/tests/check?module=a&test=z"},
		},
		{
			name:      "exclusion",
			selection: excluded,
			want:      []string{"dag://modules/tests/check?module=b&test=z", "dag://modules/tests/check?module=a&test=z"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expanded, err := tc.selection.expand(t.Context(), keys)
			require.NoError(t, err)
			batches, err := expanded.Batch(t.Context())
			require.NoError(t, err)
			var names []string
			for _, batch := range batches {
				name, err := batch.URI(ArtifactURIOpts{DimensionKeys: true})
				require.NoError(t, err)
				names = append(names, name)
				// The full address still names every item in the batch.
				full, err := batch.URI(ArtifactURIOpts{DimensionKeys: true, Full: true})
				require.NoError(t, err)
				for _, key := range batch.Node.Parent.CollectionKeys {
					require.Contains(t, full, "test="+key)
				}
			}
			require.Equal(t, tc.want, names)
		})
	}
}
