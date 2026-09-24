package daggercmd

import (
	"slices"
	"testing"

	"github.com/dagger/dagger/core/artifact"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestArtifactCollectionTypeKeys(t *testing.T) {
	item := listedArtifact{
		URI: "dag+go-test://go/modules/tests?go-module=./api&go-test=TestHealth", CollectionItem: true,
		DimensionKeys: []struct{ Dimension, Key string }{{"Go.modules", "./api"}, {"GoModule.tests", "TestHealth"}, {"type:GoTest", "go/modules/tests"}},
	}
	path := artifactListPath{URI: "dag+check://go/modules/tests/run", Dimensions: []string{"Go.modules", "GoModule.tests", "type:Check"}}
	for _, tc := range []struct {
		name  string
		paths []artifactListPath
		omit  bool
	}{
		{"descendant operations", []artifactListPath{path}, true},
		{"module checks do not match a test key", []artifactListPath{path, {URI: "dag+check://go/modules/test", Dimensions: []string{"Go.modules", "type:Check"}}}, true},
		{"empty sibling counts", []artifactListPath{path, {URI: "dag+check://other/tests/run", Dimensions: path.Dimensions}}, false},
		{"unknown schema", nil, false},
		{"path boundaries", []artifactListPath{{URI: "dag+check://go/modules/tests-extra/run", Dimensions: path.Dimensions}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := item
			other.DimensionKeys = slices.Clone(item.DimensionKeys)
			other.DimensionKeys[0].Key = "other"
			items := []listedArtifact{item, other}
			require.NoError(t, omitCollectionTypeKeys(items, tc.paths))
			for _, got := range items {
				require.Equal(t, tc.omit, got.OmitTypeKey)
				require.Equal(t, item.URI, got.URI, "display must preserve the complete link")
			}
		})
	}
}

func TestArtifactListProduct(t *testing.T) {
	row := func(module, test string) listedArtifact {
		return listedArtifact{DimensionKeys: []struct{ Dimension, Key string }{{"Go.modules", module}, {"GoModule.tests", test}}}
	}
	t.Run("full product collapses", func(t *testing.T) {
		items := []listedArtifact{row("api", "health"), row("api", "ready"), row("worker", "health"), row("worker", "ready")}
		require.True(t, artifactRowsFormProduct(items))
		require.Len(t, artifactListRows(items, false), 1)
		require.Len(t, artifactListRows(items, true), 4)
	})
	t.Run("diagonal pairs cannot become independent flags", func(t *testing.T) {
		items := []listedArtifact{row("api", "health"), row("worker", "ready")}
		require.False(t, artifactRowsFormProduct(items))
		require.Len(t, artifactListRows(items, false), 2)
	})
	t.Run("duplicate rows cannot fill missing pairs", func(t *testing.T) {
		items := []listedArtifact{row("api", "health"), row("api", "health"), row("worker", "ready"), row("worker", "ready")}
		require.False(t, artifactRowsFormProduct(items))
	})
	t.Run("missing dimension is not an empty key", func(t *testing.T) {
		missing := listedArtifact{DimensionKeys: []struct{ Dimension, Key string }{{"Go.modules", "api"}}}
		require.False(t, artifactRowsFormProduct([]listedArtifact{row("api", ""), missing}))
	})
	t.Run("empty keys can form a product", func(t *testing.T) {
		require.True(t, artifactRowsFormProduct([]listedArtifact{row("api", ""), row("worker", "")}))
	})
	t.Run("order does not change identity", func(t *testing.T) {
		a, b := row("api", "health"), row("worker", "health")
		slices.Reverse(b.DimensionKeys)
		require.True(t, artifactRowsFormProduct([]listedArtifact{a, b}))
	})
}

func TestArtifactProjectionFlagNames(t *testing.T) {
	defs := artifact.Dimensions{
		{Identifier: "Build.skip", Name: "skip", QualifiedName: "build-skip"},
		{Identifier: "Go.tests", Name: "go-test", QualifiedName: "go-tests"},
	}
	// list has no --skip option. check does. Both must print the same key flag.
	root := &cobra.Command{Use: "dagger"}
	list := &cobra.Command{Use: "list"}
	check := &cobra.Command{Use: "check"}
	check.Flags().StringArray("skip", nil, "")
	root.AddCommand(list, check)
	for _, cmd := range []*cobra.Command{list, check} {
		require.Equal(t, "build-skip", artifactDimensionFlagNames(cmd, defs)[defs[0].Identifier].Key)
		require.Equal(t, "go-test", artifactDimensionFlagNames(cmd, defs)[defs[1].Identifier].Key)
	}
}
