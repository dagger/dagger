package main

import (
	"os"
	"path/filepath"
	"sort"
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
	for _, want := range []string{"install", "uninstall", "list", "search", "recommended"} {
		require.Truef(t, got[want], "expected `dagger mod %s` to be registered", want)
	}
}

func TestRecommendModules(t *testing.T) {
	reg := []registryModule{
		{Name: "go", Repo: "github.com/dagger/go", Recommend: "**/go.mod"},
		{Name: "eslint", Repo: "github.com/dagger/eslint", Recommend: "**/.eslintrc*"},
		{Name: "prettier", Repo: "github.com/dagger/prettier", Recommend: "**/.prettierrc*"},
		// no Recommend: must never appear
		{Name: "naked", Repo: "github.com/dagger/naked"},
	}

	t.Run("matches on present files and sorts by name", func(t *testing.T) {
		files := []string{"a/.eslintrc.json", "service/go.mod", "src/main.go"}
		got := recommendModules(reg, files, nil)

		var names, matches []string
		for _, r := range got {
			names = append(names, r.Module.Name)
			matches = append(matches, r.Match)
		}
		require.Equal(t, []string{"eslint", "go"}, names)
		require.Equal(t, []string{"a/.eslintrc.json", "service/go.mod"}, matches)
	})

	t.Run("installed modules are excluded", func(t *testing.T) {
		files := []string{"go.mod"}
		got := recommendModules(reg, files, map[string]bool{"go": true})
		require.Empty(t, got)
	})

	t.Run("missing recommend never matches", func(t *testing.T) {
		// Any file present; the "naked" entry must still be skipped.
		files := []string{"anything"}
		got := recommendModules(reg, files, nil)
		for _, r := range got {
			require.NotEqual(t, "naked", r.Module.Name)
		}
	})

	t.Run("first matching file wins", func(t *testing.T) {
		// Input is sorted by collectWorkspaceFiles, so first match is
		// the lexicographically smallest path that satisfies the glob.
		files := []string{"a/go.mod", "z/go.mod"}
		got := recommendModules(reg, files, nil)
		require.Len(t, got, 1)
		require.Equal(t, "a/go.mod", got[0].Match)
	})

	t.Run("invalid glob is skipped, others proceed", func(t *testing.T) {
		bad := []registryModule{
			{Name: "broken", Repo: "x", Recommend: "[unterminated"},
			{Name: "go", Repo: "github.com/dagger/go", Recommend: "**/go.mod"},
		}
		got := recommendModules(bad, []string{"go.mod"}, nil)
		require.Len(t, got, 1)
		require.Equal(t, "go", got[0].Module.Name)
	})
}

func TestCollectWorkspaceFiles(t *testing.T) {
	root := t.TempDir()

	mustWrite := func(rel string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, nil, 0o644))
	}

	// Included files at varying depths.
	mustWrite("go.mod")
	mustWrite("svc/api/go.mod")
	mustWrite("web/.eslintrc.json")

	// Files under denylisted directories must be pruned.
	mustWrite(".git/HEAD")
	mustWrite("node_modules/foo/package.json")
	mustWrite("vendor/example.com/pkg/file.go")
	mustWrite(".dagger/config.toml")

	files, err := collectWorkspaceFiles(root)
	require.NoError(t, err)

	require.Equal(t, []string{
		"go.mod",
		"svc/api/go.mod",
		"web/.eslintrc.json",
	}, files)
	require.True(t, sort.StringsAreSorted(files))
}

func TestEmbeddedRegistryRecommendPatternsValid(t *testing.T) {
	mods, err := parseModuleRegistry(embeddedModuleRegistry)
	require.NoError(t, err)
	for _, m := range mods {
		if m.Recommend == "" {
			continue
		}
		// doublestar.Match validates the pattern even on a probe string.
		_, err := doublestar.Match(m.Recommend, "probe")
		require.NoErrorf(t, err, "module %q has invalid recommend glob %q", m.Name, m.Recommend)
	}
}
