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
