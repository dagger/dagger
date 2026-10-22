package dangv2

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"

	"github.com/dagger/dagger/engine"
)

func TestResolveEntrypointVersions(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		config *string // nil: no dang.toml
		engine string
		dang   string
		err    string
	}{
		{
			name:   "no dang.toml",
			engine: "v1.0.0-beta.20",
			dang:   "2.1",
		},
		{
			name:   "empty dang.toml",
			config: ptr(""),
			engine: "v1.0.0-beta.20",
			dang:   "2.1",
		},
		{
			name:   "dang version only",
			config: ptr(`dang = "2.0"`),
			engine: "v1.0.0-beta.20",
			dang:   "2.0",
		},
		{
			name:   "dang version with patch",
			config: ptr(`dang = "v1.0.3"`),
			engine: "v1.0.0-beta.20",
			dang:   "1.0",
		},
		{
			name: "both versions",
			config: ptr(`dang = "2.0"

[imports.Dagger]
dagger = "v0.20.1"
`),
			engine: "v0.20.1",
			dang:   "2.0",
		},
		{
			name: "dagger version without patch",
			config: ptr(`[imports.Dagger]
dagger = "0.19"
`),
			engine: "v0.19.0",
			dang:   "2.1",
		},
		{
			// The naming cut is a prerelease, v1.0.0-beta.21, so a declared
			// prerelease must reach the engine as is.
			name: "dagger prerelease",
			config: ptr(`[imports.Dagger]
dagger = "v1.0.0-beta.19"
`),
			engine: "v1.0.0-beta.19",
			dang:   "2.1",
		},
		{
			name: "dagger prerelease with build metadata",
			config: ptr(`[imports.Dagger]
dagger = "v1.0.0-beta.20+abc"
`),
			engine: "v1.0.0-beta.20",
			dang:   "2.1",
		},
		{
			name: "dagger = true declares no version",
			config: ptr(`[imports.Dagger]
dagger = true
`),
			engine: "v1.0.0-beta.20",
			dang:   "2.1",
		},
		{
			name: "dagger = false declares no version",
			config: ptr(`[imports.Dagger]
dagger = false
`),
			engine: "v1.0.0-beta.20",
			dang:   "2.1",
		},
		{
			name: "import of another name",
			config: ptr(`[imports.D]
dagger = "v0.20.0"
`),
			engine: "v0.20.0",
			dang:   "2.1",
		},
		{
			name: "agreeing imports",
			config: ptr(`[imports.A]
dagger = "v0.20.0"

[imports.B]
dagger = "0.20"
`),
			engine: "v0.20.0",
			dang:   "2.1",
		},
		{
			name: "disagreeing imports",
			config: ptr(`[imports.A]
dagger = "v0.20.0"

[imports.B]
dagger = "v0.19.0"
`),
			err: `dang.toml: imports "A" and "B" declare different Dagger versions, v0.20.0 and v0.19.0`,
		},
		{
			name: "dagger version too new",
			config: ptr(`[imports.Dagger]
dagger = "v999.0.0"
`),
			err: "dang.toml: module requires dagger v999.0.0, but you have " + engine.Version,
		},
		{
			name: "dagger version too old",
			config: ptr(`[imports.Dagger]
dagger = "v0.9.0"
`),
			err: "dang.toml: module requires dagger v0.9.0, but support for that version has been removed",
		},
		{
			name: "dagger version not a version",
			config: ptr(`[imports.Dagger]
dagger = "latest"
`),
			err: `invalid Dagger version "latest"`,
		},
		{
			name:   "dang version too new",
			config: ptr(`dang = "99.0"`),
			err:    "this project needs Dang 99.0",
		},
		{
			name:   "malformed toml",
			config: ptr(`dang = `),
			err:    "parse dang.toml",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var data []byte
			if tc.config != nil {
				data = []byte(*tc.config)
			}
			versions, err := ResolveEntrypointVersions(context.Background(), "dang.toml", data)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.engine, versions.Engine)
			require.Equal(t, tc.dang, versions.DangString())
		})
	}
}

func TestDefaultEntrypointVersions(t *testing.T) {
	t.Parallel()

	// The default is pinned: an entrypoint that declares nothing must not
	// change meaning when the engine or Dang moves on.
	defaults := DefaultEntrypointVersions()
	require.Equal(t, "v1.0.0-beta.20", defaults.Engine)
	require.Equal(t, dang.DefaultVersion, defaults.Dang)
	require.Equal(t, "2.1", defaults.DangString())

	// It is the view from before the naming cut, which generated entrypoints
	// that declare nothing were written against.
	require.False(t, engine.APIVersionAtLeast(defaults.Engine, engine.IdentifierCasingVersion))
	require.True(t, engine.APIVersionAtLeast(engine.IdentifierCasingVersion, engine.IdentifierCasingVersion))
}

// A declared version goes through the same checks as a dagger.json
// engineVersion, which compare base versions: a later beta of this engine's
// release is accepted there, so it is here, and the module's view then comes
// from engine.ModuleAPIViewVersion like any module's.
func TestResolveEntrypointVersionsLikeEngineVersion(t *testing.T) {
	t.Parallel()

	for _, declared := range []string{"v1.0.0", "v1.0.0-beta.19", "v1.0.0-beta.21", "v1.0.0-beta.999", "v0.20.0"} {
		t.Run(declared, func(t *testing.T) {
			t.Parallel()
			versions, err := ResolveEntrypointVersions(context.Background(), "dang.toml", []byte("[imports.Dagger]\ndagger = \""+declared+"\"\n"))
			maxOK := engine.CheckMaxVersionCompatibility(declared, engine.BaseVersion(engine.Version))
			if !maxOK {
				require.ErrorContains(t, err, "module requires dagger "+declared)
				return
			}
			require.NoError(t, err)
			require.Equal(t, declared, versions.Engine)
		})
	}
}

// legacyCommaSource separates top-level forms with a comma, which Dang 2.0
// accepts and Dang 2.1 does not.
const legacyCommaSource = "let legacyA = 1, let legacyB = 2\n"

func TestEntrypointDangVersion(t *testing.T) {
	t.Parallel()

	source := entrypointSource(withoutSourcesMethods) + legacyCommaSource

	t.Run("declared version parses its own syntax", func(t *testing.T) {
		t.Parallel()
		_, err := runEntrypointDirAt(t, source, "2.0")
		require.NoError(t, err)
	})
	t.Run("default version rejects older syntax", func(t *testing.T) {
		t.Parallel()
		_, err := runEntrypointDirAt(t, source, "")
		require.Error(t, err)
	})
	t.Run("invalid version", func(t *testing.T) {
		t.Parallel()
		_, err := runEntrypointDirAt(t, entrypointSource(withoutSourcesMethods), "99.0")
		require.ErrorContains(t, err, "module entrypoint Dang version")
	})
}

func TestDeclaresEntrypointSourcesAtVersion(t *testing.T) {
	t.Parallel()

	dir := writeDangFiles(t, map[string]string{
		"main.dang": entrypointSource(sourcesMember, goSDKMethods) + legacyCommaSource,
	})

	declared, err := DeclaresEntrypointSources(dir, "2.0")
	require.NoError(t, err)
	require.True(t, declared)

	// Parsed as the default version, the file does not parse, so it declares
	// nothing.
	declared, err = DeclaresEntrypointSources(dir, "")
	require.NoError(t, err)
	require.False(t, declared)

	_, err = DeclaresEntrypointSources(dir, "99.0")
	require.ErrorContains(t, err, "module entrypoint Dang version")
}

func runEntrypointDirAt(t *testing.T, source, dangVersion string) (dang.ValueScope, error) {
	t.Helper()
	ctx := dang.ContextWithImportConfigs(context.Background(), dang.ImportConfig{
		Name:       "Dagger",
		Client:     failingClient{},
		Schema:     contractTestSchema(),
		AutoImport: true,
	})
	return runEntrypointDir(ctx, writeDangFiles(t, map[string]string{"main.dang": source}), dangVersion, false)
}

func ptr[T any](v T) *T {
	return &v
}
