package schema

import (
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestArtifactAbsentDimensionArgumentViews(t *testing.T) {
	for _, version := range []string{"v1.0.0-beta.25", artifactDimensionViewVersion} {
		view := call.View(engine.APIViewVersion(version))
		_, srv := newNestingTestServer(t, view)
		field := srv.SchemaForView(view).Types["Artifacts"].Fields.ForName("filterDimensions")
		require.NotNil(t, field)
		require.NotNil(t, field.Arguments.ForName("dimensions"))
		arg := field.Arguments.ForName("exclude")
		if version == artifactDimensionViewVersion {
			require.NotNil(t, arg)
			require.Equal(t, "false", arg.DefaultValue.Raw)
		} else {
			require.Nil(t, arg)
		}
	}
}

func TestArtifactAbsentDimensionResolver(t *testing.T) {
	s := &artifactsSchema{}
	all := &core.Artifacts{Entries: []*core.Artifact{{ModuleName: "app", Path: []string{"app", "build"}, TypeName: "Container"}}}
	selected, err := s.filterDimensions(t.Context(), all, struct {
		Dimensions []string
		Exclude    bool `default:"false"`
	}{Dimensions: []string{"module"}, Exclude: true})
	require.NoError(t, err)
	selected, err = selected.SchemaSelection()
	require.NoError(t, err)
	require.Empty(t, selected.Entries)
}
