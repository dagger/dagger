package schema

import (
	"fmt"
	"testing"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

func testModuleMigrationPlanner(t *testing.T, files map[string]string) *moduleMigrationPlanner {
	t.Helper()
	p := &moduleMigrationPlanner{
		configPath: "dagger.toml", config: &workspace.Config{},
		files: map[string]bool{}, visited: map[string]bool{}, active: map[string]bool{},
		writes: map[string][]byte{}, removed: map[string]bool{},
		readFile: func(file string) ([]byte, error) {
			data, ok := files[file]
			if !ok {
				return nil, fmt.Errorf("missing file %s", file)
			}
			return []byte(data), nil
		},
	}
	for file := range files {
		p.files[file] = true
	}
	return p
}

func TestModuleMigrationGraph(t *testing.T) {
	p := testModuleMigrationPlanner(t, map[string]string{
		"a/dagger.json":        `{"name":"a","sdk":"github.com/acme/sdk@v1","dependencies":[{"name":"b","source":"../b"},{"name":"c","source":"../c"}]}`,
		"b/dagger.json":        `{"name":"b","sdk":"github.com/acme/sdk@v1","dependencies":[{"name":"c","source":"../c"}]}`,
		"c/dagger.json":        `{"name":"c","sdk":"github.com/acme/sdk@v1","dependencies":[{"name":"a","source":"../a"}]}`,
		"fixtures/dagger.json": `{"name":"fixture","sdk":"go"}`,
	})
	require.NoError(t, p.module("a", false, true))
	require.Len(t, p.writes, 3)
	require.Len(t, p.removed, 3)
	require.Len(t, p.config.Modules, 1)
	require.Len(t, p.config.SDKs, 1)
	require.Contains(t, p.warnings[0], "cycle includes a")
	require.Equal(t, []string{"fixtures"}, p.optionalCandidates())
	require.NotContains(t, p.removed, "fixtures/dagger.json")
}

func TestStandaloneModuleMigrationKeepsDependenciesAndNativeWorkspace(t *testing.T) {
	p := testModuleMigrationPlanner(t, map[string]string{
		"app/dagger.json": `{"name":"app","sdk":"github.com/acme/sdk@v1","dependencies":[{"name":"dep","source":"../dep"}]}`,
		"dep/dagger.json": `{"name":"dep","sdk":"go"}`,
	})
	p.config.Modules = map[string]workspace.ModuleEntry{"existing": {Source: "github.com/acme/existing@v2"}}
	require.NoError(t, p.module("app", true, false))
	require.Len(t, p.writes, 1)
	require.NotContains(t, p.removed, "dep/dagger.json")
	require.Equal(t, "github.com/acme/existing@v2", p.config.Modules["existing"].Source)
	for _, sdk := range p.config.SDKs {
		require.Equal(t, []string{"./dep"}, sdk.Scopes["app"].Clients)
	}
}

func TestStandaloneModuleMigrationWithoutWorkspace(t *testing.T) {
	p := testModuleMigrationPlanner(t, map[string]string{
		"dagger.json":         `{"toolchains":[{"name":"app","source":"./app"}]}`,
		"app/dagger.json":     `{"name":"app","sdk":"github.com/acme/sdk@v1","dependencies":[{"name":"dep","source":"../dep"}]}`,
		"dep/dagger.json":     `{"name":"dep","sdk":"go"}`,
		"fixture/dagger.json": `{"name":"fixture","sdk":"go"}`,
	})
	p.configPath = ""
	p.config = nil
	require.NoError(t, p.module("app", true, false))
	require.Nil(t, p.config)
	require.Len(t, p.writes, 1)
	require.Len(t, p.removed, 1)
	require.Contains(t, p.writes, "app/dagger-module.toml")
	require.Contains(t, p.removed, "app/dagger.json")
	require.NotContains(t, p.removed, "dagger.json")
	require.NotContains(t, p.removed, "dep/dagger.json")
	require.NotContains(t, p.removed, "fixture/dagger.json")
}

func TestModuleMigrationNativeDependenciesWithoutWorkspace(t *testing.T) {
	config, err := workspace.ParseLegacyModuleConfigTolerant([]byte(`{"name":"app","sdk":"go","dependencies":[{"name":"dep","source":"../dep"}]}`))
	require.NoError(t, err)
	native, err := workspace.PlanModuleMigration(config, false)
	require.NoError(t, err)
	p := testModuleMigrationPlanner(t, map[string]string{
		"app/dagger-module.toml": string(native.ConfigData),
		"dep/dagger.json":        `{"name":"dep","sdk":"go"}`,
	})
	p.configPath = ""
	p.config = nil
	require.NoError(t, p.requiredModules([]string{"app"}))
	require.Nil(t, p.config)
	require.Len(t, p.writes, 1)
	require.Contains(t, p.writes, "dep/dagger-module.toml")
	require.NotContains(t, p.writes, "app/dagger-module.toml")
}

func TestModuleMigrationConflictsPreserveFiles(t *testing.T) {
	p := testModuleMigrationPlanner(t, map[string]string{
		"app/dagger.json":        `{"name":"app","sdk":"go"}`,
		"app/dagger-module.toml": "name = 'other'\nruntime = 'go'\n",
	})
	require.ErrorContains(t, p.module("app", false, true), "both")
	require.Empty(t, p.writes)
	require.Empty(t, p.removed)
}

func TestModuleMigrationStopsAtNestedWorkspace(t *testing.T) {
	p := testModuleMigrationPlanner(t, map[string]string{
		"nested/dagger.toml":     "",
		"nested/app/dagger.json": `{"name":"app","sdk":"go"}`,
	})
	require.ErrorContains(t, p.module("nested/app", false, true), "another workspace")
	require.Empty(t, p.writes)
	require.Empty(t, p.optionalCandidates())
	require.Empty(t, p.warnings)
}

func TestModuleMigrationKeepsNativeWorkspaceBoundary(t *testing.T) {
	p := testModuleMigrationPlanner(t, map[string]string{
		"nested/dagger.toml":            "",
		"nested/app/dagger-module.toml": "name = 'app'\nruntime = 'go'\n",
	})
	require.NoError(t, p.module("nested/app", false, true))
	require.Empty(t, p.writes)
	require.Empty(t, p.config.SDKs)
}

func TestModuleMigrationRequiredFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		app  string
		dep  string
		want string
	}{
		{"unsupported SDK", `{"name":"app","sdk":{"source":"go","debug":true}}`, "", "deprecated SDK settings"},
		{"unsupported dependency", `{"name":"app","sdk":"go","dependencies":[{"name":"dep","source":"../dep"}]}`, `{"name":"dep","sdk":{"source":"go","debug":true}}`, `required dependency "dep" (../dep) of module app: migrate dep/dagger.json`},
		{"missing dependency", `{"name":"app","sdk":"go","dependencies":[{"name":"dep","source":"../dep"}]}`, "", "no module configuration found at dep"},
		{"absolute dependency", `{"name":"app","sdk":"go","dependencies":[{"name":"dep","source":"/dep"}]}`, "", "absolute source is outside migration scope"},
		{"escaping dependency", `{"name":"app","sdk":"go","dependencies":[{"name":"dep","source":"../../dep"}]}`, "", "escapes the workspace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"app/dagger.json": tc.app}
			if tc.dep != "" {
				files["dep/dagger.json"] = tc.dep
			}
			p := testModuleMigrationPlanner(t, files)
			require.ErrorContains(t, p.module("app", false, true), tc.want)
			require.Empty(t, p.writes)
			require.Empty(t, p.removed)
			require.Empty(t, p.warnings)
		})
	}
	_, err := migrationLocalRefPath(".", "")
	require.ErrorContains(t, err, "source is empty")
	_, err = migrationLocalRefPath(".", "/outside")
	require.ErrorContains(t, err, "absolute source")
}

func TestOptionalModuleCandidates(t *testing.T) {
	planner := &moduleMigrationPlanner{
		configPath: "app/dagger.toml",
		files: map[string]bool{
			"app/installed/dagger.json":      true,
			"app/fixture/dagger.json":        true,
			"app/current/dagger.json":        true,
			"app/current/dagger-module.toml": true,
			"sibling/dagger.json":            true,
		},
		visited: map[string]bool{"app/installed": true},
	}
	require.Equal(t, []string{"app/fixture"}, planner.optionalCandidates())
}

func TestMigrationModulePath(t *testing.T) {
	for _, tc := range []struct{ cwd, target, want string }{
		{"/", "app", "app"},
		{"/app", ".", "app"},
		{"app", ".", "app"},
		{"app", "../common", "common"},
		{"app", "/common", "common"},
	} {
		got, err := migrationModulePath(tc.cwd, tc.target)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
	_, err := migrationModulePath(".", "../outside")
	require.ErrorContains(t, err, "escapes")
}

func TestMigrationScopeRejectsUnnormalizedPaths(t *testing.T) {
	p := testModuleMigrationPlanner(t, nil)
	for _, dir := range []string{"/", "/app", "..", "../app"} {
		require.False(t, p.inScope(dir), dir)
	}
}

func TestMigrationSDKUpdatesOnlyChangedConfigs(t *testing.T) {
	files := map[string]string{
		"dagger.toml":           "[modules.dang]\nsource = 'dang'\n[sdks.dang]\nmodule = 'dang'\n[sdks.dang.scopes.app]\nmodule = true\n",
		"parent/dagger.toml":    "[modules.go]\nsource = 'go'\n[sdks.go]\nmodule = 'go'\n[sdks.go.scopes.lib]\nmodule = true\n",
		"unrelated/dagger.toml": "not even valid TOML",
	}
	updates, err := migratedConfigSDKUpdates([]string{"dagger.toml", "parent/dagger.toml"}, func(file string) ([]byte, error) {
		return []byte(files[file]), nil
	})
	require.NoError(t, err)
	require.Len(t, updates, 2)
	require.NotContains(t, updates, "unrelated/dagger.toml")
	for _, file := range []string{"dagger.toml", "parent/dagger.toml"} {
		cfg, err := workspace.ParseConfig(updates[file])
		require.NoError(t, err)
		for _, entry := range cfg.Modules {
			require.Contains(t, entry.Source, "github.com/dagger/")
		}
	}
}

func TestMigrationNoopPreservesConfigBytes(t *testing.T) {
	for _, original := range []string{"", "\n", "# keep this comment\n\n", "[modules.app]\nsource = './app'\n\n"} {
		cfg, err := workspace.ParseConfig([]byte(original))
		require.NoError(t, err)
		got, err := migrationConfigBytes([]byte(original), cfg)
		require.NoError(t, err)
		require.Equal(t, original, string(got))
	}
}

func TestMigrationConfigSelectionUsesOwnership(t *testing.T) {
	for _, configs := range [][]string{
		{"sibling/dagger.toml", "dagger.toml", "app/dagger.toml"},
		{"app/dagger.toml", "sibling/dagger.toml", "dagger.toml"},
	} {
		owner, err := owningMigrationConfig(configs, "/app/child")
		require.NoError(t, err)
		require.Equal(t, "app/dagger.toml", owner)
		owner, err = owningMigrationConfig(configs, "/other")
		require.NoError(t, err)
		require.Equal(t, "dagger.toml", owner)
	}
	_, err := owningMigrationConfig([]string{"sibling/dagger.toml"}, "/app")
	require.ErrorContains(t, err, "owns app")
	owner, err := owningMigrationConfig([]string{"dagger.toml", "a/dagger.toml"}, "/a")
	require.NoError(t, err)
	require.Equal(t, "a/dagger.toml", owner)
}

func TestModuleMigrationCleanupFailurePreservesPlan(t *testing.T) {
	p := testModuleMigrationPlanner(t, map[string]string{
		"app/dagger.json": `{"name":"app","sdk":"go"}`,
	})
	p.cleanupModule = func(dir string, cfg *modules.ModuleConfig) error {
		require.Equal(t, "app", dir)
		require.Equal(t, "go", cfg.SDK.Source)
		return fmt.Errorf("SDK code generation failed")
	}
	require.ErrorContains(t, p.module("app", true, false), "clean legacy .gitignore for module app")
	require.Empty(t, p.writes)
	require.Empty(t, p.removed)
	require.True(t, p.files["app/dagger.json"])
}

func TestModuleMigrationCleansEveryConvertedModule(t *testing.T) {
	p := testModuleMigrationPlanner(t, map[string]string{
		"app/dagger.json": `{"name":"app","sdk":"go","dependencies":[{"name":"dep","source":"../dep"}]}`,
		"dep/dagger.json": `{"name":"dep","sdk":"go"}`,
	})
	var cleaned []string
	p.cleanupModule = func(dir string, cfg *modules.ModuleConfig) error {
		cleaned = append(cleaned, dir)
		return nil
	}
	require.NoError(t, p.module("app", false, true))
	require.Equal(t, []string{"dep", "app"}, cleaned)
	require.NoError(t, p.module("app", true, false))
	require.Len(t, cleaned, 2)
}
