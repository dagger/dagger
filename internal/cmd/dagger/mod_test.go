package daggercmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSearchModuleRegistry(t *testing.T) {
	reg := []registryModule{
		{Name: "wolfi", Description: "Wolfi Linux base images", Repo: "github.com/dagger/wolfi"},
		{Name: "apko", Description: "Build OCI images with apko", Repo: "github.com/example/apko"},
		{Name: "golang", Description: "Go toolchain helpers", Repo: "github.com/example/golang"},
	}

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"empty query returns all sorted by name", "", []string{"apko", "golang", "wolfi"}},
		{"name substring", "wol", []string{"wolfi"}},
		{"case insensitive", "WOL", []string{"wolfi"}},
		{"description match", "images", []string{"apko", "wolfi"}},
		{"no match", "nonexistent", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := searchModuleRegistry(reg, tt.query)
			var names []string
			for _, m := range got {
				names = append(names, m.Name)
			}
			require.Equal(t, tt.want, names)
		})
	}
}

func TestLoadSearchRegistryIncludesSDKsUnlessFiltered(t *testing.T) {
	all, err := loadSearchRegistry(false)
	require.NoError(t, err)
	sdkOnly, err := loadSearchRegistry(true)
	require.NoError(t, err)

	repos := func(entries []registryModule) []string {
		out := make([]string, len(entries))
		for i, entry := range entries {
			out[i] = entry.Repo
		}
		return out
	}
	require.Contains(t, repos(all), "dagger.io/go")
	require.Contains(t, repos(all), "dagger.io/sdk/go")
	require.NotContains(t, repos(sdkOnly), "dagger.io/go")
	require.Contains(t, repos(sdkOnly), "dagger.io/sdk/go")
	for _, repo := range repos(all) {
		require.Regexp(t, `^dagger\.io/`, repo)
	}
}
