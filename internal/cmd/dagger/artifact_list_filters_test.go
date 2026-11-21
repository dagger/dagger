package daggercmd

import (
	"testing"

	"github.com/dagger/dagger/core/artifact"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestArtifactListURIKeyKinds(t *testing.T) {
	types := artifact.Dimensions{
		{Identifier: "type:Check", Kind: "TYPE", Name: "check", QualifiedName: "artifact-check"},
		{Identifier: "name", Kind: "NAME", Name: "name"},
	}
	for _, test := range []struct {
		name, uri  string
		dimensions artifact.Dimensions
		expand     bool
	}{
		{"type alias selects schema paths", "dag://?check=stale", types, false},
		{"name selects schema paths", "dag://?name=generate.stale", types, false},
		{"collection alias has priority", "dag://?check=unit", append(append(artifact.Dimensions{}, types...), &artifact.Dimension{Identifier: "/tests/checks", Kind: "COLLECTION", Name: "check", QualifiedName: "tests-check"}), true},
		{"explicit type remains static", "dag://?type:Check=stale", types, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "check"}
			require.NoError(t, cmd.ParseFlags([]string{test.uri}))
			expand, err := artifactListHasKeyFilters(cmd, test.dimensions)
			require.NoError(t, err)
			require.Equal(t, test.expand, expand)
		})
	}
}

func TestAbsentCollectionDoesNotExpandListing(t *testing.T) {
	cmd := &cobra.Command{Use: "check"}
	require.NoError(t, cmd.ParseFlags([]string{"dag://?!cell"}))
	expand, err := artifactListHasKeyFilters(cmd, artifact.Dimensions{{Identifier: "Row", Name: "cell", Kind: "COLLECTION"}})
	require.NoError(t, err)
	require.False(t, expand)
}

func TestCollapsedArtifactURIKeepsAbsence(t *testing.T) {
	item := listedArtifact{URI: "dag+check://?row=top&!Row"}
	item.DimensionKeys = []struct{ Dimension, Key string }{
		{"type:Check", "grid/rows/check"}, {"Rows", "top"},
	}
	uri, err := collapsedArtifactURI(item)
	require.NoError(t, err)
	require.Equal(t, "dag+check://?!Row&type:Check=/grid/rows/check&Rows=top", uri)
}

func TestCollapsedArtifactURIKeepsModuleGuard(t *testing.T) {
	item := listedArtifact{URI: "dag+check://foo/check/unit?module=app"}
	item.DimensionKeys = []struct{ Dimension, Key string }{{"type:Check", "foo/check/unit"}}
	uri, err := collapsedArtifactURI(item)
	require.NoError(t, err)
	require.Contains(t, uri, "module=app")
}
