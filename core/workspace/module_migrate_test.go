package workspace

import (
	"testing"

	"github.com/dagger/dagger/core/modules"
	"github.com/stretchr/testify/require"
)

func TestModuleMigrationPreservesConfiguration(t *testing.T) {
	cfg, err := ParseLegacyModuleConfigTolerant([]byte(`{
  "name":"app", "sdk":{"source":"github.com/acme/sdk@main","pin":"1234"},
  "source":"src/../src", "include":["**","!vendor/**"],
  "codegen":{"automaticGitignore":false}, "disableDefaultFunctionCaching":true,
  "dependencies":[{"name":"dep","source":"github.com/acme/dep@main","pin":"5678"}],
  "toolchains":[{"name":"tool","source":"../tool","pin":""}]
}`))
	require.NoError(t, err)
	_, err = PlanModuleMigration(cfg, false)
	require.ErrorContains(t, err, "workspace migrate")
	plan, err := PlanModuleMigration(cfg, true)
	require.NoError(t, err)
	current, err := modules.ParseModuleConfigForFilename(plan.ConfigData, ModuleConfigFileName)
	require.NoError(t, err)
	require.Equal(t, cfg.Name, current.Name)
	require.Equal(t, cfg.SDK, current.SDK)
	require.Equal(t, "src", current.Source)
	require.Equal(t, cfg.Include, current.Include)
	require.Equal(t, cfg.Codegen, current.Codegen)
	require.Equal(t, cfg.DisableDefaultFunctionCaching, current.DisableDefaultFunctionCaching)
	require.Len(t, current.Dependencies, 2)
	require.Equal(t, "5678", current.Dependencies[0].Pin)
	require.Equal(t, "../tool", current.Dependencies[1].Source)
	require.Empty(t, current.Toolchains)
	require.Len(t, cfg.Toolchains, 1, "planning must not mutate the input")
	require.Equal(t, []string{"github.com/acme/dep@5678", "./tool"}, MigratedModuleClients(cfg, "app"))
	require.Equal(t, "github.com/acme/sdk@1234", MigratedModuleSDKSource(cfg, "app"))
}

func TestModuleMigrationRejectsUnhandledRuntimeSettings(t *testing.T) {
	cfg := &modules.ModuleConfig{SDK: &modules.SDK{Source: "go", Config: map[string]any{"unknown": true}}}
	_, err := PlanModuleMigration(cfg, true)
	require.ErrorContains(t, err, "cannot be preserved")
}

func TestMigratedModuleSDKScopePreservation(t *testing.T) {
	cfg := &Config{
		Modules: map[string]ModuleEntry{"sdk": {Source: "github.com/acme/sdk@v1"}, "app": {Source: "./app"}},
		SDKs: map[string]SDKEntry{"custom": {Module: "sdk", Scopes: map[string]SDKScope{
			"./app": {Name: "app", Settings: map[string]any{"setting": "keep"}, Clients: []string{"./existing"}},
		}}},
	}
	err := RegisterMigratedModuleSDK(cfg, "github.com/acme/sdk@v1", "", "app", "app", "./dep", "./existing")
	require.NoError(t, err)
	require.Len(t, cfg.Modules, 2)
	require.Len(t, cfg.SDKs["custom"].Scopes, 1)
	scope := cfg.SDKs["custom"].Scopes["./app"]
	require.True(t, scope.IsModule)
	require.Equal(t, map[string]any{"setting": "keep"}, scope.Settings)
	require.Equal(t, []string{"./existing", "./dep"}, scope.Clients)
	before := SerializeConfig(cfg)
	require.ErrorContains(t, RegisterMigratedModuleSDK(cfg, "github.com/acme/sdk@v2", "", "app", "app"), "belongs to SDK")
	require.Equal(t, before, SerializeConfig(cfg))
	require.ErrorContains(t, RegisterMigratedModuleSDK(cfg, "github.com/acme/sdk@v1", "", "app", "other"), "is named")
	require.Equal(t, before, SerializeConfig(cfg))
}

func TestRegisterMigratedModuleSDKUsesPreferredInstallName(t *testing.T) {
	cfg := &Config{}
	require.NoError(t, RegisterMigratedModuleSDK(cfg, "github.com/dagger/go-sdk", "dagger-go-sdk", "app", "app"))
	require.Equal(t, "github.com/dagger/go-sdk", cfg.Modules["dagger-go-sdk"].Source)
	require.Equal(t, "dagger-go-sdk", cfg.SDKs["go"].Module)
}

func TestEmptyLegacyWorkspaceMigration(t *testing.T) {
	compat, err := ParseMigrationCompatWorkspaceAt([]byte(`{}`), "/repo/dagger.json")
	require.NoError(t, err)
	require.NotNil(t, compat)
	plan, err := PlanWorkspaceMigration(compat, "/repo")
	require.NoError(t, err)
	require.Empty(t, plan.MigratedModuleConfigData)
	_, err = ParseConfig(plan.WorkspaceConfigData)
	require.NoError(t, err)
}

func TestMigratedModuleUnnamedClients(t *testing.T) {
	cfg, err := ParseLegacyModuleConfigTolerant([]byte(`{"sdk":"go","dependencies":["./one","./two"]}`))
	require.NoError(t, err)
	require.Equal(t, []string{"./app/one", "./app/two"}, MigratedModuleClients(cfg, "app"))
}

func TestMigratedModuleAbsoluteReferencesAreNotRebased(t *testing.T) {
	cfg, err := ParseLegacyModuleConfigTolerant([]byte(`{"sdk":"/external/sdk","dependencies":["/external/dep"]}`))
	require.NoError(t, err)
	require.Equal(t, []string{"/external/dep"}, MigratedModuleClients(cfg, "app"))
	require.Equal(t, "/external/sdk", MigratedModuleSDKSource(cfg, "app"))
}
