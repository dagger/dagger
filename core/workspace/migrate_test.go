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

	cfg, err := modules.ParseModuleConfigForFilename(plan.MigratedModuleConfigData, ModuleConfigFileName)
	require.NoError(t, err)
	require.Equal(t, "myapp", cfg.Name)
	require.Equal(t, "go", cfg.SDK.Source)
	require.Equal(t, "../../../src", cfg.Source)
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

func TestPlanMigrationRebasesMainModuleSource(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		source string
		want   string
	}{
		{name: "empty", source: "", want: "../../.."},
		{name: "root", source: ".", want: "../../.."},
		{name: "subdir", source: "ci", want: "../../../ci"},
		{name: "nested subdir", source: "src/mod", want: "../../../src/mod"},
		{name: "dot dagger", source: ".dagger", want: "../.."},
		{name: "clean dot dagger", source: "./.dagger/", want: "../.."},
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

			cfg, err := modules.ParseModuleConfigForFilename(plan.MigratedModuleConfigData, ModuleConfigFileName)
			require.NoError(t, err)
			require.Equal(t, tc.want, cfg.Source)
		})
	}
}

func TestPlanMigrationRejectsAbsoluteMainModuleSource(t *testing.T) {
	t.Parallel()

	compat := testCompatWorkspace(t, "repo", `{
  "name": "myapp",
  "sdk": {"source": "go"},
  "source": "/tmp"
}`)

	_, err := PlanMigration(compat)
	require.ErrorContains(t, err, `source path "/tmp" is absolute`)
}

func TestPlanMigrationWritesMainModuleFirst(t *testing.T) {
	t.Parallel()

	plan := testMigrationPlan(t, "repo", `{
  "name": "myapp",
  "sdk": {"source": "go"},
  "source": ".",
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
	require.Contains(t, configData, `path = ".dagger/modules/myapp"`)
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

func TestPlanMigrationRejectsConfigWithoutWorkspaceConfigMigration(t *testing.T) {
	t.Parallel()

	cfg, err := parseLegacyConfig([]byte(`{
	  "name": "myapp",
	  "sdk": {"source": "go"}
	}`))
	require.NoError(t, err)

	_, err = PlanMigration(buildCompatWorkspace(cfg, filepath.Join("repo", LegacyModuleConfigFileName)))
	require.ErrorContains(t, err, "dagger.json does not require workspace config migration")
}

func testMigrationPlan(t *testing.T, projectRoot, cfg string) *MigrationPlan {
	t.Helper()

	plan, err := PlanMigration(testCompatWorkspace(t, projectRoot, cfg))
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

func TestPlanMigrationMovesRuntimeSettingsToWorkspaceConfig(t *testing.T) {
	t.Parallel()

	plan := testMigrationPlan(t, "repo", `{
  "name": "myapp",
  "sdk": {
    "source": "go",
    "config": {"goprivate": "gitlab.example.com/acme"}
  },
  "source": "src"
}`)

	// goprivate moves into the module's workspace settings namespace...
	configData := string(plan.WorkspaceConfigData)
	require.Contains(t, configData, `goprivate = "gitlab.example.com/acme"`)
	// ...and does not survive in the migrated dagger-module.toml
	require.NotContains(t, string(plan.MigratedModuleConfigData), "goprivate")
}

func TestRuntimeSettingsJSON(t *testing.T) {
	t.Parallel()

	settings := map[string]any{
		"goprivate": "gitlab.example.com/acme",
		"someArg":   "constructor-owned",
	}

	out, err := RuntimeSettingsJSON("go", settings)
	require.NoError(t, err)
	require.JSONEq(t, `{"goprivate": "gitlab.example.com/acme"}`, out)

	// goprivate is only reserved for the go SDK: for any other SDK it stays a
	// plain constructor setting and must not leak into the SDK config, where
	// an unknown key fails module load.
	for _, sdkSource := range []string{"python", "typescript", "github.com/acme/custom-sdk", ""} {
		out, err = RuntimeSettingsJSON(sdkSource, settings)
		require.NoError(t, err)
		require.Empty(t, out)
	}

	out, err = RuntimeSettingsJSON("go", map[string]any{"someArg": "x"})
	require.NoError(t, err)
	require.Empty(t, out)
}
