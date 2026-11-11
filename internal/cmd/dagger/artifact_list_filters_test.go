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
