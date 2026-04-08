package workspace

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlanMigrationWritesLockForLegacyPins(t *testing.T) {
	t.Parallel()

	plan := testMigrationPlan(t, "repo", `{
  "name": "myapp",
  "toolchains": [
    {"name": "toolchain", "source": "github.com/acme/toolchain@main", "pin": "1111111"}
  ],
  "blueprint": {"name": "blueprint", "source": "github.com/acme/blueprint@main", "pin": "2222222"}
}`)

	lock, err := ParseLock(plan.LockData)
	require.NoError(t, err)

	result, ok, err := lock.GetModuleResolve("github.com/acme/toolchain@main")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "1111111", result.Value)
	require.Equal(t, PolicyPin, result.Policy)

	result, ok, err = lock.GetModuleResolve("github.com/acme/blueprint@main")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "2222222", result.Value)
	require.Equal(t, PolicyPin, result.Policy)
}

func TestPlanMigrationSkipsLockWithoutPins(t *testing.T) {
	t.Parallel()

	plan := testMigrationPlan(t, "repo", `{
  "name": "myapp",
  "toolchains": [
    {"name": "toolchain", "source": "github.com/acme/toolchain@main"}
  ]
}`)

	require.Empty(t, plan.LockData)
}

func TestPlanMigrationReturnsLookupSources(t *testing.T) {
	t.Parallel()

	plan := testMigrationPlan(t, "repo", `{
  "name": "myapp",
  "toolchains": [
    {"name": "toolchain-a", "source": "github.com/acme/toolchain@main"},
    {"name": "toolchain-b", "source": "github.com/acme/toolchain@main"},
    {"name": "local-toolchain", "source": "./toolchains/local"}
  ],
  "blueprint": {"name": "blueprint", "source": "github.com/acme/blueprint@v1.0.0"}
}`)

	require.Equal(t, []string{
		"github.com/acme/blueprint@v1.0.0",
		"github.com/acme/toolchain@main",
	}, plan.LookupSources)
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
	require.Contains(t, reportData, "Module `toolchain`")
	require.Contains(t, reportData, `"defaultPath": "./custom-config.txt"`)
	require.Contains(t, reportData, `"function": [`)
}

func TestPlanMigrationFailsOnConflictingLegacyPins(t *testing.T) {
	t.Parallel()

	compat := testCompatWorkspace(t, "repo", `{
  "name": "myapp",
  "toolchains": [
    {"name": "toolchain-a", "source": "github.com/acme/toolchain@main", "pin": "1111111"},
    {"name": "toolchain-b", "source": "github.com/acme/toolchain@main", "pin": "2222222"}
  ]
}`)

	_, err := PlanMigration(compat)
	require.ErrorContains(t, err, "conflicting pins for source")
}

func testMigrationPlan(t *testing.T, projectRoot, cfg string) *MigrationPlan {
	t.Helper()

	plan, err := PlanMigration(testCompatWorkspace(t, projectRoot, cfg))
	require.NoError(t, err)
	return plan
}

func testCompatWorkspace(t *testing.T, projectRoot, cfg string) *CompatWorkspace {
	t.Helper()

	configPath := filepath.Join(projectRoot, ModuleConfigFileName)
	compat, err := ParseCompatWorkspaceAt([]byte(cfg), configPath)
	require.NoError(t, err)
	require.NotNil(t, compat)
	return compat
}
