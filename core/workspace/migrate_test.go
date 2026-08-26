package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/core/modules"
	"github.com/stretchr/testify/require"
)

func TestPlanMigrationPreservesLegacyWorkspacePinsInSources(t *testing.T) {
	t.Parallel()

	plan := testMigrationPlan(t, "repo", `{
  "name": "myapp",
  "toolchains": [
    {"name": "toolchain", "source": "github.com/acme/toolchain@main", "pin": "1111111"}
  ],
	  "blueprint": {"name": "blueprint", "source": "github.com/acme/blueprint@main", "pin": "2222222"}
	}`)

	configData := string(plan.WorkspaceConfigData)
	require.Contains(t, configData, `source = "github.com/acme/toolchain@1111111"`)
	require.Contains(t, configData, `source = "github.com/acme/blueprint@2222222"`)
}

func TestPlanMigrationPreservesDependencyPinsInModuleConfig(t *testing.T) {
	t.Parallel()

	plan := testMigrationPlan(t, "repo", `{
  "name": "myapp",
  "sdk": {"source": "go"},
  "source": "src",
  "dependencies": [
    {"name": "dep", "source": "github.com/acme/dep@main", "pin": "sha256:abc"}
  ]
}`)

	require.Equal(t, ModuleConfigFileName, plan.MigratedModuleConfigPath,
		"the module config replaces dagger.json at the same location")
	cfg, err := modules.ParseModuleConfigForFilename(plan.MigratedModuleConfigData, ModuleConfigFileName)
	require.NoError(t, err)
	require.Equal(t, "myapp", cfg.Name)
	require.Equal(t, "go", cfg.SDK.Source)
	require.Equal(t, "src", cfg.Source, "the source path is preserved as-is")
	require.Len(t, cfg.Dependencies, 1)
	require.Equal(t, "dep", cfg.Dependencies[0].Name)
	require.Equal(t, "github.com/acme/dep@main", cfg.Dependencies[0].Source)
	require.Equal(t, "sha256:abc", cfg.Dependencies[0].Pin)
	require.NotContains(t, string(plan.MigratedModuleConfigData), "sdk")
}

func TestPlanMigrationKeepsUnpinnedWorkspaceSourcesSymbolic(t *testing.T) {
	t.Parallel()

	plan := testMigrationPlan(t, "repo", `{
  "name": "myapp",
  "toolchains": [
    {"name": "toolchain", "source": "github.com/acme/toolchain@main"}
	  ]
	}`)

	require.Contains(t, string(plan.WorkspaceConfigData), `source = "github.com/acme/toolchain@main"`)
}

func TestPlanMigrationWritesMigrationReportForGaps(t *testing.T) {
	t.Parallel()

	plan := testMigrationPlan(t, "repo", `{
  "name": "myapp",
  "toolchains": [
    {
      "name": "toolchain",
      "source": "./toolchain",
      "customizations": [
        {
          "argument": "src",
          "defaultPath": "./custom-config.txt",
          "ignore": ["node_modules"]
        },
        {
          "function": ["build"],
          "argument": "tag",
          "default": "dev"
        }
      ]
    }
  ]
}`)

	require.Len(t, plan.Warnings, 2)
	require.Equal(t, filepath.Join(LockDirName, "migration-report.md"), plan.MigrationReportPath)

	configData := string(plan.WorkspaceConfigData)
	require.NotContains(t, configData, "# WARNING:")
	require.NotContains(t, configData, "# Original:")

	reportData := string(plan.MigrationReportData)
	require.Contains(t, reportData, "# Migration Report")
	require.Contains(t, reportData, "`toolchain` needs a manual check")
	require.Contains(t, reportData, "ACTION: Review each item below")
	require.Contains(t, reportData, `"defaultPath": "./custom-config.txt"`)
	require.Contains(t, reportData, `"function": [`)
}

// TestPlanMigrationConvertsModuleConfigInPlace verifies the migrated
// dagger-module.toml replaces dagger.json at the exact same location, with the
// source path preserved as-is: other repos may install this module by path,
// and that path must keep resolving to the module config file. Only a module
// whose source lives in a subdirectory (a project-style layout) is installed
// into the workspace with an entrypoint.
func TestPlanMigrationConvertsModuleConfigInPlace(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		source     string
		wantSource string
		installed  bool
	}{
		{name: "empty", source: "", wantSource: "."},
		{name: "root", source: ".", wantSource: "."},
		{name: "subdir", source: "ci", wantSource: "ci", installed: true},
		{name: "nested subdir", source: "src/mod", wantSource: "src/mod", installed: true},
		{name: "dot dagger", source: ".dagger", wantSource: ".dagger", installed: true},
		{name: "clean dot dagger", source: "./.dagger/", wantSource: ".dagger", installed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			plan := testMigrationPlan(t, "repo", fmt.Sprintf(`{
  "name": "myapp",
  "sdk": {"source": "go"},
  "source": %q,
  "toolchains": [
    {"name": "toolchain", "source": "github.com/acme/toolchain@main"}
  ]
}`, tc.source))

			require.Equal(t, ModuleConfigFileName, plan.MigratedModuleConfigPath,
				"the module config replaces dagger.json at the same location")
			cfg, err := modules.ParseModuleConfigForFilename(plan.MigratedModuleConfigData, ModuleConfigFileName)
			require.NoError(t, err)
			require.Equal(t, "myapp", cfg.Name)
			// Parsing normalizes an omitted source to ".".
			require.Equal(t, tc.wantSource, cfg.Source)

			wsCfg, err := ParseConfig(plan.WorkspaceConfigData)
			require.NoError(t, err)
			entry, installed := wsCfg.Modules["myapp"]
			require.Equal(t, tc.installed, installed,
				"a module whose source is the project root is the repo itself and must not be installed")
			if tc.installed {
				require.Equal(t, ".", entry.Source,
					"the entry points at the module config's directory")
				require.True(t, entry.Entrypoint)
			}
		})
	}
}

// TestPlanMigrationModuleRepoSkipsSDKInstall verifies the "repo is just a
// dagger module" shape (source at the project root) records no as-sdk install
// for the root module: the runtime pin is a plain install added by the
// migration caller, and the module gets no entrypoint.
func TestPlanMigrationModuleRepoSkipsSDKInstall(t *testing.T) {
	t.Parallel()

	plan := testMigrationPlan(t, "repo", `{
  "name": "myapp",
  "sdk": {"source": "go"},
  "source": ".",
  "toolchains": [
    {"name": "tools", "source": "./tools"}
  ]
}`)

	require.Equal(t, ModuleConfigFileName, plan.MigratedModuleConfigPath)
	configData := string(plan.WorkspaceConfigData)
	require.NotContains(t, configData, "[modules.myapp]")
	require.NotContains(t, configData, "as-sdk")
	require.Contains(t, configData, "[modules.tools]")
}

// TestPlanMigrationRecordsToolchainsAsModuleDependencies covers the 0.21
// semantics where a module's toolchains were also loaded into its own API:
// module code could call them like dependencies (e.g. dag.Go). Migration must
// therefore install the toolchains into dagger.toml AND record them as
// dependencies of the migrated dagger-module.toml, or that code stops
// compiling after `dagger setup`.
func TestPlanMigrationRecordsToolchainsAsModuleDependencies(t *testing.T) {
	t.Parallel()

	plan := testMigrationPlan(t, "repo", `{
  "name": "myapp",
  "sdk": {"source": "go"},
  "source": ".dagger",
  "dependencies": [
    {"name": "lib", "source": "./libs/lib"},
    {"name": "shared", "source": "github.com/acme/shared@main", "pin": "aaaaaaa"}
  ],
  "toolchains": [
    {"name": "go", "source": "github.com/acme/go@main", "pin": "1111111",
     "customizations": [{"argument": "version", "default": "1.22"}],
     "ignoreChecks": ["lint"]},
    {"name": "local-tc", "source": "./toolchain"},
    {"name": "shared", "source": "github.com/acme/shared@main", "pin": "bbbbbbb"}
  ]
}`)

	require.Equal(t, ModuleConfigFileName, plan.MigratedModuleConfigPath)
	cfg, err := modules.ParseModuleConfigForFilename(plan.MigratedModuleConfigData, ModuleConfigFileName)
	require.NoError(t, err)
	require.Empty(t, cfg.Toolchains, "toolchains never survive as a module config field")

	byName := map[string]*modules.ModuleConfigDependency{}
	for _, dep := range cfg.Dependencies {
		byName[dep.Name] = dep
	}
	require.Len(t, cfg.Dependencies, 4, "explicit deps plus each toolchain, deduplicated by name")

	require.Equal(t, "./libs/lib", byName["lib"].Source)

	goDep := byName["go"]
	require.NotNil(t, goDep, "a toolchain becomes a dependency of the migrated module")
	require.Equal(t, "github.com/acme/go@main", goDep.Source)
	require.Equal(t, "1111111", goDep.Pin, "the toolchain pin is preserved on the dependency")
	require.Empty(t, goDep.Customizations, "toolchain customizations belong to the dagger.toml install")
	require.Empty(t, goDep.IgnoreChecks)

	localTC := byName["local-tc"]
	require.NotNil(t, localTC)
	require.Equal(t, "./toolchain", localTC.Source,
		"the module config does not move, so a local toolchain ref is preserved as-is")

	shared := byName["shared"]
	require.NotNil(t, shared)
	require.Equal(t, "aaaaaaa", shared.Pin,
		"an explicit dependency wins over a same-named toolchain")

	// The toolchains are still installed into the workspace with their
	// workspace-only settings.
	wsCfg, err := ParseConfig(plan.WorkspaceConfigData)
	require.NoError(t, err)
	require.Equal(t, "github.com/acme/go@1111111", wsCfg.Modules["go"].Source)
	require.Equal(t, []string{"lint"}, wsCfg.Modules["go"].Check.Skip)
	require.Contains(t, wsCfg.Modules, "local-tc")
	require.Contains(t, wsCfg.Modules, "shared")
}

// TestPlanMigrationToolchainsWithoutSDKWriteNoModuleConfig verifies that a
// toolchains-only dagger.json (no sdk, so no module code that could have
// called them) still produces no dagger-module.toml.
func TestPlanMigrationToolchainsWithoutSDKWriteNoModuleConfig(t *testing.T) {
	t.Parallel()

	plan := testMigrationPlan(t, "repo", `{
  "name": "myapp",
  "toolchains": [
    {"name": "go", "source": "github.com/acme/go@main", "pin": "1111111"}
  ]
}`)

	require.Empty(t, plan.MigratedModuleConfigPath)
	require.Empty(t, plan.MigratedModuleConfigData)
	require.Contains(t, string(plan.WorkspaceConfigData), "[modules.go]")
}

func TestPlanMigrationRejectsAbsoluteMainModuleSource(t *testing.T) {
	t.Parallel()

	compat := testCompatWorkspace(t, "repo", `{
  "name": "myapp",
  "sdk": {"source": "go"},
  "source": "/tmp"
}`)

	_, err := PlanMigration(compat, compat.ProjectRoot)
	require.ErrorContains(t, err, `source path "/tmp" is absolute`)
}

func TestPlanMigrationWritesMainModuleFirst(t *testing.T) {
	t.Parallel()

	plan := testMigrationPlan(t, "repo", `{
  "name": "myapp",
  "sdk": {"source": "go"},
  "source": "src",
  "toolchains": [
    {"name": "toolchain", "source": "./toolchain"}
  ],
  "blueprint": {"name": "blueprint", "source": "./blueprint"}
}`)

	configData := string(plan.WorkspaceConfigData)
	mainIdx := strings.Index(configData, "[modules.myapp]")
	toolchainIdx := strings.Index(configData, "[modules.toolchain]")
	blueprintIdx := strings.Index(configData, "[modules.blueprint]")
	require.NotEqual(t, -1, mainIdx)
	require.NotEqual(t, -1, toolchainIdx)
	require.NotEqual(t, -1, blueprintIdx)
	require.Less(t, mainIdx, blueprintIdx)
	require.Less(t, mainIdx, toolchainIdx)
}

// TestPlanMigrationWritesAsSDK verifies that the legacy `sdk` field on
// dagger.json is surfaced as a workspace module installed as an SDK, keyed by
// a "dagger-"-prefixed canonical basename (go -> dagger-go-sdk) so it cannot
// collide with an unrelated module named "go", with the migrated module
// recorded under [[modules.<name>.as-sdk.modules]].
func TestPlanMigrationWritesAsSDK(t *testing.T) {
	t.Parallel()

	plan := testMigrationPlan(t, "repo", `{
  "name": "myapp",
  "sdk": {"source": "go"},
  "source": "src",
  "toolchains": [
    {"name": "tools", "source": "./tools"}
  ]
}`)

	configData := string(plan.WorkspaceConfigData)
	require.Contains(t, configData, "[modules.dagger-go-sdk]")
	require.Contains(t, configData, `source = "go"`)
	require.Contains(t, configData, "[[modules.dagger-go-sdk.as-sdk.modules]]")
	require.Contains(t, configData, `path = "."`)
}

func TestMigrationSDKInstallName(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in   string
		want string
	}{
		{"go", "dagger-go-sdk"},         // builtin short name -> prefixed basename
		{"php", "dagger-php-sdk"},       // builtin short name -> prefixed basename
		{"php@v0.18", "dagger-php-sdk"}, // versioned builtin
		{"go-sdk", "go-sdk"},            // already a basename, not a builtin runtime
		{"coolsdk", "coolsdk"},          // custom/local SDK kept as-is
		{"github.com/dagger/go-sdk@v1.2.3", "go-sdk"},
		{"github.com/acme/custom", "custom"},
	} {
		require.Equal(t, tc.want, migrationSDKInstallName(tc.in), "input %q", tc.in)
	}
}

// TestPlanMigrationKeepsUnrelatedModuleNamedLikeSDK verifies the migrated SDK
// install does not clobber an unrelated module that already carries the SDK's
// prefixed install name; it is recorded under a distinct name instead.
func TestPlanMigrationKeepsUnrelatedModuleNamedLikeSDK(t *testing.T) {
	t.Parallel()

	plan := testMigrationPlan(t, "repo", `{
  "name": "myapp",
  "sdk": {"source": "go"},
  "source": "src",
  "toolchains": [
    {"name": "dagger-go-sdk", "source": "github.com/acme/go-sdk"}
  ]
}`)

	cfg, err := ParseConfig(plan.WorkspaceConfigData)
	require.NoError(t, err)

	unrelated, ok := cfg.Modules["dagger-go-sdk"]
	require.True(t, ok)
	require.Equal(t, "github.com/acme/go-sdk", unrelated.Source)
	require.Nil(t, unrelated.AsSDK, "unrelated module must not be marked as an SDK")

	sdk, ok := cfg.Modules["dagger-go-sdk-2"]
	require.True(t, ok)
	require.Equal(t, "go", sdk.Source)
	require.NotNil(t, sdk.AsSDK)
}

// TestPlanMigrationKeepsModuleSharingBuiltinSDKSource covers the subtle case
// where an unrelated module shares both the SDK's prefixed install name and its
// bare source string ("go"): the string means a runtime in SDK context but a
// local path in module context, so it must not be treated as the same install.
func TestPlanMigrationKeepsModuleSharingBuiltinSDKSource(t *testing.T) {
	t.Parallel()

	plan := testMigrationPlan(t, "repo", `{
  "name": "myapp",
  "sdk": {"source": "go"},
  "source": "src",
  "toolchains": [
    {"name": "dagger-go-sdk", "source": "go"}
  ]
}`)

	cfg, err := ParseConfig(plan.WorkspaceConfigData)
	require.NoError(t, err)

	unrelated, ok := cfg.Modules["dagger-go-sdk"]
	require.True(t, ok)
	require.Nil(t, unrelated.AsSDK, "unrelated module must not be marked as an SDK")

	sdk, ok := cfg.Modules["dagger-go-sdk-2"]
	require.True(t, ok)
	require.NotNil(t, sdk.AsSDK)
}

// TestPlanMigrationExternalSDKShortName verifies the SDK short name is
// derived from the canonical ref (basename, @version stripped).
func TestPlanMigrationExternalSDKShortName(t *testing.T) {
	t.Parallel()

	plan := testMigrationPlan(t, "repo", `{
  "name": "myapp",
  "sdk": {"source": "github.com/dagger/go-sdk@v1.2.3"},
  "source": "src",
  "toolchains": [
    {"name": "tools", "source": "./tools"}
  ]
}`)

	configData := string(plan.WorkspaceConfigData)
	require.Contains(t, configData, "[modules.go-sdk]")
	require.Contains(t, configData, `source = "github.com/dagger/go-sdk@v1.2.3"`)
}

func TestPlanMigrationAllowsDifferentPinnedWorkspaceRefs(t *testing.T) {
	t.Parallel()

	plan := testMigrationPlan(t, "repo", `{
  "name": "myapp",
  "toolchains": [
    {"name": "toolchain-a", "source": "github.com/acme/toolchain@main", "pin": "1111111"},
    {"name": "toolchain-b", "source": "github.com/acme/toolchain@main", "pin": "2222222"}
  ]
}`)

	configData := string(plan.WorkspaceConfigData)
	require.Contains(t, configData, `source = "github.com/acme/toolchain@1111111"`)
	require.Contains(t, configData, `source = "github.com/acme/toolchain@2222222"`)
}

// TestPlanMigrationHoistsSubdirectoryToolchains covers a legacy config in a
// subdirectory of the workspace: nested dagger.toml files are never created,
// so its toolchains install into a dagger.toml at the workspace root with
// local sources rebased, while the module config converts in place at the
// subdirectory and the module itself is not installed.
func TestPlanMigrationHoistsSubdirectoryToolchains(t *testing.T) {
	t.Parallel()

	root := "repo"
	compat := testCompatWorkspace(t, filepath.Join(root, "tools", "hello"), `{
  "name": "hello",
  "sdk": {"source": "go"},
  "source": "src",
  "toolchains": [
    {"name": "tc", "source": "./toolchain"},
    {"name": "remote", "source": "github.com/acme/tool@main", "pin": "1111111"}
  ]
}`)

	plan, err := PlanMigration(compat, root)
	require.NoError(t, err)

	require.Equal(t, root, plan.ProjectRoot,
		"the workspace config lands at the workspace root, never nested")
	require.Equal(t, filepath.Join(root, "tools", "hello"), plan.ModuleProjectRoot,
		"the module config stays at the legacy config's own directory")
	require.Equal(t, ModuleConfigFileName, plan.MigratedModuleConfigPath)

	wsCfg, err := ParseConfig(plan.WorkspaceConfigData)
	require.NoError(t, err)
	require.Equal(t, "./tools/hello/toolchain", wsCfg.Modules["tc"].Source,
		"local toolchain sources are rebased to the workspace root")
	require.Equal(t, "github.com/acme/tool@1111111", wsCfg.Modules["remote"].Source,
		"remote toolchain refs pass through untouched")
	_, installed := wsCfg.Modules["hello"]
	require.False(t, installed,
		"a hoisted subdirectory module is not installed into the repo-wide workspace")
}

func TestPlanMigrationRefusesToHoistSubdirectoryBlueprint(t *testing.T) {
	t.Parallel()

	compat := testCompatWorkspace(t, filepath.Join("repo", "tools", "hello"), `{
  "name": "hello",
  "sdk": {"source": "go"},
  "blueprint": {"name": "bp", "source": "github.com/acme/bp@main"}
}`)

	_, err := PlanMigration(compat, "repo")
	require.ErrorContains(t, err, "blueprint cannot be hoisted")
}

func TestPlanMigrationRejectsConfigWithoutWorkspaceConfigMigration(t *testing.T) {
	t.Parallel()

	cfg, err := parseLegacyConfig([]byte(`{
	  "name": "myapp",
	  "sdk": {"source": "go"}
	}`))
	require.NoError(t, err)

	_, err = PlanMigration(buildCompatWorkspace(cfg, filepath.Join("repo", LegacyModuleConfigFileName)), "repo")
	require.ErrorContains(t, err, "dagger.json does not require workspace config migration")
}

func testMigrationPlan(t *testing.T, projectRoot, cfg string) *MigrationPlan {
	t.Helper()

	plan, err := PlanMigration(testCompatWorkspace(t, projectRoot, cfg), projectRoot)
	require.NoError(t, err)
	return plan
}

func testCompatWorkspace(t *testing.T, projectRoot, cfg string) *CompatWorkspace {
	t.Helper()

	configPath := filepath.Join(projectRoot, LegacyModuleConfigFileName)
	compat, err := ParseCompatWorkspaceAt([]byte(cfg), configPath)
	require.NoError(t, err)
	require.NotNil(t, compat)
	return compat
}
