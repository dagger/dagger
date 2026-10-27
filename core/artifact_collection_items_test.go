package core

import (
	"context"
	"fmt"
	"testing"

	"github.com/dagger/dagger/core/dagaddress"
	"github.com/stretchr/testify/require"
)

// Every selected key produces an item check. Collection-level functions no
// longer replace item checks with native batches.
func artifactItemNameFixture() (*Artifacts, artifactCollectionKeyFunc) {
	module := &ArtifactDimension{Identifier: "app/modules", Name: "module"}
	test := &ArtifactDimension{Identifier: "app/modules/tests", Name: "test"}
	root := &ModTreeNode{Name: "modules"}
	item := &ModTreeNode{Name: "get", Parent: root, CollectionDimension: module}
	tests := &ModTreeNode{Name: "tests", Parent: item}
	receiver := &ModTreeNode{Name: "get", Parent: tests, CollectionDimension: test}
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

func TestArtifactCollectionItemNames(t *testing.T) {
	all, keys := artifactItemNameFixture()
	exclusion, err := dagaddress.Parse("modules/tests/check?test=x")
	require.NoError(t, err)
	excluded, err := all.WithoutURI(exclusion)
	require.NoError(t, err)
	for _, tc := range []struct {
		name      string
		selection *Artifacts
		want      []string
	}{
		{"whole collection", all, []string{
			"dag://modules/tests/check?module=b&test=z", "dag://modules/tests/check?module=b&test=x",
			"dag://modules/tests/check?module=a&test=z", "dag://modules/tests/check?module=a&test=x",
		}},
		{"parent filter", all.FilterDimensionKeys("module", []string{"a"}), []string{
			"dag://modules/tests/check?module=a&test=z", "dag://modules/tests/check?module=a&test=x",
		}},
		{"every key named", all.FilterDimensionKeys("test", []string{"x", "z"}), []string{
			"dag://modules/tests/check?module=b&test=z", "dag://modules/tests/check?module=b&test=x",
			"dag://modules/tests/check?module=a&test=z", "dag://modules/tests/check?module=a&test=x",
		}},
		{"key filter", all.FilterDimensionKeys("test", []string{"z"}), []string{
			"dag://modules/tests/check?module=b&test=z", "dag://modules/tests/check?module=a&test=z",
		}},
		{"exclusion", excluded, []string{
			"dag://modules/tests/check?module=b&test=z", "dag://modules/tests/check?module=a&test=z",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for range 2 {
				expanded, err := tc.selection.expand(t.Context(), keys)
				require.NoError(t, err)
				var names []string
				for _, item := range expanded.Entries {
					name, err := item.URI(ArtifactURIOpts{DimensionKeys: true})
					require.NoError(t, err)
					names = append(names, name)
					require.NotNil(t, item.Node.Parent.CollectionKey)
				}
				require.Equal(t, tc.want, names)
				// Expansion must preserve the reusable schema selection.
				require.Nil(t, all.Entries[0].Node.Parent.CollectionKey)
				require.Empty(t, all.Entries[0].DimensionKeys)
			}
		})
	}
}
