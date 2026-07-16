package workspace

import (
	"fmt"
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

[modules.greeter.as-sdk]
name = "go"

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
		AsSDK:             &ModuleAsSDK{Name: "go"},
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
				AsSDK:             &ModuleAsSDK{Name: "go"},
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

[modules.greeter.as-sdk]
name = "go"

[modules.wolfi]
source = "github.com/dagger/dagger/modules/wolfi"

[env.ci.modules.greeter.settings]
enabled = false
greeting = "hola"

[env.local]
`, string(out))
}

func TestWorkspaceCheckGeneratedSetting(t *testing.T) {
	t.Parallel()

	t.Run("unset leaves CheckGenerated nil", func(t *testing.T) {
		t.Parallel()

		cfg, err := ParseConfig([]byte(`[modules.greeter]
source = "modules/greeter"
`))
		require.NoError(t, err)
		require.Nil(t, cfg.CheckGenerated)
	})

	t.Run("parses explicit booleans", func(t *testing.T) {
		t.Parallel()

		cfg, err := ParseConfig([]byte("check-generated = false\n"))
		require.NoError(t, err)
		require.NotNil(t, cfg.CheckGenerated)
		require.False(t, *cfg.CheckGenerated)

		cfg, err = ParseConfig([]byte("check-generated = true\n"))
		require.NoError(t, err)
		require.NotNil(t, cfg.CheckGenerated)
		require.True(t, *cfg.CheckGenerated)
	})

	t.Run("serializes when set", func(t *testing.T) {
		t.Parallel()

		falsy := false
		out := SerializeConfig(&Config{CheckGenerated: &falsy})
		require.Equal(t, "check-generated = false\n\n", string(out))

		truthy := true
		out = SerializeConfig(&Config{CheckGenerated: &truthy})
		require.Equal(t, "check-generated = true\n\n", string(out))

		out = SerializeConfig(&Config{})
		require.NotContains(t, string(out), "check-generated")
	})

	t.Run("read default is true when unset", func(t *testing.T) {
		t.Parallel()

		value, err := ReadConfigValue([]byte(""), "check-generated")
		require.NoError(t, err)
		require.Equal(t, "true", value)
	})

	t.Run("write and read round-trip", func(t *testing.T) {
		t.Parallel()

		data, err := WriteConfigValue(nil, "check-generated", "false")
		require.NoError(t, err)

		cfg, err := ParseConfig(data)
		require.NoError(t, err)
		require.NotNil(t, cfg.CheckGenerated)
		require.False(t, *cfg.CheckGenerated)

		value, err := ReadConfigValue(data, "check-generated")
		require.NoError(t, err)
		require.Equal(t, "false", value)

		data, err = WriteConfigValue(data, "check-generated", "true")
		require.NoError(t, err)
		cfg, err = ParseConfig(data)
		require.NoError(t, err)
		require.NotNil(t, cfg.CheckGenerated)
		require.True(t, *cfg.CheckGenerated)
	})

	t.Run("rejects sub-keys", func(t *testing.T) {
		t.Parallel()

		_, err := WriteConfigValue(nil, "check-generated.skip", "foo")
		require.Error(t, err)
	})
}

func TestSerializeConfigQuotesDynamicPathSegments(t *testing.T) {
	t.Parallel()

	out := SerializeConfig(&Config{
		Modules: map[string]ModuleEntry{
			"my.module": {
				Source: "modules/my.module",
				Settings: map[string]any{
					"some.key": "value",
				},
			},
		},
		Env: map[string]EnvOverlay{
			"review env": {
				Modules: map[string]EnvModuleOverlay{
					"my.module": {
						Settings: map[string]any{
							"some.key": "override",
						},
					},
				},
			},
		},
		Ports: map[string]PortMapping{
			"127.0.0.1": {
				BackendService: "my.module:web",
				BackendPort:    80,
			},
		},
	})

	require.Contains(t, string(out), `[modules."my.module"]`)
	require.Contains(t, string(out), `[modules."my.module".settings]`)
	require.Contains(t, string(out), `"some.key" = "value"`)
	require.Contains(t, string(out), `[env."review env".modules."my.module".settings]`)
	require.Contains(t, string(out), `"some.key" = "override"`)
	require.Contains(t, string(out), `[ports."127.0.0.1"]`)

	cfg, err := ParseConfig(out)
	require.NoError(t, err)
	require.Equal(t, "modules/my.module", cfg.Modules["my.module"].Source)
	require.Equal(t, "value", cfg.Modules["my.module"].Settings["some.key"])
	require.Equal(t, "override", cfg.Env["review env"].Modules["my.module"].Settings["some.key"])
	require.Equal(t, PortMapping{BackendService: "my.module:web", BackendPort: 80}, cfg.Ports["127.0.0.1"])
}

func TestConfigPathSegmentFormatting(t *testing.T) {
	t.Parallel()

	require.Equal(t, "greeter", FormatConfigPathSegment("greeter"))
	require.Equal(t, `"my.module"`, FormatConfigPathSegment("my.module"))
	require.Equal(t, `"review env"`, FormatConfigPathSegment("review env"))
	require.Equal(t, `"café"`, FormatConfigPathSegment("café"))
	require.Equal(t, `"quote\"slash\\line\n"`, FormatConfigPathSegment("quote\"slash\\line\n"))

	path := JoinConfigPath("env", "review env", "modules", "my.module", "settings", "some.key")
	require.Equal(t, `env."review env".modules."my.module".settings."some.key"`, path)

	parts, err := SplitConfigPath(`env."review env".modules."my.module".settings."some.key"`)
	require.NoError(t, err)
	require.Equal(t, []string{"env", "review env", "modules", "my.module", "settings", "some.key"}, parts)

	parts, err = SplitConfigPath(`modules."quote\"slash\\line\n".source`)
	require.NoError(t, err)
	require.Equal(t, []string{"modules", "quote\"slash\\line\n", "source"}, parts)

	_, err = SplitConfigPath(`modules.my module.source`)
	require.EqualError(t, err, `invalid key "modules.my module.source": path segment "my module" must be quoted`)
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

	t.Run("missing boolean fields read as false defaults", func(t *testing.T) {
		t.Parallel()

		value, err := ReadConfigValue(data, "modules.greeter.entrypoint")
		require.NoError(t, err)
		require.Equal(t, "false", value)

		value, err = ReadConfigValue(data, "modules.greeter.legacy-default-path")
		require.NoError(t, err)
		require.Equal(t, "false", value)

		value, err = ReadConfigValue(data, "defaults_from_dotenv")
		require.NoError(t, err)
		require.Equal(t, "false", value)
	})

	t.Run("quoted path segments", func(t *testing.T) {
		t.Parallel()

		data := []byte(`[modules."my.module"]
source = "modules/my.module"

[modules."my.module".settings]
"some.key" = "value"
`)

		value, err := ReadConfigValue(data, `modules."my.module".source`)
		require.NoError(t, err)
		require.Equal(t, "modules/my.module", value)

		value, err = ReadConfigValue(data, `modules."my.module".settings."some.key"`)
		require.NoError(t, err)
		require.Equal(t, "value", value)
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

	t.Run("settings named after module bool fields keep their written value", func(t *testing.T) {
		t.Parallel()

		data, err := WriteConfigValue(nil, "modules.greeter.settings.entrypoint", "cmd/main.go")
		require.NoError(t, err)
		data, err = WriteConfigValue(data, "env.ci.modules.greeter.settings.entrypoint", "cmd/ci.go")
		require.NoError(t, err)

		cfg, err := ParseConfig(data)
		require.NoError(t, err)
		require.Equal(t, "cmd/main.go", cfg.Modules["greeter"].Settings["entrypoint"])
		require.Equal(t, "cmd/ci.go", cfg.Env["ci"].Modules["greeter"].Settings["entrypoint"])
	})

	t.Run("bracketed values keep string handling instead of JSON parsing", func(t *testing.T) {
		t.Parallel()

		data, err := WriteConfigValue(nil, "modules.greeter.settings.glob", `[abc]*`)
		require.NoError(t, err)
		data, err = WriteConfigValue(data, "modules.greeter.settings.globs", `[a]*,[b]*`)
		require.NoError(t, err)
		data, err = WriteConfigValue(data, "modules.greeter.settings.lint", `["!docs","!x"]`)
		require.NoError(t, err)

		cfg, err := ParseConfig(data)
		require.NoError(t, err)
		require.Equal(t, map[string]any{
			"glob":  "[abc]*",
			"globs": []any{"[a]*", "[b]*"},
			"lint":  []any{`["!docs"`, `"!x"]`},
		}, cfg.Modules["greeter"].Settings)
	})

	t.Run("WriteConfigValues stores elements verbatim as native arrays", func(t *testing.T) {
		t.Parallel()

		data, err := WriteConfigValues(nil, "modules.greeter.settings.lint", []string{"!docs", "!x"})
		require.NoError(t, err)
		data, err = WriteConfigValues(data, "modules.greeter.settings.tags", []string{"a,b", `["c"]`, "", "true", "42"})
		require.NoError(t, err)
		data, err = WriteConfigValues(data, "env.ci.modules.greeter.settings.lint", []string{"ci-only"})
		require.NoError(t, err)

		cfg, err := ParseConfig(data)
		require.NoError(t, err)
		require.Equal(t, map[string]any{
			"lint": []any{"!docs", "!x"},
			"tags": []any{"a,b", `["c"]`, "", "true", "42"},
		}, cfg.Modules["greeter"].Settings)
		require.Equal(t, map[string]any{
			"lint": []any{"ci-only"},
		}, cfg.Env["ci"].Modules["greeter"].Settings)
	})

	t.Run("writes module skip fields", func(t *testing.T) {
		t.Parallel()

		data, err := WriteConfigValue(nil, "modules.greeter.generate.skip", "generate-other-files, other-generators:*")
		require.NoError(t, err)
		data, err = WriteConfigValue(data, "modules.greeter.check.skip", "flaky-check")
		require.NoError(t, err)
		data, err = WriteConfigValue(data, "modules.greeter.up.skip", "redis, infra:database")
		require.NoError(t, err)
		data, err = WriteConfigValue(data, "modules.greeter.as-sdk.name", "go")
		require.NoError(t, err)

		cfg, err := ParseConfig(data)
		require.NoError(t, err)
		require.Equal(t, []string{"generate-other-files", "other-generators:*"}, cfg.Modules["greeter"].Generate.Skip)
		require.Equal(t, []string{"flaky-check"}, cfg.Modules["greeter"].Check.Skip)
		require.Equal(t, []string{"redis", "infra:database"}, cfg.Modules["greeter"].Up.Skip)
		require.Equal(t, &ModuleAsSDK{Name: "go"}, cfg.Modules["greeter"].AsSDK)
	})

	t.Run("writes quoted path segments", func(t *testing.T) {
		t.Parallel()

		data, err := WriteConfigValue(nil, `modules."my.module".source`, "modules/my.module")
		require.NoError(t, err)
		data, err = WriteConfigValue(data, `modules."my.module".settings."some.key"`, "value")
		require.NoError(t, err)
		data, err = WriteConfigValue(data, `env."review env".modules."my.module".settings."some.key"`, "override")
		require.NoError(t, err)

		out := string(data)
		require.Contains(t, out, `[modules."my.module"]`)
		require.Contains(t, out, `[modules."my.module".settings]`)
		require.Contains(t, out, `"some.key" = "value"`)
		require.Contains(t, out, `[env."review env".modules."my.module".settings]`)
		require.Contains(t, out, `"some.key" = "override"`)

		cfg, err := ParseConfig(data)
		require.NoError(t, err)
		require.Equal(t, "modules/my.module", cfg.Modules["my.module"].Source)
		require.Equal(t, "value", cfg.Modules["my.module"].Settings["some.key"])
		require.Equal(t, "override", cfg.Env["review env"].Modules["my.module"].Settings["some.key"])
	})

	t.Run("rejects invalid keys", func(t *testing.T) {
		t.Parallel()

		_, err := WriteConfigValue(nil, "modules.greeter", "value")
		require.EqualError(t, err, "cannot set \"modules.greeter\" directly; specify a field like modules.greeter.settings")

		_, err = WriteConfigValue(nil, "modules.greeter.unknown", "value")
		require.EqualError(t, err, "unknown config key \"modules.greeter.unknown\"; valid fields at this level: as-sdk, check, entrypoint, generate, legacy-default-path, pin, settings, source, up")

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

	t.Run("preserves comments across env setting writes", func(t *testing.T) {
		t.Parallel()

		data := []byte(`# top comment
[modules.greeter]
source = "modules/greeter"

# env comment
[env.ci.modules.greeter.settings]
# region comment
region = "us-west-2"
`)

		updated, err := WriteConfigValue(data, "env.ci.modules.greeter.settings.region", "us-east-1")
		require.NoError(t, err)

		out := string(updated)
		require.Contains(t, out, "# top comment")
		require.Contains(t, out, "# env comment")
		require.Contains(t, out, "# region comment")
		require.Contains(t, out, "[env.ci.modules.greeter.settings]")
		require.Contains(t, out, `region = "us-east-1"`)
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
				Description:  "Greeting to use.",
				ExampleValue: `"hello"`,
			}},
		})
		require.NoError(t, err)
		out := string(updated)
		require.Contains(t, out, "# Greeting to use.\n# settings.greeting = \"hello\"")
		require.NotContains(t, out, "# settings.greeting = \"hello\" # string")
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
		require.Contains(t, out, "# greeting = \"hello\"")
		require.NotContains(t, out, "# greeting = \"hello\" # string")
		require.NotContains(t, out, "# settings.greeting = \"hello\"")
	})

	t.Run("adds setting hints for quoted module and setting names", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{
			Modules: map[string]ModuleEntry{
				"my.module": {
					Source: "modules/my.module",
					Settings: map[string]any{
						"enabled.flag": true,
					},
				},
			},
		}

		updated, err := UpdateConfigBytesWithHints(nil, cfg, map[string][]ConstructorArgHint{
			"my.module": {{
				Name:         "some.key",
				TypeLabel:    "string",
				ExampleValue: `"hello"`,
			}},
		})
		require.NoError(t, err)

		out := string(updated)
		require.Contains(t, out, `[modules."my.module".settings]`)
		require.Contains(t, out, `"enabled.flag" = true`)
		require.Contains(t, out, `# "some.key" = "hello"`)
	})

	t.Run("preserves comments across env removal", func(t *testing.T) {
		t.Parallel()

		existing := []byte(`# Dagger workspace configuration
[modules.greeter]
source = "modules/greeter"

# keep dev
[env.dev]

# remove ci
[env.ci.modules.greeter.settings]
region = "us-east-1"
`)

		cfg, err := ParseConfig(existing)
		require.NoError(t, err)

		err = RemoveEnv(cfg, "ci")
		require.NoError(t, err)

		updated, err := UpdateConfigBytes(existing, cfg)
		require.NoError(t, err)

		out := string(updated)
		require.Contains(t, out, "# Dagger workspace configuration")
		require.Contains(t, out, "# keep dev")
		require.Contains(t, out, "[env.dev]")
		require.NotContains(t, out, "[env.ci.modules.greeter.settings]")
		require.NotContains(t, out, `region = "us-east-1"`)
	})

	t.Run("removes existing numeric path segments", func(t *testing.T) {
		t.Parallel()

		existing := []byte(`[ports.3000]
backendService = "web"
backendPort = 80
`)

		cfg, err := ParseConfig(existing)
		require.NoError(t, err)
		delete(cfg.Ports, "3000")

		updated, err := UpdateConfigBytes(existing, cfg)
		require.NoError(t, err)
		require.NotContains(t, string(updated), "[ports.3000]")
		require.NotContains(t, string(updated), "backendService")
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
		require.EqualError(t, err, `workspace env "ci" is not defined (no envs defined); create it by writing a setting: dagger settings --env="ci" <module> <setting> <value>`)
	})

	t.Run("missing env error lists defined envs", func(t *testing.T) {
		t.Parallel()

		_, err := ApplyEnvOverlay(&Config{
			Env: map[string]EnvOverlay{"ci": {}, "prod": {}},
		}, "prdo")
		require.ErrorContains(t, err, `workspace env "prdo" is not defined (defined envs: ci, prod)`)
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

func TestDeleteConfigValue(t *testing.T) {
	t.Parallel()

	data := []byte(`# workspace config
[modules.greeter]
source = "modules/greeter"
entrypoint = true

[modules.greeter.settings]
greeting = "hello"
enabled = true

[env.ci.modules.greeter.settings]
greeting = "hola"

[env.local]
`)

	t.Run("removes a base setting and keeps the rest", func(t *testing.T) {
		t.Parallel()

		out, err := DeleteConfigValue(data, "modules.greeter.settings.greeting")
		require.NoError(t, err)

		cfg, err := ParseConfig(out)
		require.NoError(t, err)
		require.NotContains(t, cfg.Modules["greeter"].Settings, "greeting")
		require.Equal(t, true, cfg.Modules["greeter"].Settings["enabled"])
		require.Equal(t, "hola", cfg.Env["ci"].Modules["greeter"].Settings["greeting"])
		require.Contains(t, string(out), "# workspace config")
	})

	t.Run("removing the last base setting drops the settings section", func(t *testing.T) {
		t.Parallel()

		out, err := DeleteConfigValue(data, "modules.greeter.settings.greeting")
		require.NoError(t, err)
		out, err = DeleteConfigValue(out, "modules.greeter.settings.enabled")
		require.NoError(t, err)

		require.NotContains(t, string(out), "[modules.greeter.settings]")
		cfg, err := ParseConfig(out)
		require.NoError(t, err)
		require.Empty(t, cfg.Modules["greeter"].Settings)
		require.Equal(t, "modules/greeter", cfg.Modules["greeter"].Source)
	})

	t.Run("removes an env setting and keeps the env defined", func(t *testing.T) {
		t.Parallel()

		out, err := DeleteConfigValue(data, "env.ci.modules.greeter.settings.greeting")
		require.NoError(t, err)

		cfg, err := ParseConfig(out)
		require.NoError(t, err)
		require.Contains(t, cfg.Env, "ci")
		require.NotContains(t, cfg.Env["ci"].Modules, "greeter")
		require.Equal(t, "hello", cfg.Modules["greeter"].Settings["greeting"])
		require.Contains(t, cfg.Env, "local")
		require.Contains(t, string(out), "[env.ci]")
		require.NotContains(t, string(out), "[env]\n")
	})

	t.Run("removes boolean and list fields", func(t *testing.T) {
		t.Parallel()

		listData := []byte(`ignore = ["dist"]
defaults_from_dotenv = true
check-generated = false

[modules.greeter]
source = "modules/greeter"
entrypoint = true

[modules.greeter.check]
skip = ["slow"]
`)

		out, err := DeleteConfigValue(listData, "modules.greeter.entrypoint")
		require.NoError(t, err)
		out, err = DeleteConfigValue(out, "modules.greeter.check.skip")
		require.NoError(t, err)
		out, err = DeleteConfigValue(out, "ignore")
		require.NoError(t, err)
		out, err = DeleteConfigValue(out, "defaults_from_dotenv")
		require.NoError(t, err)
		out, err = DeleteConfigValue(out, "check-generated")
		require.NoError(t, err)

		cfg, err := ParseConfig(out)
		require.NoError(t, err)
		require.False(t, cfg.Modules["greeter"].Entrypoint)
		require.Empty(t, cfg.Modules["greeter"].Check.Skip)
		require.Empty(t, cfg.Ignore)
		require.False(t, cfg.DefaultsFromDotEnv)
		require.Nil(t, cfg.CheckGenerated)
		require.NotContains(t, string(out), "entrypoint")
		require.NotContains(t, string(out), "check-generated")
	})

	t.Run("removes explicitly set zero values", func(t *testing.T) {
		t.Parallel()

		zeroData := []byte(`ignore = []
defaults_from_dotenv = false

[modules.greeter]
source = "modules/greeter"
entrypoint = false
`)

		for _, tc := range []struct {
			key  string
			line string
		}{
			{"ignore", "ignore"},
			{"defaults_from_dotenv", "defaults_from_dotenv"},
			{"modules.greeter.entrypoint", "entrypoint"},
		} {
			out, err := DeleteConfigValue(zeroData, tc.key)
			require.NoError(t, err, tc.key)
			require.NotContains(t, string(out), tc.line, tc.key)

			_, err = DeleteConfigValue(out, tc.key)
			require.ErrorContains(t, err, fmt.Sprintf("key %q is not set", tc.key), tc.key)
		}
	})

	t.Run("errors when the key is not set", func(t *testing.T) {
		t.Parallel()

		for _, key := range []string{
			"modules.greeter.settings.missing",
			"modules.missing.settings.greeting",
			"env.missing.modules.greeter.settings.greeting",
			"env.ci.modules.missing.settings.greeting",
			"env.ci.modules.greeter.settings.enabled",
			"modules.greeter.legacy-default-path",
			"check-generated",
			"ignore",
		} {
			_, err := DeleteConfigValue(data, key)
			require.ErrorContains(t, err, fmt.Sprintf("key %q is not set", key), key)
		}
	})

	t.Run("rejects invalid and protected keys", func(t *testing.T) {
		t.Parallel()

		_, err := DeleteConfigValue(data, "")
		require.ErrorContains(t, err, "key is required")

		_, err = DeleteConfigValue(data, "modules.greeter.source")
		require.ErrorContains(t, err, "cannot unset modules.greeter.source")

		_, err = DeleteConfigValue(data, "modules.greeter")
		require.ErrorContains(t, err, `cannot unset "modules.greeter" directly; use dagger uninstall to remove a module`)

		_, err = DeleteConfigValue(data, "modules.greeter.settings")
		require.ErrorContains(t, err, `cannot unset "modules.greeter.settings" directly`)

		_, err = DeleteConfigValue(data, "modules.greeter.badfield")
		require.ErrorContains(t, err, "unknown config key")

		_, err = DeleteConfigValue(data, "modules.greeter.as-sdk.name")
		require.ErrorContains(t, err, "SDK state is managed by dagger install")

		portsData := []byte("[ports.3000]\nbackendService = \"web\"\nbackendPort = 8080\n")
		_, err = DeleteConfigValue(portsData, "ports.3000.backendService")
		require.ErrorContains(t, err, "cannot unset")
	})

	t.Run("removes keys without dedicated handling", func(t *testing.T) {
		t.Parallel()

		pinned := []byte(`[modules.greeter]
source = "modules/greeter"
pin = "abc123"
`)

		out, err := DeleteConfigValue(pinned, "modules.greeter.pin")
		require.NoError(t, err)
		require.NotContains(t, string(out), "pin")

		cfg, err := ParseConfig(out)
		require.NoError(t, err)
		require.Equal(t, "modules/greeter", cfg.Modules["greeter"].Source)
	})

	t.Run("removes quoted setting keys", func(t *testing.T) {
		t.Parallel()

		quoted := []byte(`[modules."my.module"]
source = "modules/my.module"

[modules."my.module".settings]
"some.key" = "value"
other = "kept"
`)

		out, err := DeleteConfigValue(quoted, `modules."my.module".settings."some.key"`)
		require.NoError(t, err)

		cfg, err := ParseConfig(out)
		require.NoError(t, err)
		require.NotContains(t, cfg.Modules["my.module"].Settings, "some.key")
		require.Equal(t, "kept", cfg.Modules["my.module"].Settings["other"])
	})
}
