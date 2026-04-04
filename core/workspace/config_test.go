package workspace

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseConfig(t *testing.T) {
	t.Parallel()

	cfg, err := ParseConfig([]byte(`ignore = ["dist"]
defaults_from_dotenv = true

[modules.greeter]
source = "modules/greeter"
blueprint = true
legacy-default-path = true

[modules.greeter.config]
greeting = "hello"
enabled = true
`))
	require.NoError(t, err)
	require.Equal(t, []string{"dist"}, cfg.Ignore)
	require.True(t, cfg.DefaultsFromDotEnv)
	require.Equal(t, ModuleEntry{
		Source:            "modules/greeter",
		Blueprint:         true,
		LegacyDefaultPath: true,
		Config: map[string]any{
			"enabled":  true,
			"greeting": "hello",
		},
	}, cfg.Modules["greeter"])
}

func TestSerializeConfig(t *testing.T) {
	t.Parallel()

	out := SerializeConfig(&Config{
		Ignore:             []string{"dist", "node_modules"},
		DefaultsFromDotEnv: true,
		Modules: map[string]ModuleEntry{
			"wolfi": {
				Source: "github.com/dagger/dagger/modules/wolfi",
			},
			"greeter": {
				Source:            "modules/greeter",
				Blueprint:         true,
				LegacyDefaultPath: true,
				Config: map[string]any{
					"tags":     []string{"main", "develop"},
					"greeting": "hello",
					"enabled":  true,
				},
			},
		},
	})

	require.Equal(t, `ignore = ["dist", "node_modules"]

defaults_from_dotenv = true

[modules.greeter]
source = "modules/greeter"
blueprint = true
legacy-default-path = true

[modules.greeter.config]
enabled = true
greeting = "hello"
tags = ["main", "develop"]

[modules.wolfi]
source = "github.com/dagger/dagger/modules/wolfi"
`, string(out))
}

func TestReadConfigValue(t *testing.T) {
	t.Parallel()

	data := []byte(`[modules.greeter]
source = "modules/greeter"

[modules.greeter.config]
greeting = "hello"
enabled = true
`)

	t.Run("full file", func(t *testing.T) {
		t.Parallel()

		value, err := ReadConfigValue(data, "")
		require.NoError(t, err)
		require.Equal(t, string(data), value)
	})

	t.Run("scalar", func(t *testing.T) {
		t.Parallel()

		value, err := ReadConfigValue(data, "modules.greeter.source")
		require.NoError(t, err)
		require.Equal(t, "modules/greeter", value)
	})

	t.Run("table", func(t *testing.T) {
		t.Parallel()

		value, err := ReadConfigValue(data, "modules.greeter.config")
		require.NoError(t, err)
		require.ElementsMatch(t,
			[]string{
				"enabled = true",
				`greeting = "hello"`,
			},
			strings.Split(value, "\n"),
		)
	})
}

func TestWriteConfigValue(t *testing.T) {
	t.Parallel()

	t.Run("writes typed values", func(t *testing.T) {
		t.Parallel()

		data, err := WriteConfigValue(nil, "modules.greeter.source", "modules/greeter")
		require.NoError(t, err)
		data, err = WriteConfigValue(data, "modules.greeter.blueprint", "true")
		require.NoError(t, err)
		data, err = WriteConfigValue(data, "modules.greeter.config.greeting", "hello")
		require.NoError(t, err)
		data, err = WriteConfigValue(data, "modules.greeter.config.count", "42")
		require.NoError(t, err)
		data, err = WriteConfigValue(data, "modules.greeter.config.tags", "main, develop")
		require.NoError(t, err)
		data, err = WriteConfigValue(data, "defaults_from_dotenv", "true")
		require.NoError(t, err)

		cfg, err := ParseConfig(data)
		require.NoError(t, err)
		require.True(t, cfg.DefaultsFromDotEnv)
		require.Equal(t, ModuleEntry{
			Source:    "modules/greeter",
			Blueprint: true,
			Config: map[string]any{
				"count":    int64(42),
				"greeting": "hello",
				"tags":     []any{"main", "develop"},
			},
		}, cfg.Modules["greeter"])
	})

	t.Run("rejects invalid keys", func(t *testing.T) {
		t.Parallel()

		_, err := WriteConfigValue(nil, "modules.greeter", "value")
		require.EqualError(t, err, "cannot set \"modules.greeter\" directly; specify a field like modules.greeter.blueprint")

		_, err = WriteConfigValue(nil, "modules.greeter.unknown", "value")
		require.EqualError(t, err, "unknown config key \"modules.greeter.unknown\"; valid fields at this level: blueprint, config, legacy-default-path, source")

		_, err = WriteConfigValue(nil, "ignore.path", "value")
		require.EqualError(t, err, "invalid key \"ignore.path\"; ignore does not have sub-keys")
	})
}
