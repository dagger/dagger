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

func TestParseModuleRegistry(t *testing.T) {
	data := []byte(`[
		{"name": "go", "description": "Go toolchain", "repo": "github.com/dagger/go"},
		{"name": "pytest", "description": "Run Python tests", "repo": "github.com/dagger/pytest"}
	]`)

	mods, err := parseModuleRegistry(data)
	require.NoError(t, err)
	require.Len(t, mods, 2)
	require.Equal(t, "go", mods[0].Name)
	require.Equal(t, "github.com/dagger/pytest", mods[1].Repo)
}

func TestEmbeddedModuleRegistryParses(t *testing.T) {
	mods, err := parseModuleRegistry(embeddedModuleRegistry)
	require.NoError(t, err)
	require.NotEmpty(t, mods)
}
