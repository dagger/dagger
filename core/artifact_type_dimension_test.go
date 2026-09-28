package core

import (
	"testing"

	"github.com/dagger/dagger/core/dagaddress"
	"github.com/stretchr/testify/require"
)

func TestArtifactModuleSelectionBeforeExpansion(t *testing.T) {
	all := &Artifacts{Entries: []*Artifact{
		{ModuleName: "go", Path: []string{"test"}, TypeName: "Check"},
		{ModuleName: "playwright", Path: []string{"playwright", "test"}, TypeName: "Check"},
		{ModuleName: "go-tests", Path: []string{"go-tests", "test"}, TypeName: "Check"},
	}}
	selected, err := all.FilterDimensionKeys("module", []string{"go", "playwright"}).Expand(t.Context())
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
