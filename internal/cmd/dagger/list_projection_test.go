package daggercmd

import (
	"slices"
	"testing"

	"github.com/dagger/dagger/core/artifact"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestArtifactLinkProjection(t *testing.T) {
	module := "Go.modules"
	test := "GoModule.tests"
	item := listedArtifact{
		URI:           "dag+check://go/modules/tests/run?go-module=./api&go-test=TestHealth",
		DimensionKeys: []struct{ Dimension, Key string }{{module, "./api"}, {test, "TestHealth"}},
	}
	path := artifactListPath{URI: "dag+check://go/modules/tests/run", Dimensions: []string{module, test}}
	tests := []struct {
		name  string
		item  listedArtifact
		paths []artifactListPath
		omit  bool
	}{
		{"one operation", item, []artifactListPath{path}, true},
		{"module-wide checks do not match a test key", item, []artifactListPath{path,
			{URI: "dag+check://go/modules/generate/stale", Dimensions: []string{module}},
		}, true},
		{"sibling operation counts even without runtime items", item, []artifactListPath{path,
			{URI: "dag+check://go/modules/tests/bench", Dimensions: []string{module, test}},
		}, false},
		{"same dimensions at another path", item, []artifactListPath{path,
			{URI: "dag+check://other/modules/tests/run", Dimensions: []string{module, test}},
		}, false},
		{"unknown schema keeps the link", item, nil, false},
		{"unknown dimension keeps the link", item, []artifactListPath{
			{URI: path.URI, Dimensions: []string{module}},
		}, false},
		{"static row keeps the link", listedArtifact{URI: "dag+check://lint"}, []artifactListPath{{URI: "dag+check://lint"}}, false},
		{"absolute row keeps workspace and revision", listedArtifact{
			URI:           "dag+check://github.com/acme/project@abc:go/modules/tests/run?go-module=./api&go-test=TestHealth",
			DimensionKeys: item.DimensionKeys,
		}, []artifactListPath{path}, false},
		{"collection row includes descendant operations", listedArtifact{
			URI: "dag+go-test://go/modules/tests?go-module=./api&go-test=TestHealth", CollectionItem: true,
			DimensionKeys: item.DimensionKeys,
		}, []artifactListPath{path,
			{URI: "dag+container://go/modules/tests/container", Dimensions: []string{module, test}},
		}, true},
		{"path boundaries are literal", listedArtifact{
			URI: "dag+go-test://go/modules/test", CollectionItem: true, DimensionKeys: item.DimensionKeys,
		}, []artifactListPath{path}, false},
		{"type assertions remain part of scope", item, []artifactListPath{
			{URI: "dag+container://go/modules/tests/run", Dimensions: []string{module, test}},
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Different key values must produce the same schema decision.
			other := tt.item
			other.DimensionKeys = slices.Clone(tt.item.DimensionKeys)
			for i := range other.DimensionKeys {
				other.DimensionKeys[i].Key = "other"
			}
			items := []listedArtifact{tt.item, other}
			require.NoError(t, projectArtifactLinks(items, tt.paths))
			for _, got := range items {
				require.Equal(t, tt.omit, got.CLIFlagsOnly)
				require.Equal(t, tt.item.URI, got.URI, "projection must not modify complete identity")
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
		require.Equal(t, "build-skip", artifactDimensionFlagName(cmd, defs, defs[0]))
		require.Equal(t, "go-test", artifactDimensionFlagName(cmd, defs, defs[1]))
	}
}
