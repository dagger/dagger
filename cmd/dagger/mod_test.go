package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dagger/dagger/util/patternmatcher"
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

func TestPrintRecommendationsUsesInstallAddress(t *testing.T) {
	recs := []recommendation{{
		Module: registryModule{
			Name:        "wolfi",
			Description: "Wolfi Linux base images",
			Repo:        "github.com/dagger/wolfi",
		},
		Match: "apko.yaml",
	}}

	var out bytes.Buffer
	require.NoError(t, printRecommendations(&out, recs))

	got := out.String()
	require.Contains(t, got, "ADDRESS")
	require.Contains(t, got, "DESCRIPTION")
	require.Contains(t, got, "MATCHED")
	require.NotContains(t, got, "NAME")
	require.NotContains(t, got, "REPO")
	require.Contains(t, got, "github.com/dagger/wolfi")
	require.Contains(t, got, "Run 'dagger mod install <ADDRESS>' to install a module.")
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
// embedded registry. At runtime, matching is delegated to the engine via
// Directory.Glob, which uses util/patternmatcher (.dockerignore semantics) —
// the same matcher used here. Validating against it (rather than a
// shell/doublestar matcher) is deliberate: patternmatcher silently treats
// brace expansion ("{a,b}") as literal text, so a pattern like
// "**/{.eslintrc*,eslint.config.*}" would never match anything in the field
// yet pass a doublestar check. This catches that class of bug without an engine.
func TestEmbeddedRegistryRecommendPatternsValid(t *testing.T) {
	mods, err := parseModuleRegistry(embeddedModuleRegistry)
	require.NoError(t, err)
	for _, m := range mods {
		for _, pat := range m.Recommend {
			_, err := patternmatcher.NewPattern(pat)
			require.NoErrorf(t, err, "module %q has invalid recommend pattern %q", m.Name, pat)
			require.NotContainsf(t, pat, "{",
				"module %q recommend pattern %q uses brace expansion, which Directory.Glob (patternmatcher) does not support; split it into separate list entries",
				m.Name, pat)
		}
	}
}

// TestEmbeddedRegistryRecommendsCanonicalConfigFiles is a regression test for
// the brace-expansion bug: it asserts each module is actually recommended for
// the canonical config file(s) it targets, using the same matcher as the
// runtime. Brace-broken patterns (eslint, prettier, pytest) would fail here.
func TestEmbeddedRegistryRecommendsCanonicalConfigFiles(t *testing.T) {
	mods, err := parseModuleRegistry(embeddedModuleRegistry)
	require.NoError(t, err)
	byName := make(map[string]registryModule, len(mods))
	for _, m := range mods {
		byName[m.Name] = m
	}

	want := map[string][]string{
		"go":         {"go.mod", "internal/dagger/go.mod"},
		"eslint":     {".eslintrc.cjs", "eslint.config.js"},
		"prettier":   {".prettierrc.json", "prettier.config.js"},
		"jest":       {"jest.config.ts"},
		"vitest":     {"vitest.config.ts"},
		"pytest":     {"pyproject.toml", "pytest.ini"},
		"biomejs":    {"biome.json"},
		"playwright": {"playwright.config.ts"},
	}

	for name, files := range want {
		m, ok := byName[name]
		require.Truef(t, ok, "module %q missing from embedded registry", name)
		for _, f := range files {
			require.Truef(t, recommendMatchesFile(t, m, f),
				"module %q should be recommended for %q", name, f)
		}
	}
}

// recommendMatchesFile reports whether any of the module's recommend patterns
// matches path, using the same matcher (util/patternmatcher) that
// Directory.Glob applies at runtime.
func recommendMatchesFile(t *testing.T, m registryModule, path string) bool {
	t.Helper()
	for _, pat := range m.Recommend {
		p, err := patternmatcher.NewPattern(pat)
		require.NoErrorf(t, err, "module %q has invalid recommend pattern %q", m.Name, pat)
		match, err := p.Match(strings.TrimPrefix(path, "/"))
		require.NoError(t, err)
		if match {
			return true
		}
	}
	return false
}
