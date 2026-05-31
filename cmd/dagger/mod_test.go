package main

import (
	"testing"

	"github.com/bmatcuk/doublestar/v4"
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

func TestModSubcommandsRegistered(t *testing.T) {
	got := map[string]bool{}
	for _, c := range modCmd.Commands() {
		got[c.Name()] = true
	}
	for _, want := range []string{"install", "uninstall", "list", "search", "recommend"} {
		require.Truef(t, got[want], "expected `dagger module %s` to be registered", want)
	}
}

// TestEmbeddedRegistryRecommendPatternsValid is a static smoke test on the
// embedded registry: every populated recommend string must be a syntactically
// valid glob. The runtime delegates matching to the engine via Directory.Glob,
// so this catches typos in modules.json without requiring an engine.
func TestEmbeddedRegistryRecommendPatternsValid(t *testing.T) {
	mods, err := parseModuleRegistry(embeddedModuleRegistry)
	require.NoError(t, err)
	for _, m := range mods {
		if m.Recommend == "" {
			continue
		}
		_, err := doublestar.Match(m.Recommend, "probe")
		require.NoErrorf(t, err, "module %q has invalid recommend glob %q", m.Name, m.Recommend)
	}
}
