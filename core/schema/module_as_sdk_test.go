package schema

import (
	"testing"

	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

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

	t.Run("not installed as an SDK", func(t *testing.T) {
		cfg := &workspace.Config{Modules: map[string]workspace.ModuleEntry{
			"my-module": plainModule,
		}}
		_, _, err := resolveCurrentModuleSDKEntry("my-module", cfg)
		require.ErrorContains(t, err, "not installed as an SDK")
	})

	t.Run("empty config", func(t *testing.T) {
		_, _, err := resolveCurrentModuleSDKEntry("go-sdk", &workspace.Config{})
		require.ErrorContains(t, err, "not installed as an SDK")
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

	t.Run("single SDK falls back when name does not match", func(t *testing.T) {
		// The running module name need not equal its install entry name (e.g.
		// installed under a custom --name); a lone SDK install is unambiguous.
		cfg := &workspace.Config{Modules: map[string]workspace.ModuleEntry{
			"go-sdk": goSDK,
		}}
		name, _, err := resolveCurrentModuleSDKEntry("some-other-name", cfg)
		require.NoError(t, err)
		require.Equal(t, "go-sdk", name)
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

	t.Run("multiple SDKs without a name match is ambiguous", func(t *testing.T) {
		cfg := &workspace.Config{Modules: map[string]workspace.ModuleEntry{
			"go-sdk":     goSDK,
			"python-sdk": pySDK,
		}}
		_, _, err := resolveCurrentModuleSDKEntry("unrelated", cfg)
		require.ErrorContains(t, err, "cannot determine")
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
