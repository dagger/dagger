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

	blueprint, err := ParseLegacyBlueprint(data)
	require.NoError(t, err)
	require.NotNil(t, blueprint)
	require.Equal(t, "blueprint", blueprint.Name)
	require.Equal(t, "github.com/acme/blueprint@main", blueprint.Source)
	require.Equal(t, "blue123", blueprint.Pin)
}
