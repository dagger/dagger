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

// A version 2 manifest names the files outside the module directory that its
// build reads, and writes them back in the same place.
func TestModuleManifestV2Include(t *testing.T) {
	t.Parallel()

	src := `name = "tiny"
include = ["../../../go.mod", "../../../util/parallel/*.go", "!../../../util/parallel/*_test.go"]

[entrypoint]
  kind = "dang"
  source = "./entrypoint"
`
	cfg, err := ParseModuleConfigForFilename([]byte(src), Filename)
	require.NoError(t, err)
	require.Equal(t, []string{"../../../go.mod", "../../../util/parallel/*.go", "!../../../util/parallel/*_test.go"}, cfg.Include)

	out, err := MarshalModuleConfigForFilename(cfg, Filename)
	require.NoError(t, err)
	require.Equal(t, src, string(out))
}

// A version 2 manifest that declares include replaces its entrypoint's
// sources member, even with an empty list, so an empty include is read as
// declared and written back; one without include stays without.
func TestModuleManifestV2IncludeDeclared(t *testing.T) {
	t.Parallel()

	empty := `name = "tiny"
include = []

[entrypoint]
  kind = "dang"
  source = "./entrypoint"
`
	cfg, err := ParseModuleConfigForFilename([]byte(empty), Filename)
	require.NoError(t, err)
	require.True(t, cfg.IncludeDeclared)
	require.Empty(t, cfg.Include)
	out, err := MarshalModuleConfigForFilename(cfg, Filename)
	require.NoError(t, err)
	require.Equal(t, empty, string(out))

	without := `name = "tiny"

[entrypoint]
  kind = "dang"
  source = "./entrypoint"
`
	cfg, err = ParseModuleConfigForFilename([]byte(without), Filename)
	require.NoError(t, err)
	require.False(t, cfg.IncludeDeclared)

	// A fat manifest's include also serves the pre-v2 format, whose include
	// never had to list what sources returns, so it doesn't replace sources.
	fat, err := ParseModuleConfigForFilename([]byte(`name = "tiny"
include = ["../shared/*.txt"]

[runtime]
source = "go"

[entrypoint]
  kind = "dang"
  source = "./entrypoint"
`), Filename)
	require.NoError(t, err)
	require.NotNil(t, fat.Entrypoint)
	require.Equal(t, []string{"../shared/*.txt"}, fat.Include)
	require.False(t, fat.IncludeDeclared)
	out, err = MarshalModuleConfigForFilename(cfg, Filename)
	require.NoError(t, err)
	require.Equal(t, without, string(out))
}

// A fat manifest's include applies to both formats, so a version 2 read takes
// its pre-v2 exclude with it, as the pre-v2 format does.
func TestFatModuleManifestIncludeKeepsExclude(t *testing.T) {
	t.Parallel()

	cfg, err := ParseModuleConfigForFilename([]byte(`name = "tiny"
include = ["../shared/*.txt"]
exclude = ["../shared/skip.txt", "!../shared/keep.txt"]

[entrypoint]
  kind = "dang"
  source = "./entrypoint"
`), Filename)
	require.NoError(t, err)
	require.NotNil(t, cfg.Entrypoint)
	require.Equal(t, []string{"../shared/*.txt", "!../shared/skip.txt", "../shared/keep.txt"}, cfg.Include)
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

// A fat manifest carries a version 2 entrypoint table and the pre-v2 fields at
// the same time. Manifest version 2 lists only an entrypoint's own
// dependencies, so a manifest that declares a runtime and dependencies stays
// on the pre-v2 format. See config_fat_manifest.go.
func TestParseFatModuleManifest(t *testing.T) {
	t.Parallel()

	t.Run("entrypoint without dependencies reads as version 2", func(t *testing.T) {
		t.Parallel()

		cfg, err := ParseModuleConfigForFilename([]byte(`
name = "fat"
engineVersion = "latest"

[runtime]
source = "dang"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`), Filename)
		require.NoError(t, err)
		require.Equal(t, "fat", cfg.Name)
		require.Equal(t, &ModuleEntrypointConfig{
			Kind:   ModuleEntrypointKindDang,
			Source: "./entrypoint",
		}, cfg.Entrypoint)
		// The pre-v2 fields exist for older engines. A version 2 read ignores them.
		require.Nil(t, cfg.SDK)
		require.Empty(t, cfg.EngineVersion)
	})

	t.Run("entrypoint with dependencies and runtime reads as pre-v2", func(t *testing.T) {
		t.Parallel()

		cfg, err := ParseModuleConfigForFilename([]byte(`
name = "fat"
engineVersion = "latest"

[runtime]
source = "dang"

[[dependencies]]
name = "dep"
source = "github.com/acme/dep"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`), Filename)
		require.NoError(t, err)
		require.Equal(t, "fat", cfg.Name)
		// The entrypoint exists for a newer engine. A pre-v2 read ignores it.
		require.Nil(t, cfg.Entrypoint)
		require.Equal(t, "dang", cfg.SDK.Source)
		require.Equal(t, "latest", cfg.EngineVersion)
		require.Equal(t, "github.com/acme/dep", cfg.Dependencies[0].Source)
	})

	t.Run("entrypoint with dependencies and no runtime has the entrypoint's dependencies", func(t *testing.T) {
		t.Parallel()

		cfg, err := ParseModuleConfigForFilename([]byte(`
name = "app"

[[dependencies]]
name = "go"
source = "github.com/acme/go"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`), Filename)
		require.NoError(t, err)
		require.Equal(t, "app", cfg.Name)
		require.Equal(t, ModuleEntrypointKindDang, cfg.Entrypoint.Kind)
		require.Len(t, cfg.Dependencies, 1)
		require.Equal(t, "go", cfg.Dependencies[0].Name)
		require.Equal(t, "github.com/acme/go", cfg.Dependencies[0].Source)

		out, err := MarshalModuleConfigForFilename(cfg, Filename)
		require.NoError(t, err)
		got, err := ParseModuleConfigForFilename(out, Filename)
		require.NoError(t, err)
		require.Equal(t, cfg, got)
	})
}

func TestModuleManifestV2RoundTrip(t *testing.T) {
	t.Parallel()

	want := &ModuleConfigWithUserFields{
		ModuleConfig: ModuleConfig{
			Name: "tiny",
			Entrypoint: &ModuleEntrypointConfig{
				Kind:   ModuleEntrypointKindDang,
				Source: "github.com/acme/entrypoint@v1.0.0",
			},
		},
	}

	out, err := MarshalModuleConfigForFilename(want, Filename)
	require.NoError(t, err)

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
			// A module is named by runtime.source, not by an entrypoint.
			name: "module kind",
			cfg:  "name = \"tiny\"\n[entrypoint]\nkind = \"module\"\nsource = \"github.com/acme/runtime@v1\"\n",
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
			name: "unknown field",
			cfg:  "name = \"tiny\"\nnope = \"x\"\n[entrypoint]\nkind = \"dang\"\nsource = \".\"\n",
			want: "does not support \"nope\"",
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
