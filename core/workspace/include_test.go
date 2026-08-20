package workspace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// mergeIncluded parses both configs from their TOML source, so every case
// exercises the same explicit-key detection the engine uses.
func mergeIncluded(t *testing.T, includedTOML, currentTOML string) (*Config, error) {
	t.Helper()

	included, err := ParseConfig([]byte(includedTOML))
	require.NoError(t, err)
	current, err := ParseConfig([]byte(currentTOML))
	require.NoError(t, err)
	currentKeys, err := ExplicitConfigKeys([]byte(currentTOML))
	require.NoError(t, err)

	return MergeIncludedConfig(included, current, currentKeys)
}

func mustMergeIncluded(t *testing.T, includedTOML, currentTOML string) *Config {
	t.Helper()

	merged, err := mergeIncluded(t, includedTOML, currentTOML)
	require.NoError(t, err)
	return merged
}

func TestMergeIncludedConfigInheritsAndOverrides(t *testing.T) {
	t.Parallel()

	included := `ignore = ["base-dist"]
defaults_from_dotenv = true
check-generated = false

[modules.go]
source = "github.com/acme/go-toolchain@v1"
pin = "aaa"
entrypoint = true
up.skip = ["base-service"]

[modules.go.settings]
version = "1.22"
strict = true

[modules.node]
source = "github.com/acme/node-toolchain@v2"
`

	current := `[modules.go.settings]
version = "1.24"

[[include]]
source = "github.com/acme/base@v1"
`

	merged := mustMergeIncluded(t, included, current)

	require.Equal(t, []IncludeEntry{{Source: "github.com/acme/base@v1"}}, merged.Include)
	require.Equal(t, []string{"base-dist"}, merged.Ignore)
	require.True(t, merged.DefaultsFromDotEnv)
	require.NotNil(t, merged.CheckGenerated)
	require.False(t, *merged.CheckGenerated)

	// The source-less entry inherits its ref, pin, entrypoint and skips, and
	// overrides one setting while keeping the others.
	goEntry := merged.Modules["go"]
	require.Equal(t, "github.com/acme/go-toolchain@v1", goEntry.Source)
	require.Equal(t, "aaa", goEntry.Pin)
	require.True(t, goEntry.Entrypoint)
	require.Equal(t, []string{"base-service"}, goEntry.Up.Skip)
	require.Equal(t, map[string]any{"version": "1.24", "strict": true}, goEntry.Settings)

	require.Equal(t, "github.com/acme/node-toolchain@v2", merged.Modules["node"].Source)
}

func TestMergeIncludedConfigCurrentWins(t *testing.T) {
	t.Parallel()

	included := `ignore = ["base-dist"]

[modules.go]
source = "github.com/acme/go-toolchain@v1"
pin = "aaa"

[modules.go.settings]
version = "1.22"

[ports.3000]
backendService = "base:web"
backendPort = 8080
`

	current := `ignore = ["dist"]

[modules.go]
source = "github.com/acme/go-toolchain@v2"

[ports.3000]
backendService = "app:web"
backendPort = 9090

[[include]]
source = "github.com/acme/base@v1"
`

	merged := mustMergeIncluded(t, included, current)

	require.Equal(t, []string{"dist"}, merged.Ignore)
	// A replacing source drops the inherited pin, which belonged to the ref it
	// replaced.
	require.Equal(t, "github.com/acme/go-toolchain@v2", merged.Modules["go"].Source)
	require.Empty(t, merged.Modules["go"].Pin)
	require.Equal(t, map[string]any{"version": "1.22"}, merged.Modules["go"].Settings)
	require.Equal(t, PortMapping{BackendService: "app:web", BackendPort: 9090}, merged.Ports["3000"])
}

func TestMergeIncludedConfigLonePinUpdatesInheritedSource(t *testing.T) {
	t.Parallel()

	included := `[modules.go]
source = "github.com/acme/go-toolchain@v1"
pin = "aaa"
`
	current := `[modules.go]
pin = "bbb"

[[include]]
source = "github.com/acme/base@v1"
`

	merged := mustMergeIncluded(t, included, current)

	require.Equal(t, "github.com/acme/go-toolchain@v1", merged.Modules["go"].Source)
	require.Equal(t, "bbb", merged.Modules["go"].Pin)
}

func TestMergeIncludedConfigExplicitZeroOverrides(t *testing.T) {
	t.Parallel()

	included := `ignore = ["base-dist"]
defaults_from_dotenv = true
check-generated = true

[modules.go]
source = "github.com/acme/go-toolchain@v1"
entrypoint = true
check.skip = ["lint"]
`

	current := `ignore = []
defaults_from_dotenv = false
check-generated = false

[modules.go]
entrypoint = false
check.skip = []

[[include]]
source = "github.com/acme/base@v1"
`

	merged := mustMergeIncluded(t, included, current)

	require.Empty(t, merged.Ignore)
	require.False(t, merged.DefaultsFromDotEnv)
	require.NotNil(t, merged.CheckGenerated)
	require.False(t, *merged.CheckGenerated)
	require.False(t, merged.Modules["go"].Entrypoint)
	require.Empty(t, merged.Modules["go"].Check.Skip)
}

func TestMergeIncludedConfigExplicitEmptySkipsOverride(t *testing.T) {
	t.Parallel()

	included := `[modules.go]
source = "github.com/acme/go-toolchain@v1"
up.skip = ["base-service"]
generate.skip = ["base-client"]
check.skip = ["lint"]
`

	current := `[modules.go]
up.skip = []
generate.skip = []

[[include]]
source = "github.com/acme/base@v1"
`

	merged := mustMergeIncluded(t, included, current)

	entry := merged.Modules["go"]
	require.Empty(t, entry.Up.Skip)
	require.Empty(t, entry.Generate.Skip)
	require.Equal(t, []string{"lint"}, entry.Check.Skip, "a skip list the current config leaves alone is inherited")
}

func TestMergeIncludedConfigEnvSourcePinCoupling(t *testing.T) {
	t.Parallel()

	included := `[modules.go]
source = "github.com/acme/go-toolchain@v1"

[env.ci.modules.go]
source = "github.com/acme/go-toolchain@v2"
pin = "aaa"
settings.version = "1.22"
`

	current := `[env.ci.modules.go]
source = "github.com/acme/go-toolchain@v3"

[[include]]
source = "github.com/acme/base@v1"
`

	merged := mustMergeIncluded(t, included, current)

	overlay := merged.Env["ci"].Modules["go"]
	require.Equal(t, "github.com/acme/go-toolchain@v3", overlay.Source)
	require.Empty(t, overlay.Pin, "the inherited pin belonged to the ref the current source replaced")
	require.Equal(t, map[string]any{"version": "1.22"}, overlay.Settings)
}

func TestMergeIncludedConfigKeepsInheritedWhenAbsent(t *testing.T) {
	t.Parallel()

	included := `ignore = ["base-dist"]
defaults_from_dotenv = true

[modules.go]
source = "github.com/acme/go-toolchain@v1"
entrypoint = true
check.skip = ["lint"]
`

	current := `[modules.go.settings]
version = "1.24"

[[include]]
source = "github.com/acme/base@v1"
`

	merged := mustMergeIncluded(t, included, current)

	require.Equal(t, []string{"base-dist"}, merged.Ignore)
	require.True(t, merged.DefaultsFromDotEnv)
	require.True(t, merged.Modules["go"].Entrypoint)
	require.Equal(t, []string{"lint"}, merged.Modules["go"].Check.Skip)
}

func TestMergeIncludedConfigDropsAsSDKAndLegacyDefaultPath(t *testing.T) {
	t.Parallel()

	included := `[modules.go-sdk]
source = "github.com/acme/go-sdk@v1"
legacy-default-path = true

[modules.go-sdk.as-sdk]
name = "go"

[[modules.go-sdk.as-sdk.modules]]
path = "modules/ci"
`

	current := `

[[include]]
source = "github.com/acme/base@v1"
`

	merged := mustMergeIncluded(t, included, current)

	entry := merged.Modules["go-sdk"]
	require.Equal(t, "github.com/acme/go-sdk@v1", entry.Source)
	require.Nil(t, entry.AsSDK, "blueprint as-sdk authoring state points into the base tree")
	require.False(t, entry.LegacyDefaultPath)
}

func TestMergeIncludedConfigKeepsCurrentAsSDK(t *testing.T) {
	t.Parallel()

	included := `[modules.go-sdk]
source = "github.com/acme/go-sdk@v1"

[modules.go-sdk.as-sdk]
name = "base-go"
`

	current := `[modules.go-sdk.as-sdk]
name = "go"

[[modules.go-sdk.as-sdk.modules]]
path = "modules/ci"

[[include]]
source = "github.com/acme/base@v1"
`

	merged := mustMergeIncluded(t, included, current)

	entry := merged.Modules["go-sdk"]
	require.NotNil(t, entry.AsSDK)
	require.Equal(t, "go", entry.AsSDK.Name)
	require.Equal(t, []SDKManagedModule{{Path: "modules/ci"}}, entry.AsSDK.Modules)
}

func TestMergeIncludedConfigEnvs(t *testing.T) {
	t.Parallel()

	included := `[modules.go]
source = "github.com/acme/go-toolchain@v1"

[env.ci.modules.go.settings]
version = "1.22"
strict = true

[env.base-only.modules.go.settings]
version = "1.20"
`

	current := `[env.ci.modules.go.settings]
version = "1.24"

[env.local.modules.go.settings]
version = "1.25"

[[include]]
source = "github.com/acme/base@v1"
`

	merged := mustMergeIncluded(t, included, current)

	require.Equal(t, map[string]any{"version": "1.24", "strict": true}, merged.Env["ci"].Modules["go"].Settings)
	require.Equal(t, map[string]any{"version": "1.20"}, merged.Env["base-only"].Modules["go"].Settings)
	require.Equal(t, map[string]any{"version": "1.25"}, merged.Env["local"].Modules["go"].Settings)
}

func TestMergeIncludedConfigRejectsNestedBlueprint(t *testing.T) {
	t.Parallel()

	_, err := mergeIncluded(t,
		`[[include]]
source = "github.com/acme/deeper@v1"`,
		`[[include]]
source = "github.com/acme/base@v1"`,
	)

	var nested *NestedIncludeError
	require.ErrorAs(t, err, &nested)
	require.Equal(t, "github.com/acme/base@v1", nested.Include)
	require.Equal(t, "github.com/acme/deeper@v1", nested.Nested)
	require.Contains(t, err.Error(), "nested includes are not supported")
}

func TestMergeIncludedConfigWithoutBlueprintConfig(t *testing.T) {
	t.Parallel()

	current, err := ParseConfig([]byte(`[modules.go]
source = "github.com/acme/go-toolchain@v1"
`))
	require.NoError(t, err)

	merged, err := MergeIncludedConfig(nil, current, nil)
	require.NoError(t, err)
	require.Equal(t, "github.com/acme/go-toolchain@v1", merged.Modules["go"].Source)
}

func TestValidateEffectiveConfig(t *testing.T) {
	t.Parallel()

	t.Run("accepts a complete config", func(t *testing.T) {
		t.Parallel()

		cfg, err := ParseConfig([]byte(`[modules.go]
source = "github.com/acme/go-toolchain@v1"

[ports.3000]
backendService = "app:web"
backendPort = 8080
`))
		require.NoError(t, err)
		require.NoError(t, ValidateEffectiveConfig(cfg))
	})

	t.Run("rejects a source-less module with no blueprint", func(t *testing.T) {
		t.Parallel()

		cfg, err := ParseConfig([]byte(`[modules.go.settings]
version = "1.24"
`))
		require.NoError(t, err)

		err = ValidateEffectiveConfig(cfg)
		require.ErrorContains(t, err, `module "go" has no source`)
		require.NotContains(t, err.Error(), "blueprint")
	})

	t.Run("rejects a source-less module left over by a blueprint", func(t *testing.T) {
		t.Parallel()

		merged := mustMergeIncluded(t,
			`[modules.node]
source = "github.com/acme/node-toolchain@v1"
`,
			`[modules.go.settings]
version = "1.24"

[[include]]
source = "github.com/acme/base@v1"
`)

		err := ValidateEffectiveConfig(merged)
		require.ErrorContains(t, err, `module "go" has no source`)
		require.ErrorContains(t, err, "github.com/acme/base@v1")
	})

	t.Run("accepts a partial port mapping", func(t *testing.T) {
		t.Parallel()

		// `dagger workspace config ports.3000.backendService web` writes one key
		// at a time, so a half-written mapping is a state the CLI produces and
		// existing configs are in; rejecting it would break reads that work.
		cfg, err := ParseConfig([]byte(`[ports.3000]
backendService = "app:web"
`))
		require.NoError(t, err)
		require.NoError(t, ValidateEffectiveConfig(cfg))

		cfg, err = ParseConfig([]byte(`[ports.3000]
backendPort = 8080
`))
		require.NoError(t, err)
		require.NoError(t, ValidateEffectiveConfig(cfg))
	})
}

func TestExplicitConfigKeys(t *testing.T) {
	t.Parallel()

	keys, err := ExplicitConfigKeys([]byte(`ignore = []

[modules.go]
entrypoint = false
check.skip = []

[modules."my.mod"]
source = "github.com/acme/mod@v1"

[modules.go.settings]
version = "1.24"

[ports.3000]
backendPort = 8080

[[modules.go.as-sdk.modules]]
path = "modules/ci"

[[include]]
source = "github.com/acme/base@v1"
`))
	require.NoError(t, err)

	for _, key := range []string{
		"ignore",
		"modules.go.entrypoint",
		"modules.go.check.skip",
		"modules.go.settings.version",
		`modules."my.mod".source`,
		"ports.3000.backendPort",
	} {
		require.True(t, keys[key], "expected explicit key %q in %v", key, keys)
	}
	require.False(t, keys["modules.go.source"], "absent keys must not be reported as explicit")
	require.False(t, keys["modules.go.as-sdk.modules"], "array-of-tables blocks carry no merge decision")
	require.False(t, keys["include"], "the include list is carried over wholesale, so its presence decides nothing")
}

func TestConfigIncludeRoundTrip(t *testing.T) {
	t.Parallel()

	cfg, err := ParseConfig([]byte(`[modules.go.settings]
version = "1.24"

[[include]]
source = "github.com/acme/base@v1"
`))
	require.NoError(t, err)
	require.Equal(t, []IncludeEntry{{Source: "github.com/acme/base@v1"}}, cfg.Include)

	serialized := SerializeConfig(cfg)
	require.Contains(t, string(serialized), `[[include]]
source = "github.com/acme/base@v1"`)
	require.NotContains(t, string(serialized), `source = ""`)

	reparsed, err := ParseConfig(serialized)
	require.NoError(t, err)
	require.Equal(t, cfg.Include, reparsed.Include)
	require.Equal(t, cfg.Modules, reparsed.Modules)
}

func TestConfigIncludeWriteReadDelete(t *testing.T) {
	t.Parallel()

	// The config shape is an array of tables, but the CLI addresses the single
	// supported include as one value.
	written, err := WriteConfigValue(nil, "include", "github.com/acme/base@v1")
	require.NoError(t, err)
	require.Contains(t, string(written), "[[include]]")

	cfg, err := ParseConfig(written)
	require.NoError(t, err)
	require.Equal(t, []IncludeEntry{{Source: "github.com/acme/base@v1"}}, cfg.Include)

	// Setting it again replaces rather than appends: only one is supported.
	rewritten, err := WriteConfigValue(written, "include", "github.com/acme/other@v2")
	require.NoError(t, err)
	cfg, err = ParseConfig(rewritten)
	require.NoError(t, err)
	require.Equal(t, []IncludeEntry{{Source: "github.com/acme/other@v2"}}, cfg.Include)
}

func TestUpdateConfigBytesKeepsSourcelessOverride(t *testing.T) {
	t.Parallel()

	existing := []byte(`# base config
[modules.go]
settings.version = "1.24"

[[include]]
source = "github.com/acme/base@v1"
`)

	updated, err := WriteConfigValue(existing, "modules.go.settings.strict", "true")
	require.NoError(t, err)

	require.NotContains(t, string(updated), `source = ""`)
	require.Contains(t, string(updated), "# base config")

	cfg, err := ParseConfig(updated)
	require.NoError(t, err)
	require.Equal(t, []IncludeEntry{{Source: "github.com/acme/base@v1"}}, cfg.Include)
	require.Equal(t, map[string]any{"version": "1.24", "strict": true}, cfg.Modules["go"].Settings)
}
