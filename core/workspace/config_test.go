package workspace

import (
	"path/filepath"
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
entrypoint = true
legacy-default-path = true

[modules.greeter.settings]
greeting = "hello"
enabled = true

[env.ci.modules.greeter.settings]
greeting = "hola"

[env.local]
`))
	require.NoError(t, err)
	require.Equal(t, []string{"dist"}, cfg.Ignore)
	require.True(t, cfg.DefaultsFromDotEnv)
	require.Equal(t, ModuleEntry{
		Source:            "modules/greeter",
		Entrypoint:        true,
		LegacyDefaultPath: true,
		Settings: map[string]any{
			"enabled":  true,
			"greeting": "hello",
		},
	}, cfg.Modules["greeter"])
	require.Equal(t, EnvOverlay{
		Modules: map[string]EnvModuleOverlay{
			"greeter": {
				Settings: map[string]any{
					"greeting": "hola",
				},
			},
		},
	}, cfg.Env["ci"])
	require.Contains(t, cfg.Env, "local")
	require.Empty(t, cfg.Env["local"].Modules)
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
				Entrypoint:        true,
				LegacyDefaultPath: true,
				Settings: map[string]any{
					"tags":     []string{"main", "develop"},
					"greeting": "hello",
					"enabled":  true,
				},
			},
		},
		Env: map[string]EnvOverlay{
			"local": {},
			"ci": {
				Modules: map[string]EnvModuleOverlay{
					"greeter": {
						Settings: map[string]any{
							"greeting": "hola",
							"enabled":  false,
						},
					},
				},
			},
		},
	})

	require.Equal(t, `ignore = ["dist", "node_modules"]

defaults_from_dotenv = true

[modules.greeter]
source = "modules/greeter"
entrypoint = true
legacy-default-path = true

[modules.greeter.settings]
enabled = true
greeting = "hello"
tags = ["main", "develop"]

[modules.wolfi]
source = "github.com/dagger/dagger/modules/wolfi"

[env.ci.modules.greeter.settings]
enabled = false
greeting = "hola"

[env.local]
`, string(out))
}

func TestReadConfigValue(t *testing.T) {
	t.Parallel()

	data := []byte(`[modules.greeter]
source = "modules/greeter"

[modules.greeter.settings]
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

		value, err := ReadConfigValue(data, "modules.greeter.settings")
		require.NoError(t, err)
		require.ElementsMatch(t,
			[]string{
				"enabled = true",
				`greeting = "hello"`,
			},
			strings.Split(value, "\n"),
		)
	})

	t.Run("env table", func(t *testing.T) {
		t.Parallel()

		envData := []byte(`[env.ci.modules.greeter.settings]
greeting = "hola"
enabled = false
`)

		value, err := ReadConfigValue(envData, "env.ci.modules.greeter.settings")
		require.NoError(t, err)
		require.ElementsMatch(t,
			[]string{
				"enabled = false",
				`greeting = "hola"`,
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
		data, err = WriteConfigValue(data, "modules.greeter.entrypoint", "true")
		require.NoError(t, err)
		data, err = WriteConfigValue(data, "modules.greeter.settings.greeting", "hello")
		require.NoError(t, err)
		data, err = WriteConfigValue(data, "modules.greeter.settings.count", "42")
		require.NoError(t, err)
		data, err = WriteConfigValue(data, "modules.greeter.settings.tags", "main, develop")
		require.NoError(t, err)
		data, err = WriteConfigValue(data, "defaults_from_dotenv", "true")
		require.NoError(t, err)
		data, err = WriteConfigValue(data, "env.ci.modules.greeter.settings.region", "us-east-1")
		require.NoError(t, err)

		cfg, err := ParseConfig(data)
		require.NoError(t, err)
		require.True(t, cfg.DefaultsFromDotEnv)
		require.Equal(t, ModuleEntry{
			Source:     "modules/greeter",
			Entrypoint: true,
			Settings: map[string]any{
				"count":    int64(42),
				"greeting": "hello",
				"tags":     []any{"main", "develop"},
			},
		}, cfg.Modules["greeter"])
		require.Equal(t, EnvOverlay{
			Modules: map[string]EnvModuleOverlay{
				"greeter": {
					Settings: map[string]any{
						"region": "us-east-1",
					},
				},
			},
		}, cfg.Env["ci"])
	})

	t.Run("rejects invalid keys", func(t *testing.T) {
		t.Parallel()

		_, err := WriteConfigValue(nil, "modules.greeter", "value")
		require.EqualError(t, err, "cannot set \"modules.greeter\" directly; specify a field like modules.greeter.settings")

		_, err = WriteConfigValue(nil, "modules.greeter.unknown", "value")
		require.EqualError(t, err, "unknown config key \"modules.greeter.unknown\"; valid fields at this level: entrypoint, legacy-default-path, settings, source")

		_, err = WriteConfigValue(nil, "ignore.path", "value")
		require.EqualError(t, err, "invalid key \"ignore.path\"; ignore does not have sub-keys")

		_, err = WriteConfigValue(nil, "env.ci.modules.greeter.source", "github.com/acme/greeter")
		require.EqualError(t, err, "unknown config key \"env.ci.modules.greeter.source\"; valid fields at this level: settings")
	})

	t.Run("preserves comments and section layout", func(t *testing.T) {
		t.Parallel()

		data := []byte(`# top comment
ignore = ["dist"]

# module comment
[modules.greeter]
source = "modules/greeter"

[modules.greeter.settings]
# greeting comment
greeting = "hello"
`)

		updated, err := WriteConfigValue(data, "modules.greeter.settings.count", "42")
		require.NoError(t, err)

		out := string(updated)
		require.Contains(t, out, "# top comment")
		require.Contains(t, out, "# module comment")
		require.Contains(t, out, "# greeting comment")
		require.Contains(t, out, "[modules.greeter]")
		require.Contains(t, out, "[modules.greeter.settings]")
		require.Contains(t, out, "count = 42")
		require.NotContains(t, out, "[modules]\n\n  [modules.greeter]")
	})
}

func TestUpdateConfigBytes(t *testing.T) {
	t.Parallel()

	t.Run("preserves existing comments across structured writes", func(t *testing.T) {
		t.Parallel()

		existing := []byte(`# Dagger workspace configuration
[modules.greeter]
source = "modules/greeter"
# settings.greeting = "" # string
`)

		cfg, err := ParseConfig(existing)
		require.NoError(t, err)
		cfg.Env = map[string]EnvOverlay{
			"local": {},
		}

		updated, err := UpdateConfigBytes(existing, cfg)
		require.NoError(t, err)

		out := string(updated)
		require.Contains(t, out, "# Dagger workspace configuration")
		require.Contains(t, out, "# settings.greeting = \"\" # string")
		require.Contains(t, out, "[env.local]")
	})

	t.Run("adds setting hints under module sections", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{
			Modules: map[string]ModuleEntry{
				"greeter": {
					Source: "modules/greeter",
				},
			},
		}

		updated, err := UpdateConfigBytesWithHints(nil, cfg, map[string][]ConstructorArgHint{
			"greeter": {{
				Name:         "greeting",
				TypeLabel:    "string",
				ExampleValue: `"hello"`,
			}},
		})
		require.NoError(t, err)
		require.Contains(t, string(updated), "# settings.greeting = \"hello\" # string")
	})

	t.Run("adds setting hints inside existing settings sections", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{
			Modules: map[string]ModuleEntry{
				"greeter": {
					Source: "modules/greeter",
					Settings: map[string]any{
						"enabled": true,
					},
				},
			},
		}

		updated, err := UpdateConfigBytesWithHints(nil, cfg, map[string][]ConstructorArgHint{
			"greeter": {{
				Name:         "greeting",
				TypeLabel:    "string",
				ExampleValue: `"hello"`,
			}},
		})
		require.NoError(t, err)

		out := string(updated)
		require.Contains(t, out, "[modules.greeter.settings]")
		require.Contains(t, out, "# greeting = \"hello\" # string")
		require.NotContains(t, out, "# settings.greeting = \"hello\" # string")
	})
}

func TestApplyEnvOverlay(t *testing.T) {
	t.Parallel()

	t.Run("merges module settings overrides without mutating base config", func(t *testing.T) {
		t.Parallel()

		base := &Config{
			Modules: map[string]ModuleEntry{
				"aws": {
					Source: "github.com/dagger/aws",
					Settings: map[string]any{
						"region": "us-west-2",
						"format": "json",
					},
				},
				"vitest": {
					Source: "github.com/dagger/vitest",
					Settings: map[string]any{
						"reporter": "dot",
					},
				},
			},
			Env: map[string]EnvOverlay{
				"ci": {
					Modules: map[string]EnvModuleOverlay{
						"aws": {
							Settings: map[string]any{
								"region":    "us-east-1",
								"secretKey": "op://supervault/prodawskey",
							},
						},
					},
				},
			},
		}

		applied, err := ApplyEnvOverlay(base, "ci")
		require.NoError(t, err)
		require.Equal(t, map[string]any{
			"format":    "json",
			"region":    "us-east-1",
			"secretKey": "op://supervault/prodawskey",
		}, applied.Modules["aws"].Settings)
		require.Equal(t, map[string]any{
			"reporter": "dot",
		}, applied.Modules["vitest"].Settings)
		require.Equal(t, map[string]any{
			"region": "us-west-2",
			"format": "json",
		}, base.Modules["aws"].Settings)
	})

	t.Run("returns an unchanged copy when env name is empty", func(t *testing.T) {
		t.Parallel()

		base := &Config{
			Modules: map[string]ModuleEntry{
				"aws": {Source: "github.com/dagger/aws"},
			},
		}

		applied, err := ApplyEnvOverlay(base, "")
		require.NoError(t, err)
		require.Equal(t, base, applied)
		require.NotSame(t, base, applied)
	})

	t.Run("rejects missing env", func(t *testing.T) {
		t.Parallel()

		_, err := ApplyEnvOverlay(&Config{}, "ci")
		require.EqualError(t, err, `workspace env "ci" is not defined`)
	})

	t.Run("rejects unknown module alias", func(t *testing.T) {
		t.Parallel()

		_, err := ApplyEnvOverlay(&Config{
			Modules: map[string]ModuleEntry{
				"aws": {Source: "github.com/dagger/aws"},
			},
			Env: map[string]EnvOverlay{
				"ci": {
					Modules: map[string]EnvModuleOverlay{
						"missing": {Settings: map[string]any{"region": "us-east-1"}},
					},
				},
			},
		}, "ci")
		require.EqualError(t, err, `workspace env "ci" references unknown module "missing"`)
	})
}

func TestResolveModuleEntrySource(t *testing.T) {
	t.Parallel()

	t.Run("resolves relative local source from config dir", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, filepath.Clean(".dagger/modules/greeter"), ResolveModuleEntrySource(LockDirName, "modules/greeter"))
	})

	t.Run("preserves absolute local source", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, filepath.Clean("/tmp/greeter"), ResolveModuleEntrySource(LockDirName, "/tmp/greeter"))
	})

	t.Run("leaves remote source unchanged", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, "github.com/dagger/dagger/modules/wolfi", ResolveModuleEntrySource(LockDirName, "github.com/dagger/dagger/modules/wolfi"))
	})
}
