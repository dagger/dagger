package workspace

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseCompatWorkspacePins(t *testing.T) {
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

	compatWorkspace, err := ParseCompatWorkspace(data)
	require.NoError(t, err)
	require.NotNil(t, compatWorkspace)
	require.Len(t, compatWorkspace.Modules, 2)
	require.Equal(t, "go", compatWorkspace.Modules[0].Name)
	require.Equal(t, "github.com/acme/go-toolchain@main", compatWorkspace.Modules[0].Source)
	require.Equal(t, "tool123", compatWorkspace.Modules[0].Pin)
	require.Equal(t, map[string]any{"version": "1.24.1"}, compatWorkspace.Modules[0].Entry.Settings)
	require.Len(t, compatWorkspace.Modules[0].ArgCustomizations, 1)
	require.Equal(t, "version", compatWorkspace.Modules[0].ArgCustomizations[0].Argument)
	require.Equal(t, "1.24.1", compatWorkspace.Modules[0].ArgCustomizations[0].Default)
	require.Equal(t, "blueprint", compatWorkspace.Modules[1].Name)
	require.Equal(t, "github.com/acme/blueprint@main", compatWorkspace.Modules[1].Source)
	require.Equal(t, "blue123", compatWorkspace.Modules[1].Pin)

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
	compatWorkspace, err = ParseCompatWorkspace(customPathData)
	require.NoError(t, err)
	require.NotNil(t, compatWorkspace)
	require.Len(t, compatWorkspace.Modules, 1)
	require.Len(t, compatWorkspace.Modules[0].ArgCustomizations, 1)
	require.Equal(t, "config", compatWorkspace.Modules[0].ArgCustomizations[0].Argument)
	require.Equal(t, "./custom-config.txt", compatWorkspace.Modules[0].ArgCustomizations[0].DefaultPath)
	require.Equal(t, []string{"node_modules"}, compatWorkspace.Modules[0].ArgCustomizations[0].Ignore)

	compatWorkspace, err = ParseCompatWorkspace([]byte(`{
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
	require.NotNil(t, compatWorkspace)
	require.Len(t, compatWorkspace.Modules, 2)
	require.Equal(t, "../toolchains/go", compatWorkspace.Modules[0].Entry.Source)
	require.Equal(t, map[string]any{"version": "1.24.1"}, compatWorkspace.Modules[0].Entry.Settings)
	require.Equal(t, "../blueprint", compatWorkspace.Modules[1].Entry.Source)
	require.True(t, compatWorkspace.Modules[1].Entry.Entrypoint)

	cfg := compatWorkspace.WorkspaceConfig()
	require.Equal(t, ModuleEntry{
		Source:            "../toolchains/go",
		Settings:          map[string]any{"version": "1.24.1"},
		LegacyDefaultPath: true,
	}, cfg.Modules["go"])
	require.Equal(t, ModuleEntry{
		Source:            "../blueprint",
		Entrypoint:        true,
		LegacyDefaultPath: true,
	}, cfg.Modules["blueprint"])

	compatWorkspace, err = ParseCompatWorkspace([]byte(`{
			"name": "app",
			"blueprint": {
				"source": "./blueprint"
			}
		}`))
	require.NoError(t, err)
	require.NotNil(t, compatWorkspace)
	require.Empty(t, compatWorkspace.Modules[0].Name)
	require.Equal(t, "blueprint", compatWorkspace.Modules[0].ConfigName)
}

func TestParseCompatWorkspace(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join("repo", ModuleConfigFileName)
	compat, err := ParseCompatWorkspaceAt([]byte(`{
		"name": "app",
		"sdk": {"source": "dang"},
		"source": "ci",
		"toolchains": [{
			"name": "go",
			"source": "./toolchains/go"
		}]
	}`), configPath)
	require.NoError(t, err)
	require.NotNil(t, compat)
	require.Equal(t, configPath, compat.ConfigPath)
	require.Equal(t, "repo", compat.ProjectRoot)
	require.NotNil(t, compat.MainModule)
	require.Equal(t, "app", compat.MainModule.Name)
	require.Equal(t, ModuleEntry{
		Source:     "modules/app",
		Entrypoint: true,
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
