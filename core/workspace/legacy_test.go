package workspace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseLegacyPins(t *testing.T) {
	t.Parallel()

	data := []byte(`{
		"name": "app",
		"blueprint": {
			"name": "blueprint",
			"source": "github.com/acme/blueprint@main",
			"pin": "blue123"
		},
		"toolchains": [{
			"name": "go",
			"source": "github.com/acme/go-toolchain@main",
			"pin": "tool123",
			"customizations": [{
				"argument": "version",
				"default": "1.24.1"
			}]
		}]
	}`)

	toolchains, err := ParseLegacyToolchains(data)
	require.NoError(t, err)
	require.Len(t, toolchains, 1)
	require.Equal(t, "go", toolchains[0].Name)
	require.Equal(t, "github.com/acme/go-toolchain@main", toolchains[0].Source)
	require.Equal(t, "tool123", toolchains[0].Pin)
	require.Equal(t, map[string]any{"version": "1.24.1"}, toolchains[0].ConfigDefaults)
	require.Len(t, toolchains[0].Customizations, 1)
	require.Equal(t, "version", toolchains[0].Customizations[0].Argument)
	require.Equal(t, "1.24.1", toolchains[0].Customizations[0].Default)

	customPathData := []byte(`{
		"name": "app",
		"toolchains": [{
			"name": "go",
			"source": "github.com/acme/go-toolchain@main",
			"customizations": [{
				"argument": "config",
				"defaultPath": "./custom-config.txt",
				"ignore": ["node_modules"]
			}]
		}]
	}`)
	toolchains, err = ParseLegacyToolchains(customPathData)
	require.NoError(t, err)
	require.Len(t, toolchains, 1)
	require.Len(t, toolchains[0].Customizations, 1)
	require.Equal(t, "config", toolchains[0].Customizations[0].Argument)
	require.Equal(t, "./custom-config.txt", toolchains[0].Customizations[0].DefaultPath)
	require.Equal(t, []string{"node_modules"}, toolchains[0].Customizations[0].Ignore)

	blueprint, err := ParseLegacyBlueprint(data)
	require.NoError(t, err)
	require.NotNil(t, blueprint)
	require.Equal(t, "blueprint", blueprint.Name)
	require.Equal(t, "github.com/acme/blueprint@main", blueprint.Source)
	require.Equal(t, "blue123", blueprint.Pin)

	legacyWorkspace, err := ParseLegacyWorkspace([]byte(`{
		"name": "app",
		"blueprint": {
			"name": "blueprint",
			"source": "./blueprint"
		},
		"toolchains": [{
			"name": "go",
			"source": "./toolchains/go",
			"customizations": [{
				"argument": "version",
				"default": "1.24.1"
			}]
		}]
	}`))
	require.NoError(t, err)
	require.NotNil(t, legacyWorkspace)
	require.Len(t, legacyWorkspace.Modules, 2)
	require.Equal(t, "../toolchains/go", legacyWorkspace.Modules[0].Entry.Source)
	require.Equal(t, map[string]any{"version": "1.24.1"}, legacyWorkspace.Modules[0].Entry.Config)
	require.Equal(t, "../blueprint", legacyWorkspace.Modules[1].Entry.Source)
	require.True(t, legacyWorkspace.Modules[1].Entry.Blueprint)

	cfg := legacyWorkspace.WorkspaceConfig()
	require.Equal(t, ModuleEntry{
		Source:            "../toolchains/go",
		Config:            map[string]any{"version": "1.24.1"},
		LegacyDefaultPath: true,
	}, cfg.Modules["go"])
	require.Equal(t, ModuleEntry{
		Source:            "../blueprint",
		Blueprint:         true,
		LegacyDefaultPath: true,
	}, cfg.Modules["blueprint"])

	unnamedBlueprint, err := ParseLegacyWorkspace([]byte(`{
		"name": "app",
		"blueprint": {
			"source": "./blueprint"
		}
	}`))
	require.NoError(t, err)
	require.NotNil(t, unnamedBlueprint)
	require.Empty(t, unnamedBlueprint.Modules[0].Name)
	require.Equal(t, "blueprint", unnamedBlueprint.Modules[0].ConfigName)
}

func TestParseCompatWorkspace(t *testing.T) {
	t.Parallel()

	compat, err := ParseCompatWorkspace([]byte(`{
		"name": "app",
		"sdk": {"source": "dang"},
		"source": "ci",
		"toolchains": [{
			"name": "go",
			"source": "./toolchains/go"
		}]
	}`))
	require.NoError(t, err)
	require.NotNil(t, compat)
	require.NotNil(t, compat.MainModule)
	require.Equal(t, "app", compat.MainModule.Name)
	require.Equal(t, ModuleEntry{
		Source:    "modules/app",
		Blueprint: true,
	}, compat.MainModule.Entry)
	require.Len(t, compat.Modules, 1)
	require.Equal(t, "../toolchains/go", compat.Modules[0].Entry.Source)

	noCompat, err := ParseCompatWorkspace([]byte(`{
		"name": "app",
		"sdk": {"source": "dang"}
	}`))
	require.NoError(t, err)
	require.Nil(t, noCompat)
}
