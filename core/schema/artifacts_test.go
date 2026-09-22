package schema

import (
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/stretchr/testify/require"
)

func TestArtifactDirectiveFilterDoesNotRequireWorkspace(t *testing.T) {
	// Raw metadata filters must work without workspace settings or valid
	// command result types. Command filters enforce those rules separately.
	check := &core.Artifact{Path: []string{"check"}, TypeName: "Container", Directives: []string{"check"}}
	other := &core.Artifact{Path: []string{"other"}, TypeName: "Check"}
	all := &core.Artifacts{Entries: []*core.Artifact{check, other}}
	schema := &artifactsSchema{}
	included, err := schema.filterDirectives(t.Context(), all, artifactDirectiveFilterArgs{Directives: []string{"check"}})
	require.NoError(t, err)
	require.Equal(t, []*core.Artifact{check}, included.Entries)
	excluded, err := schema.filterDirectives(t.Context(), all, artifactDirectiveFilterArgs{Directives: []string{"check"}, Exclude: true})
	require.NoError(t, err)
	require.Equal(t, []*core.Artifact{other}, excluded.Entries)
}
