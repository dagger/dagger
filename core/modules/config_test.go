package modules

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/stretchr/testify/require"
)

func TestParseCurrentModuleConfigUsesRuntime(t *testing.T) {
	t.Parallel()

	cfg, err := ParseModuleConfigForFilename([]byte(`
name = "mod"

[runtime]
source = "go"

[[dependencies]]
name = "dep"
source = "github.com/acme/dep"
`), Filename)
	require.NoError(t, err)
	require.Equal(t, "go", cfg.SDK.Source)
	require.Equal(t, "github.com/acme/dep", cfg.Dependencies[0].Source)
}

func TestParseModuleManifestV2(t *testing.T) {
	t.Parallel()

	cfg, err := ParseModuleConfigForFilename([]byte(`
name = "tiny"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`), Filename)
	require.NoError(t, err)
	require.Equal(t, "tiny", cfg.Name)
	require.Equal(t, &ModuleEntrypointConfig{
		Kind:   ModuleEntrypointKindDang,
		Source: "./entrypoint",
	}, cfg.Entrypoint)
	require.Nil(t, cfg.SDK)
}

// The entrypoint table is the only manifest version 2 selector. A manifest
// without it is the pre-v2 format.
func TestParseModuleConfigSelectsFormatByEntrypoint(t *testing.T) {
	t.Parallel()

	t.Run("entrypoint selects version 2", func(t *testing.T) {
		t.Parallel()

		cfg, err := ParseModuleConfigForFilename([]byte(`
name = "tiny"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`), Filename)
		require.NoError(t, err)
		require.NotNil(t, cfg.Entrypoint)
		require.Equal(t, ModuleEntrypointKindDang, cfg.Entrypoint.Kind)
		require.Nil(t, cfg.SDK)
		require.Empty(t, cfg.EngineVersion)
	})

	t.Run("an inline entrypoint table also selects version 2", func(t *testing.T) {
		t.Parallel()

		cfg, err := ParseModuleConfigForFilename(
			[]byte("name = \"tiny\"\nentrypoint = {kind = \"dang\", source = \"./entrypoint\"}\n"),
			Filename,
		)
		require.NoError(t, err)
		require.Equal(t, &ModuleEntrypointConfig{
			Kind:   ModuleEntrypointKindDang,
			Source: "./entrypoint",
		}, cfg.Entrypoint)
	})

	t.Run("no entrypoint stays on the pre-v2 format", func(t *testing.T) {
		t.Parallel()

		cfg, err := ParseModuleConfigForFilename([]byte(`
name = "tiny"
engineVersion = "latest"

[runtime]
source = "go"
`), Filename)
		require.NoError(t, err)
		require.Nil(t, cfg.Entrypoint)
		require.Equal(t, "go", cfg.SDK.Source)
		require.Equal(t, "latest", cfg.EngineVersion)
	})
}

func TestModuleManifestV2RoundTrip(t *testing.T) {
	t.Parallel()

	want := &ModuleConfigWithUserFields{
		ModuleConfig: ModuleConfig{
			Name: "tiny",
			Entrypoint: &ModuleEntrypointConfig{
				Kind:   ModuleEntrypointKindModule,
				Source: "github.com/acme/entrypoint@v1.0.0",
			},
		},
	}

	out, err := MarshalModuleConfigForFilename(want, Filename)
	require.NoError(t, err)
	require.NotContains(t, string(out), "manifestVersion")

	got, err := ParseModuleConfigForFilename(out, Filename)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestParseModuleManifestV2RejectsInvalidFields(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		cfg  string
		want string
	}{
		{
			name: "missing name",
			cfg:  "[entrypoint]\nkind = \"dang\"\nsource = \".\"\n",
			want: "requires name",
		},
		{
			name: "entrypoint is not a table",
			cfg:  "name = \"tiny\"\nentrypoint = \"dang\"\n",
			want: "requires an [entrypoint] table",
		},
		{
			name: "entrypoint is an array of tables",
			cfg:  "name = \"tiny\"\n[[entrypoint]]\nkind = \"dang\"\nsource = \".\"\n",
			want: "requires an [entrypoint] table",
		},
		{
			name: "invalid kind",
			cfg:  "name = \"tiny\"\n[entrypoint]\nkind = \"container\"\nsource = \".\"\n",
			want: "unsupported entrypoint kind",
		},
		{
			name: "missing kind",
			cfg:  "name = \"tiny\"\n[entrypoint]\nsource = \".\"\n",
			want: "requires entrypoint.kind",
		},
		{
			name: "missing source",
			cfg:  "name = \"tiny\"\n[entrypoint]\nkind = \"dang\"\n",
			want: "requires entrypoint.source",
		},
		{
			name: "legacy field",
			cfg:  "name = \"tiny\"\nengineVersion = \"latest\"\n[entrypoint]\nkind = \"dang\"\nsource = \".\"\n",
			want: "does not support \"engineVersion\"",
		},
		{
			name: "version key",
			cfg:  "manifestVersion = 2\nname = \"tiny\"\n[entrypoint]\nkind = \"dang\"\nsource = \".\"\n",
			want: "does not support \"manifestVersion\"",
		},
		{
			name: "entrypoint field",
			cfg:  "name = \"tiny\"\n[entrypoint]\nkind = \"dang\"\nsource = \".\"\ndriver = \"dang\"\n",
			want: "entrypoint does not support \"driver\"",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseModuleConfigForFilename([]byte(tc.cfg), Filename)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestMarshalModuleManifestV2(t *testing.T) {
	t.Parallel()

	out, err := MarshalModuleConfigForFilename(&ModuleConfigWithUserFields{
		ModuleConfig: ModuleConfig{
			Name: "tiny",
			Entrypoint: &ModuleEntrypointConfig{
				Kind:   ModuleEntrypointKindDang,
				Source: "./entrypoint",
			},
		},
	}, Filename)
	require.NoError(t, err)
	require.Equal(t, `name = "tiny"

[entrypoint]
  kind = "dang"
  source = "./entrypoint"
`, string(out))
}

func TestParseCurrentModuleConfigAllowsDependencyNameDefault(t *testing.T) {
	t.Parallel()

	cfg, err := ParseModuleConfigForFilename([]byte(`
name = "mod"

[runtime]
source = "go"

[[dependencies]]
source = "github.com/acme/dep"
`), Filename)
	require.NoError(t, err)
	require.Len(t, cfg.Dependencies, 1)
	require.Empty(t, cfg.Dependencies[0].Name)
	require.Equal(t, "github.com/acme/dep", cfg.Dependencies[0].Source)
}

func TestParseCurrentModuleConfigRejectsLegacyAndWorkspaceFields(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		cfg  string
		want string
	}{
		{
			name: "sdk",
			cfg: `
name = "mod"

[sdk]
source = "go"
`,
			want: "uses runtime instead of sdk",
		},
		{
			name: "blueprint",
			cfg: `
name = "mod"

[runtime]
source = "go"

[blueprint]
source = "github.com/acme/blueprint"
`,
			want: `does not support "blueprint"`,
		},
		{
			name: "toolchains",
			cfg: `
name = "mod"

[runtime]
source = "go"

[[toolchains]]
source = "github.com/acme/toolchain"
`,
			want: `does not support "toolchains"`,
		},
		{
			name: "dependency customizations",
			cfg: `
name = "mod"

[runtime]
source = "go"

[[dependencies]]
source = "github.com/acme/dep"
customizations = [{argument = "src", default = "."}]
`,
			want: `dependency 0 does not support "customizations"`,
		},
		{
			name: "inline dependency customizations",
			cfg: `
name = "mod"
dependencies = [{source = "github.com/acme/dep", customizations = [{argument = "src", default = "."}]}]

[runtime]
source = "go"
`,
			want: `dependency 0 does not support "customizations"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := ParseModuleConfigForFilename([]byte(tc.cfg), Filename)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestParseLegacyModuleConfigAcceptsSDKAndPin(t *testing.T) {
	t.Parallel()

	cfg, err := ParseModuleConfigForFilename([]byte(`{
  "name": "mod",
  "sdk": {"source": "go"},
  "dependencies": [{"name": "dep", "source": "github.com/acme/dep", "pin": "sha256:abc"}]
}`), LegacyFilename)
	require.NoError(t, err)
	require.Equal(t, "go", cfg.SDK.Source)
	require.Equal(t, "sha256:abc", cfg.Dependencies[0].Pin)
}

func TestParseLegacyModuleConfigRejectsEntrypoint(t *testing.T) {
	t.Parallel()

	_, err := ParseModuleConfigForFilename([]byte(`{
  "name": "mod",
  "sdk": {"source": "go"},
  "entrypoint": {"kind": "dang", "source": "./entrypoint"}
}`), LegacyFilename)
	require.ErrorContains(t, err, `dagger.json does not support "entrypoint": use dagger-module.toml instead`)
}

func TestParseLegacyModuleConfigRejectsRuntime(t *testing.T) {
	t.Parallel()

	_, err := ParseModuleConfigForFilename([]byte(`{
  "name": "mod",
  "runtime": {"source": "go"}
}`), LegacyFilename)
	require.ErrorContains(t, err, "uses sdk instead of runtime")
}

func TestMarshalCurrentModuleConfigUsesRuntimeAndPreservesPins(t *testing.T) {
	t.Parallel()

	out, err := MarshalModuleConfigForFilename(&ModuleConfigWithUserFields{
		ModuleConfig: ModuleConfig{
			Name:   "mod",
			SDK:    &SDK{Source: "go"},
			Source: "src",
			Dependencies: []*ModuleConfigDependency{
				{Name: "dep", Source: "github.com/acme/dep", Pin: "sha256:abc"},
			},
		},
	}, Filename)
	require.NoError(t, err)
	require.Contains(t, string(out), `name = "mod"`)
	require.Contains(t, string(out), "[runtime]")
	require.Contains(t, string(out), `source = "go"`)
	require.Contains(t, string(out), "[[dependencies]]")
	require.Contains(t, string(out), `name = "dep"`)
	require.Contains(t, string(out), `source = "github.com/acme/dep"`)
	require.Contains(t, string(out), `pin = "sha256:abc"`)
	require.NotContains(t, string(out), "sdk")

	cfg, err := ParseModuleConfigForFilename(out, Filename)
	require.NoError(t, err)
	require.Equal(t, "go", cfg.SDK.Source)
	require.Equal(t, "src", cfg.Source)
	require.Equal(t, "sha256:abc", cfg.Dependencies[0].Pin)
}

func TestMarshalCurrentModuleConfigOmitsEmptyDependencyName(t *testing.T) {
	t.Parallel()

	out, err := MarshalModuleConfigForFilename(&ModuleConfigWithUserFields{
		ModuleConfig: ModuleConfig{
			Name: "mod",
			SDK:  &SDK{Source: "go"},
			Dependencies: []*ModuleConfigDependency{
				{Source: "github.com/acme/dep"},
			},
		},
	}, Filename)
	require.NoError(t, err)
	require.Contains(t, string(out), "[[dependencies]]")
	require.Contains(t, string(out), `source = "github.com/acme/dep"`)
	require.NotContains(t, string(out), `name = ""`)
}

func TestModuleConfigSchemasKeepLegacyFrozenFieldsOutOfCurrentConfig(t *testing.T) {
	t.Parallel()

	legacySchema := reflectedSchemaJSON(t, &LegacyModuleConfigWithUserFields{})
	require.Contains(t, legacySchema, `"sdk"`)
	require.Contains(t, legacySchema, `"blueprint"`)
	require.Contains(t, legacySchema, `"toolchains"`)
	require.Contains(t, legacySchema, `"customizations"`)
	require.Contains(t, legacySchema, `"portMappings"`)
	require.NotContains(t, legacySchema, `"runtime"`)

	currentSchema := reflectedSchemaJSON(t, &CurrentModuleConfigWithUserFields{})
	require.Contains(t, currentSchema, `"runtime"`)
	require.Contains(t, currentSchema, `"pin"`)
	require.NotContains(t, currentSchema, `"sdk"`)
	require.NotContains(t, currentSchema, `"blueprint"`)
	require.NotContains(t, currentSchema, `"toolchains"`)
	require.NotContains(t, currentSchema, `"customizations"`)
	require.NotContains(t, currentSchema, `"portMappings"`)
}

func reflectedSchemaJSON(t *testing.T, v any) string {
	t.Helper()

	schema, err := json.Marshal(jsonschema.Reflect(v))
	require.NoError(t, err)
	return strings.ReplaceAll(string(schema), `\"`, `"`)
}
