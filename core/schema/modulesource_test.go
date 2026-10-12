package schema

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/moby/patternmatcher"
	"github.com/stretchr/testify/require"
)

func TestModuleLocaLSource(t *testing.T) {
	schema := &moduleSourceSchema{}
	ctx := context.Background()

	src := &core.ModuleSource{
		Kind: core.ModuleSourceKindLocal,
		Local: &core.LocalModuleSource{
			ContextDirectoryPath: "/home/user/dagger-test-modules",
		},
	}

	for _, tc := range []struct {
		name           string
		source         *core.ModuleSource
		expectedResult string
		expectError    bool
		fn             func(ctx context.Context, source *core.ModuleSource, args struct{}) (string, error)
	}{
		{
			name:           "Local module source commit",
			source:         src,
			expectedResult: "",
			expectError:    false,
			fn:             schema.moduleSourceCommit,
		},
		{
			name:           "Local module source html repo url",
			source:         src,
			expectedResult: "",
			expectError:    false,
			fn:             schema.moduleSourceHTMLRepoURL,
		},
		{
			name:           "Local module source version",
			source:         src,
			expectedResult: "",
			expectError:    false,
			fn:             schema.moduleSourceVersion,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.fn(ctx, tc.source, struct{}{})
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedResult, result)
			}
		})
	}
}

func TestLegacyWorkspaceFieldHandling(t *testing.T) {
	t.Parallel()

	local := &core.ModuleSource{
		Kind: core.ModuleSourceKindLocal,
		Local: &core.LocalModuleSource{
			ContextDirectoryPath: "/work/repo-b",
		},
		OriginalRefString: ".",
		SourceRootSubpath: ".",
		ConfigBlueprint: &modules.ModuleConfigDependency{
			Name:   "bp",
			Source: "./blueprint",
		},
		ConfigToolchains: []*modules.ModuleConfigDependency{{
			Name:   "go",
			Source: "./toolchains/go",
		}},
	}

	require.True(t, local.UsesLegacyWorkspaceFields())
	require.Equal(t, []string{"blueprint", "toolchains"}, local.LegacyWorkspaceFieldNames())

	stripped := local.StripLegacyWorkspaceFields()
	require.Nil(t, stripped.ConfigBlueprint)
	require.Nil(t, stripped.ConfigToolchains)
	require.False(t, stripped.UsesLegacyWorkspaceFields())

	directErr := local.DirectLegacyWorkspaceLoadError()
	require.EqualError(t,
		directErr,
		"This module's dagger.json uses toolchains or blueprints, which have moved to workspaces.\n\nTry: dagger -W .\n\nTo learn more: https://docs.dagger.io/reference/upgrade-to-workspaces",
	)
	var ext dagql.ExtendedError
	require.True(t, errors.As(directErr, &ext))
	require.Equal(t, map[string]any{
		"_quiet":    true,
		"_message":  "This module's dagger.json uses toolchains or blueprints, which have moved to workspaces.\n\nTry: dagger -W .\n\nTo learn more: https://docs.dagger.io/reference/upgrade-to-workspaces",
		"_exitCode": 1,
	}, ext.Extensions())
	require.EqualError(t,
		local.NestedLegacyWorkspaceLoadError(),
		"workspace module source \"/work/repo-b\" points at a legacy workspace, not a plain module: its dagger.json uses legacy workspace fields \"blueprint, toolchains\"\n\nrun `dagger workspace migrate` in \"/work/repo-b\", then update this source to point at one of the migrated modules under \"/work/repo-b/.dagger/modules\"",
	)

	remote := &core.ModuleSource{
		Kind: core.ModuleSourceKindGit,
		Git: &core.GitModuleSource{
			CloneRef: "https://github.com/acme/repo-b",
			Version:  "main",
		},
		SourceRootSubpath: ".",
		ConfigBlueprint: &modules.ModuleConfigDependency{
			Name:   "bp",
			Source: "./blueprint",
		},
	}

	require.EqualError(t,
		remote.NestedLegacyWorkspaceLoadError(),
		"workspace module source \"https://github.com/acme/repo-b@main\" points at a legacy workspace, not a plain module: its dagger.json uses legacy workspace fields \"blueprint\"\n\nuse a migrated ref that points at one of its real modules. If you control that repo, migrate it first",
	)
}

func TestLoadCurrentModuleSourceConfigPreservesGitDependencySourceAndPin(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dag, err := dagql.NewServer(ctx, &core.Query{})
	require.NoError(t, err)
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.ModuleSource]{Typed: &core.ModuleSource{}}))

	dep := &core.ModuleSource{
		Kind:              core.ModuleSourceKindGit,
		ModuleName:        "dep",
		OriginalRefString: "https://github.com/acme/dep/sdk",
		SourceRootSubpath: "sdk",
		Git: &core.GitModuleSource{
			CloneRef:     "https://github.com/acme/dep",
			Version:      "v1.2.3",
			VersionQuery: "v1.2",
			Commit:       "1234567890abcdef",
		},
	}
	parent := &core.ModuleSource{
		Kind:               core.ModuleSourceKindLocal,
		ConfigFilename:     modules.Filename,
		ModuleOriginalName: "parent",
		EngineVersion:      engine.Version,
		SourceRootSubpath:  ".",
		Local: &core.LocalModuleSource{
			ContextDirectoryPath: "/work/parent",
		},
		Dependencies: dagql.ObjectResultArray[*core.ModuleSource]{
			moduleSourceObjectResult(t, dag, "dep", dep),
		},
	}

	cfg, err := (&moduleSourceSchema{}).loadModuleSourceConfig(parent)
	require.NoError(t, err)
	require.Len(t, cfg.Dependencies, 1)
	require.Equal(t, "1234567890abcdef", cfg.Dependencies[0].Pin)

	out, err := modules.MarshalModuleConfigForFilename(cfg, modules.Filename)
	require.NoError(t, err)
	require.Contains(t, string(out), `name = "parent"`)
	require.Contains(t, string(out), `engineVersion = "`+engine.Version+`"`)
	require.Contains(t, string(out), `name = "dep"`)
	require.Contains(t, string(out), `source = "https://github.com/acme/dep/sdk@v1.2"`)
	require.Contains(t, string(out), `pin = "1234567890abcdef"`)
	require.NotContains(t, string(out), `version =`)
}

// A manifest version 2 module declares no engine version, so there is no
// declared version to check against the running engine. currentModule.source
// loads the config this way from inside a module.
func TestLoadModuleSourceConfigAcceptsEntrypointManifest(t *testing.T) {
	t.Parallel()

	entrypoint := &modules.ModuleEntrypointConfig{
		Kind:   modules.ModuleEntrypointKindDang,
		Source: "./entrypoint",
	}
	src := &core.ModuleSource{
		Kind:               core.ModuleSourceKindLocal,
		ConfigFilename:     modules.Filename,
		ModuleOriginalName: "tiny",
		EngineVersion:      engine.Version,
		SourceRootSubpath:  ".",
		Entrypoint:         entrypoint,
		Local: &core.LocalModuleSource{
			ContextDirectoryPath: "/work/tiny",
		},
	}

	cfg, err := (&moduleSourceSchema{}).loadModuleSourceConfig(src)
	require.NoError(t, err)
	require.Equal(t, "tiny", cfg.Name)
	require.Equal(t, entrypoint, cfg.Entrypoint)
	require.Empty(t, cfg.EngineVersion)
}

// A manifest version 2 rewrite writes only the name and the entrypoint, so
// patterns that the entrypoint's resources member added to IncludePaths never
// reach dagger-module.toml.
func TestLoadModuleSourceConfigOmitsEntrypointResources(t *testing.T) {
	t.Parallel()

	src := &core.ModuleSource{
		Kind:                      core.ModuleSourceKindLocal,
		ConfigFilename:            modules.Filename,
		ModuleOriginalName:        "tiny",
		EngineVersion:             engine.Version,
		SourceRootSubpath:         "tiny",
		IncludePaths:              []string{"../shared", "!../shared/secret"},
		RebasedIncludePaths:       []string{"shared", "!shared/secret"},
		EntrypointResourcesLoaded: true,
		Entrypoint: &modules.ModuleEntrypointConfig{
			Kind:   modules.ModuleEntrypointKindDang,
			Source: "./entrypoint",
		},
		Local: &core.LocalModuleSource{
			ContextDirectoryPath: "/work",
		},
	}

	cfg, err := (&moduleSourceSchema{}).loadModuleSourceConfig(src)
	require.NoError(t, err)
	out, err := modules.MarshalModuleConfigForFilename(cfg, modules.Filename)
	require.NoError(t, err)
	require.NotContains(t, string(out), "include")
	require.NotContains(t, string(out), "shared")
	reloaded, err := modules.ParseModuleConfigForFilename(out, modules.Filename)
	require.NoError(t, err)
	require.Equal(t, src.Entrypoint, reloaded.Entrypoint)
	require.Empty(t, reloaded.Include)
}

// A leading / does not anchor an include pattern: filepath.Join drops it, as in
// the previous format, so the pattern stays relative to the source root and
// the context-root check still applies.
func TestRebasePatternsAbsoluteLooking(t *testing.T) {
	t.Parallel()

	rebased, err := rebasePatterns([]string{"/shared", "!/shared", "/../outside"}, "m")
	require.NoError(t, err)
	require.Equal(t, []string{"m/shared", "!m/shared", "outside"}, rebased)

	_, err = rebasePatterns([]string{"/../../outside"}, "m")
	require.ErrorContains(t, err, `include/exclude path "../outside" escapes context`)
}

func TestRebaseEntrypointResources(t *testing.T) {
	t.Parallel()

	module := func(kind core.ModuleSourceKind, rootSubpath string) *core.ModuleSource {
		return &core.ModuleSource{Kind: kind, SourceRootSubpath: rootSubpath}
	}

	rebased, err := rebaseEntrypointResources(module(core.ModuleSourceKindLocal, "app"),
		[]string{"../lib/go.mod", "../lib/greet", "!../lib/greet/testdata", "!data", "../**/*.txt"})
	require.NoError(t, err)
	require.Equal(t, []string{"lib/go.mod", "lib/greet", "!lib/greet/testdata", "!app/data", "**/*.txt"}, rebased)

	for _, tc := range []struct {
		name     string
		src      *core.ModuleSource
		patterns []string
		err      string
	}{
		{"empty", module(core.ModuleSourceKindLocal, "app"), []string{""}, `pattern "" is empty`},
		{"bare exclusion", module(core.ModuleSourceKindLocal, "app"), []string{"!"}, `pattern "!" is empty`},
		{"double exclusion", module(core.ModuleSourceKindLocal, "app"), []string{"!!lib"}, `pattern "!!lib" has more than one leading exclamation mark`},
		{"absolute", module(core.ModuleSourceKindLocal, "app"), []string{"/lib"}, `pattern "/lib" is absolute`},
		{"absolute exclusion", module(core.ModuleSourceKindLocal, "app"), []string{"!/lib"}, `pattern "!/lib" is absolute`},
		{"whole context root", module(core.ModuleSourceKindLocal, "app"), []string{".."}, `pattern ".." names the whole context root`},
		{"escape", module(core.ModuleSourceKindGit, "app"), []string{"../../outside"}, `pattern "../../outside": include/exclude path "../outside" escapes context`},
		{"escape without .git", module(core.ModuleSourceKindLocal, "."), []string{"../lib"}, `no .git directory was found above the module`},
		{"too many", module(core.ModuleSourceKindLocal, "app"), make([]string, maxEntrypointResources+1), "exceed the limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := rebaseEntrypointResources(tc.src, tc.patterns)
			require.ErrorContains(t, err, tc.err)
		})
	}
}

// The context filter decides inclusion two ways: a walk that passes each
// directory's result to its children, and a direct check of one path. Both
// must keep repository metadata out of a context that resources widen.
func TestEntrypointResourcesGitExclusion(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		filter   []string
		included []string
		excluded []string
	}{
		{
			name:     "nested module",
			filter:   []string{"app/dagger-module.toml", "app", "**"},
			included: []string{"app/main.go", "lib/go.mod", ".gitignore", ".gitattributes", ".github/workflows/ci.yml"},
			excluded: []string{".git", ".git/config", "app/.git", "app/.git/HEAD", "lib/.git", "vendor/x/.git/config"},
		},
		{
			name:     "root module",
			filter:   []string{"dagger-module.toml", "*", "../lib"},
			included: []string{"main.go", ".gitignore"},
			excluded: []string{".git", ".git/config"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pm, err := patternmatcher.New(append(tc.filter, entrypointResourcesGitExclusion...))
			require.NoError(t, err)
			for _, path := range tc.included {
				require.True(t, matchesOpen(t, pm, path), "open %s", path)
				require.True(t, matchesWalk(t, pm, path), "walk %s", path)
			}
			for _, path := range tc.excluded {
				require.False(t, matchesOpen(t, pm, path), "open %s", path)
				require.False(t, matchesWalk(t, pm, path), "walk %s", path)
			}
		})
	}
}

func matchesOpen(t *testing.T, pm *patternmatcher.PatternMatcher, path string) bool {
	t.Helper()
	matched, err := pm.MatchesOrParentMatches(path)
	require.NoError(t, err)
	return matched
}

func matchesWalk(t *testing.T, pm *patternmatcher.PatternMatcher, path string) bool {
	t.Helper()
	var (
		matched bool
		info    patternmatcher.MatchInfo
		err     error
	)
	parts := strings.Split(path, "/")
	for i := range parts {
		matched, info, err = pm.MatchesUsingParentResults(strings.Join(parts[:i+1], "/"), info)
		require.NoError(t, err, fmt.Sprintf("walk %s", path))
	}
	return matched
}

func moduleSourceObjectResult(t *testing.T, dag *dagql.Server, op string, self *core.ModuleSource) dagql.ObjectResult[*core.ModuleSource] {
	t.Helper()

	res, err := dagql.NewObjectResultForCall(self, dag, &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: "module-source-" + op,
		Type:        dagql.NewResultCallType(self.Type()),
	})
	require.NoError(t, err)
	return res
}
