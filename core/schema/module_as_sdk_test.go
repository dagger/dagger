package schema

import (
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

func TestCurrentModuleAsSDKModulesInScopeForCwd(t *testing.T) {
	modules := func(paths ...string) []*core.CurrentModuleAsSDKModule {
		result := make([]*core.CurrentModuleAsSDKModule, 0, len(paths))
		for _, path := range paths {
			result = append(result, &core.CurrentModuleAsSDKModule{Path: path})
		}
		return result
	}
	paths := func(modules []*core.CurrentModuleAsSDKModule) []string {
		result := make([]string, 0, len(modules))
		for _, mod := range modules {
			result = append(result, mod.Path)
		}
		return result
	}

	managed := modules(
		".",
		"services/api",
		"services/api/src/tool",
		"services/web",
		"libraries/auth",
	)
	tests := []struct {
		name string
		cwd  string
		want []string
	}{
		{name: "root sees all modules", cwd: ".", want: []string{".", "services/api", "services/api/src/tool", "services/web", "libraries/auth"}},
		{name: "directory sees ancestor and descendants", cwd: "services", want: []string{".", "services/api", "services/api/src/tool", "services/web"}},
		{name: "module cwd suppresses farther ancestor", cwd: "services/api", want: []string{"services/api", "services/api/src/tool"}},
		{name: "inside module selects nearest ancestor", cwd: "services/api/src", want: []string{"services/api", "services/api/src/tool"}},
		{name: "deep inside module selects nearest ancestor only", cwd: "services/api/internal", want: []string{"services/api"}},
		{name: "unrelated directory selects root module", cwd: "other", want: []string{"."}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := currentModuleAsSDKModulesInScopeForCwd(managed, test.cwd)
			require.Equal(t, test.want, paths(got))
		})
	}

	t.Run("deduplicates normalized paths", func(t *testing.T) {
		got := currentModuleAsSDKModulesInScopeForCwd(modules("services/api", "services/./api", "services/web"), "services")
		require.Equal(t, []string{"services/api", "services/web"}, paths(got))
	})
}

func TestResolveCurrentModuleSDKEntry(t *testing.T) {
	goSDK := workspace.ModuleEntry{
		Source: "github.com/dagger/go-sdk",
		AsSDK: &workspace.ModuleAsSDK{
			Name:    "go",
			Modules: []workspace.SDKManagedModule{{Path: ".dagger/modules/my-module"}},
		},
	}
	pySDK := workspace.ModuleEntry{
		Source: "github.com/dagger/python-sdk",
		AsSDK: &workspace.ModuleAsSDK{
			Name:    "python",
			Modules: []workspace.SDKManagedModule{{Path: ".dagger/modules/py-module"}},
		},
	}
	plainModule := workspace.ModuleEntry{Source: ".dagger/modules/my-module"}
	const notInstalledAsSDK = "current module is not installed as an SDK in this workspace"

	t.Run("not installed as an SDK", func(t *testing.T) {
		cfg := &workspace.Config{Modules: map[string]workspace.ModuleEntry{
			"my-module": plainModule,
		}}
		_, _, err := resolveCurrentModuleSDKEntry("my-module", cfg)
		require.EqualError(t, err, notInstalledAsSDK)
	})

	t.Run("empty config", func(t *testing.T) {
		_, _, err := resolveCurrentModuleSDKEntry("go-sdk", &workspace.Config{})
		require.EqualError(t, err, notInstalledAsSDK)
	})

	t.Run("single SDK matched by name", func(t *testing.T) {
		cfg := &workspace.Config{Modules: map[string]workspace.ModuleEntry{
			"go-sdk":    goSDK,
			"my-module": plainModule,
		}}
		name, entry, err := resolveCurrentModuleSDKEntry("go-sdk", cfg)
		require.NoError(t, err)
		require.Equal(t, "go-sdk", name)
		require.Equal(t, "go", entry.AsSDK.Name)
		require.Len(t, entry.AsSDK.Modules, 1)
	})

	t.Run("plain workspace module does not resolve to sole SDK install", func(t *testing.T) {
		// Regression: a non-SDK current module in a workspace with exactly one
		// SDK install must not inherit that SDK's as-sdk role data.
		cfg := &workspace.Config{Modules: map[string]workspace.ModuleEntry{
			"go-sdk":    goSDK,
			"my-module": plainModule,
		}}
		_, _, err := resolveCurrentModuleSDKEntry("my-module", cfg)
		require.EqualError(t, err, notInstalledAsSDK)
	})

	t.Run("multiple SDKs matched by name", func(t *testing.T) {
		cfg := &workspace.Config{Modules: map[string]workspace.ModuleEntry{
			"go-sdk":     goSDK,
			"python-sdk": pySDK,
		}}
		name, entry, err := resolveCurrentModuleSDKEntry("python-sdk", cfg)
		require.NoError(t, err)
		require.Equal(t, "python-sdk", name)
		require.Equal(t, "python", entry.AsSDK.Name)
	})

	t.Run("unrelated current module is not installed as an SDK", func(t *testing.T) {
		cfg := &workspace.Config{Modules: map[string]workspace.ModuleEntry{
			"go-sdk":     goSDK,
			"python-sdk": pySDK,
		}}
		_, _, err := resolveCurrentModuleSDKEntry("unrelated", cfg)
		require.EqualError(t, err, notInstalledAsSDK)
	})

	t.Run("populated and empty module lists are preserved", func(t *testing.T) {
		empty := workspace.ModuleEntry{
			Source: "github.com/dagger/typescript-sdk",
			AsSDK:  &workspace.ModuleAsSDK{},
		}
		cfg := &workspace.Config{Modules: map[string]workspace.ModuleEntry{
			"typescript-sdk": empty,
		}}
		_, entry, err := resolveCurrentModuleSDKEntry("typescript-sdk", cfg)
		require.NoError(t, err)
		require.Empty(t, entry.AsSDK.Modules)
		require.Empty(t, entry.AsSDK.Clients)
	})
}
