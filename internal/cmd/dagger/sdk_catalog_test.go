package daggercmd

import (
	"bytes"
	"context"
	"maps"
	"os"
	"strings"
	"testing"

	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

func sdkCatalogConfig(name, source string) *workspace.Config {
	return &workspace.Config{
		Modules: map[string]workspace.ModuleEntry{"provider": {Source: source, Pin: "v1.2.3"}},
		SDKs:    map[string]workspace.SDKEntry{name: {Module: "provider"}},
	}
}

// Restore the dynamic command tree and help so tests do not depend on order.
func preserveSDKCommandHelp(t *testing.T) {
	t.Helper()
	commands := moduleInitCmd.Commands()
	annotations := maps.Clone(moduleInitCmd.Annotations)
	addUsage := moduleClientAddCmd.Flags().Lookup("sdk").Usage
	rmUsage := moduleClientRemoveCmd.Flags().Lookup("sdk").Usage
	t.Cleanup(func() {
		for _, cmd := range moduleInitCmd.Commands() {
			moduleInitCmd.RemoveCommand(cmd)
		}
		moduleInitCmd.AddCommand(commands...)
		moduleInitCmd.Annotations = annotations
		moduleInitCmd.SetOut(nil)
		moduleClientAddCmd.Flags().Lookup("sdk").Usage = addUsage
		moduleClientRemoveCmd.Flags().Lookup("sdk").Usage = rmUsage
	})
}

func TestWorkspaceSDKClassification(t *testing.T) {
	previousInstallSDK := moduleInitInstallSDK
	t.Cleanup(func() { moduleInitInstallSDK = previousInstallSDK })
	for _, tc := range []struct {
		name, registered, source, selected, installed, availableName string
	}{
		{name: "fresh", selected: "golang", availableName: "go"},
		{name: "renamed", registered: "my-go", source: "github.com/dagger/go-sdk@v0.1.0", selected: "golang", installed: "my-go"},
		{name: "transport", registered: "my-go", source: "https://github.com/dagger/go-sdk.git@v0.1.0", selected: "go", installed: "my-go"},
		{name: "name collision", registered: "go", source: "github.com/acme/sdk", selected: "go", installed: "go"},
		{name: "alias collision", registered: "golang", source: "github.com/acme/sdk", selected: "golang", installed: "golang"},
		{name: "official alias with collision", registered: "go", source: "github.com/acme/sdk", selected: "golang", availableName: "go-2"},
		{name: "third party basename", registered: "my-go", source: "github.com/acme/go-sdk", selected: "go", availableName: "go"},
		{name: "local name", registered: "my-go", source: "go", selected: "go", availableName: "go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg *workspace.Config
			if tc.registered != "" {
				cfg = sdkCatalogConfig(tc.registered, tc.source)
				if tc.source == "go" {
					cfg.Modules["provider"] = workspace.ModuleEntry{Source: tc.source}
				}
			}
			catalog, err := classifyWorkspaceSDKs(cfg)
			require.NoError(t, err)
			got := catalog.Resolve(tc.selected)
			moduleInitInstallSDK = true
			withInstall, err := resolveSDKInit(catalog, tc.selected)
			require.NoError(t, err)
			require.Equal(t, got, withInstall, "opt-in must use exactly the catalog's classification")
			require.Equal(t, tc.installed, got.InstalledName)
			if tc.availableName != "" {
				require.NotNil(t, got.Official)
				require.Equal(t, tc.availableName, got.Official.RegisteredName)
				require.Equal(t, tc.availableName, workspace.ConventionalSDKName(got.Official.InstallName))
				require.Contains(t, got.Official.InstallCommand(), "--name "+got.Official.InstallName)
			}
			if tc.installed == tc.selected {
				require.Nil(t, got.Official, "installed names take precedence over the registry")
				require.NoError(t, catalog.InitError(tc.selected))
			}
		})
	}
}

func TestModuleInitAllOfficialSDKsInstalled(t *testing.T) {
	preserveSDKCommandHelp(t)
	registry, err := loadSDKRegistry()
	require.NoError(t, err)
	cfg := &workspace.Config{Modules: map[string]workspace.ModuleEntry{}, SDKs: map[string]workspace.SDKEntry{}}
	for _, sdk := range registry {
		cfg.Modules[sdk.Name+"-provider"] = workspace.ModuleEntry{Source: sdk.Repo}
		cfg.SDKs[sdk.Name] = workspace.SDKEntry{Module: sdk.Name + "-provider"}
	}
	require.NoError(t, registerModuleSDKCommandsFromConfig(context.Background(), cfg, "dagger.toml", nil, ""))
	var out bytes.Buffer
	moduleInitCmd.SetOut(&out)
	require.NoError(t, moduleInitCmd.Help())
	require.NotContains(t, out.String(), "AVAILABLE TO INSTALL")
	require.NotContains(t, out.String(), "No SDK is installed")
	require.Len(t, moduleInitCmd.Commands(), len(registry))
}

func TestSDKInstallNameCollisions(t *testing.T) {
	cfg := sdkCatalogConfig("go", "github.com/acme/sdk")
	cfg.Modules["dagger-go-2-sdk"] = workspace.ModuleEntry{Source: "github.com/acme/other"}
	cfg.Modules["go-3"] = workspace.ModuleEntry{Source: "github.com/acme/third"}
	catalog, err := classifyWorkspaceSDKs(cfg)
	require.NoError(t, err)
	goSDK := catalog.Resolve("golang").Official
	require.Equal(t, "go-4", goSDK.RegisteredName)
	require.Equal(t, "dagger-go-4-sdk", goSDK.InstallName)
	require.Contains(t, catalog.InitError("golang").Error(), goSDK.InstallCommand())
	require.Contains(t, catalog.InitError("golang").Error(), "dagger module init go-4")
}

func TestModuleInitSDKGuidance(t *testing.T) {
	catalog, err := classifyWorkspaceSDKs(nil)
	require.NoError(t, err)
	for _, sdk := range catalog.Official {
		for _, name := range append([]string{sdk.Name}, sdk.Aliases...) {
			t.Run(name, func(t *testing.T) {
				err := catalog.InitError(name)
				require.Error(t, err)
				require.NotContains(t, err.Error(), "unknown command")
				require.Contains(t, err.Error(), sdk.Name+" SDK ("+sdk.Repo+")")
				require.Contains(t, err.Error(), sdk.InstallCommand())
				require.Contains(t, err.Error(), "registering SDK name "+`"`+sdk.RegisteredName+`"`)
				require.Contains(t, err.Error(), "dagger module init "+sdk.RegisteredName)
				require.Contains(t, err.Error(), "dagger module init "+name+" --install-sdk")
			})
		}
	}
	require.ErrorContains(t, catalog.InitError("dang"), "built-in Dang runtime can run existing Dang modules")
	require.ErrorContains(t, catalog.InitError("dang"), "creating a new Dang module requires installing the Dang SDK")
	for _, address := range []string{"github.com/dagger/go-sdk", "github.com/dagger/go-sdk@v1.2.3"} {
		err := catalog.InitError(address)
		require.ErrorContains(t, err, "does not accept SDK addresses")
		require.ErrorContains(t, err, "dagger module install "+address+" --name dagger-go-sdk")
		require.ErrorContains(t, err, "dagger module init go")
	}
	err = catalog.InitError("github.com/acme/sdk@v1")
	require.ErrorContains(t, err, "does not accept SDK addresses")
	require.ErrorContains(t, err, "dagger module install github.com/acme/sdk@v1")
	require.ErrorContains(t, err, "dagger sdk list")
	require.ErrorContains(t, err, "dagger module init <registered-name>")

	catalog, err = classifyWorkspaceSDKs(sdkCatalogConfig("custom", "github.com/acme/sdk"))
	require.NoError(t, err)
	err = catalog.InitError("missing")
	require.ErrorContains(t, err, `SDK "missing" is neither installed nor a known official SDK`)
	require.ErrorContains(t, err, "Installed SDKs: custom")
	require.ErrorContains(t, err, "Known official SDKs:")
	for _, sdk := range catalog.Official {
		require.Contains(t, err.Error(), sdk.Name)
	}
	require.ErrorContains(t, err, "dagger module search")
}

func TestRenamedAndCollidingSDKHelpAndList(t *testing.T) {
	preserveSDKCommandHelp(t)
	for _, tc := range []struct {
		name, source, available string
	}{
		{"my-go", "github.com/dagger/go-sdk@v1.0.0", ""},
		{"go", "github.com/acme/sdk", "go-2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := sdkCatalogConfig(tc.name, tc.source)
			require.NoError(t, registerModuleSDKCommandsFromConfig(context.Background(), cfg, "dagger.toml", nil, ""))
			require.NotNil(t, findCommand(moduleInitCmd, tc.name))
			var help, list bytes.Buffer
			moduleInitCmd.SetOut(&help)
			require.NoError(t, moduleInitCmd.Help())
			require.Contains(t, help.String(), "INSTALLED SDKs")
			require.Contains(t, strings.Fields(help.String()), tc.name)
			require.NoError(t, printSDKList(&list, cfg))
			require.Equal(t, []string{tc.name, sdkModuleEntrySource(cfg.Modules["provider"]), "installed"}, strings.Fields(strings.Split(list.String(), "\n")[1]))
			if tc.available == "" {
				require.NotContains(t, availableSDKs(moduleInitCmd), "github.com/dagger/go-sdk")
				for _, line := range strings.Split(list.String(), "\n") {
					if strings.Contains(line, "available to install") {
						require.NotContains(t, line, "github.com/dagger/go-sdk")
					}
				}
				catalog, err := classifyWorkspaceSDKs(cfg)
				require.NoError(t, err)
				for _, name := range []string{"go", "golang", "github.com/dagger/go-sdk"} {
					err := catalog.InitError(name)
					require.ErrorContains(t, err, "dagger module init my-go")
					require.NotContains(t, err.Error(), "dagger module install")
				}
			} else {
				require.Contains(t, help.String(), "dagger module install github.com/dagger/go-sdk --name dagger-go-2-sdk")
				require.Contains(t, list.String(), "registers go-2")
			}
		})
	}
}

func TestSDKGuidanceLeavesWorkspaceUnchanged(t *testing.T) {
	preserveSDKCommandHelp(t)
	oldWorkspace := workspaceRef
	t.Cleanup(func() { workspaceRef = oldWorkspace })
	workspaceRef = ""
	for _, config := range []string{"", "[modules.provider]\nsource = 'github.com/dagger/go-sdk@v1.0.0'\n[sdks.my-go]\nmodule = 'provider'\n"} {
		t.Run(config, func(t *testing.T) {
			t.Chdir(t.TempDir())
			if config != "" {
				require.NoError(t, os.WriteFile("dagger.toml", []byte(config), 0o644))
			}
			for _, name := range []string{"go", "golang", "py", "ts", "dang", "missing", "github.com/dagger/go-sdk", "github.com/acme/sdk"} {
				// This is the production registration path, before Cobra or any
				// SDK constructor inspection. No engine is available or needed.
				err := registerModuleSDKCommands(context.Background(), name)
				require.Error(t, err, name)
				require.NotContains(t, err.Error(), "unknown command")
			}
			entries, err := os.ReadDir(".")
			require.NoError(t, err)
			if config == "" {
				require.Empty(t, entries)
			} else {
				require.Len(t, entries, 1)
				data, err := os.ReadFile("dagger.toml")
				require.NoError(t, err)
				require.Equal(t, config, string(data))
			}
		})
	}
}

func TestSDKListWithoutConfig(t *testing.T) {
	oldWorkspace, oldEnv := workspaceRef, workspaceEnv
	t.Cleanup(func() { workspaceRef, workspaceEnv = oldWorkspace, oldEnv })
	workspaceRef, workspaceEnv = "", ""
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	sdkListCmd.SetOut(&out)
	t.Cleanup(func() { sdkListCmd.SetOut(nil) })
	require.NoError(t, runSDKList(sdkListCmd, nil))
	registry, err := loadSDKRegistry()
	require.NoError(t, err)
	for _, sdk := range registry {
		require.Contains(t, out.String(), sdk.Repo)
	}
	require.Equal(t, len(registry), strings.Count(out.String(), "available to install"))
}
