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
}
