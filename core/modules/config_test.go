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
manifestVersion = 2
name = "tiny"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`), Filename)
	require.NoError(t, err)
	require.Equal(t, ModuleManifestVersion2, cfg.ManifestVersion)
	require.Equal(t, "tiny", cfg.Name)
	require.Equal(t, &ModuleEntrypointConfig{
		Kind:   ModuleEntrypointKindDang,
		Source: "./entrypoint",
	}, cfg.Entrypoint)
	require.Nil(t, cfg.SDK)
}

func TestParseModuleManifestV2RejectsInvalidFields(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		cfg  string
		want string
	}{
		{
			name: "unsupported version",
			cfg:  "manifestVersion = 3\nname = \"tiny\"\n",
			want: "unsupported dagger-module.toml manifest version 3",
		},
		{
			name: "missing name",
			cfg:  "manifestVersion = 2\n[entrypoint]\nkind = \"dang\"\nsource = \".\"\n",
			want: "requires name",
		},
		{
			name: "missing entrypoint",
			cfg:  "manifestVersion = 2\nname = \"tiny\"\n",
			want: "requires entrypoint",
		},
		{
			name: "invalid kind",
			cfg:  "manifestVersion = 2\nname = \"tiny\"\n[entrypoint]\nkind = \"container\"\nsource = \".\"\n",
			want: "unsupported entrypoint kind",
		},
		{
			name: "missing source",
			cfg:  "manifestVersion = 2\nname = \"tiny\"\n[entrypoint]\nkind = \"dang\"\n",
			want: "requires entrypoint.source",
		},
		{
			name: "legacy field",
			cfg:  "manifestVersion = 2\nname = \"tiny\"\nengineVersion = \"latest\"\n[entrypoint]\nkind = \"dang\"\nsource = \".\"\n",
			want: "does not support \"engineVersion\"",
		},
		{
			name: "entrypoint field",
			cfg:  "manifestVersion = 2\nname = \"tiny\"\n[entrypoint]\nkind = \"dang\"\nsource = \".\"\ndriver = \"dang\"\n",
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
			ManifestVersion: ModuleManifestVersion2,
			Name:            "tiny",
			Entrypoint: &ModuleEntrypointConfig{
				Kind:   ModuleEntrypointKindDang,
				Source: "./entrypoint",
			},
		},
	}, Filename)
	require.NoError(t, err)
	require.Equal(t, `manifestVersion = 2
name = "tiny"

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
