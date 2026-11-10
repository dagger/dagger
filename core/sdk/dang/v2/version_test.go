package dangv2

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
	"golang.org/x/mod/semver"

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
			engine: DefaultEntrypointEngineVersion,
			dang:   "2.1",
		},
		{
			name:   "empty dang.toml",
			config: ptr(""),
			engine: DefaultEntrypointEngineVersion,
			dang:   "2.1",
		},
		{
			name:   "dang version only",
			config: ptr(`dang = "2.0"`),
			engine: DefaultEntrypointEngineVersion,
			dang:   "2.0",
		},
		{
			name:   "dang version with patch",
			config: ptr(`dang = "v1.0.3"`),
			engine: DefaultEntrypointEngineVersion,
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
			// The naming cut is a prerelease, v1.0.0-beta.26, so a declared
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
			engine: DefaultEntrypointEngineVersion,
			dang:   "2.1",
		},
		{
			name: "dagger = false declares no version",
			config: ptr(`[imports.Dagger]
dagger = false
`),
			engine: DefaultEntrypointEngineVersion,
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
			err:    "dang.toml: module requires Dang 99.0, but this Dagger engine supports Dang " + dang.CurrentVersion.String() + " at most; upgrade Dagger",
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
	require.Equal(t, "v1.0.0-beta.25", defaults.Engine)
	require.Equal(t, dang.Version2_1, defaults.Dang)
	require.Equal(t, "2.1", defaults.DangString())

	// It is the last release before the cut, which generated entrypoints that
	// declare nothing were last run on: past the gates of the releases with
	// views of their own (engine.PrereleaseViewsVersion on), before the core
	// renames and the new naming rules.
	require.Equal(t, defaults.Engine, engine.ModuleAPIViewVersion(defaults.Engine))
	require.True(t, engine.APIVersionAtLeast(defaults.Engine, engine.PrereleaseViewsVersion))
	require.False(t, engine.APIVersionAtLeast(defaults.Engine, engine.IdentifierCasingVersion))
	require.True(t, engine.APIVersionAtLeast(engine.IdentifierCasingVersion, engine.IdentifierCasingVersion))
}

// A declared version goes through the same checks as a dagger.json
// engineVersion, which compare base versions: a later beta of this engine's
// release, or the release itself, is accepted there, so it is here, and the
// module's view then comes from engine.ModuleAPIViewVersion like any
// module's, capped at the engine's own view.
func TestResolveEntrypointVersionsLikeEngineVersion(t *testing.T) {
	t.Parallel()

	for _, declared := range []string{"v1.0.0", "v1.0.0-beta.19", "v1.0.0-beta.21", "v1.0.0-beta.25", "v1.0.0-beta.26", "v1.0.0-beta.999", "v0.20.0"} {
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
			view := engine.ModuleAPIViewVersion(versions.Engine)
			require.LessOrEqual(t, semver.Compare(view, engine.APIViewVersion(engine.Version)), 0,
				"a declared version past the engine's is served the engine's view")
		})
	}
}

// withEngineVersion makes t run as an engine of version, restoring
// engine.Version when t ends. A module's view, and so its Dang version, is
// capped at the engine's (engine.ModuleAPIViewVersion): until a release
// reaches Dang24ModuleVersion, only an engine of that version serves its
// modules Dang 2.4. A test using it must not be parallel, so it runs before
// the package's parallel tests, which read engine.Version.
func withEngineVersion(t *testing.T, version string) {
	t.Helper()
	previous := engine.Version
	engine.Version = version
	t.Cleanup(func() { engine.Version = previous })
}

// A Dang SDK module's dang.toml wins over its manifest's engineVersion; what
// it leaves undeclared follows the manifest, as the Dang CLI reads it.
func TestResolveModuleVersions(t *testing.T) {
	// Not parallel: it runs as the engine of Dang24ModuleVersion.
	withEngineVersion(t, Dang24ModuleVersion)

	for _, tc := range []struct {
		name     string
		manifest string  // the manifest's engineVersion
		config   *string // nil: no dang.toml
		engine   string
		declared string
		dang     string
		err      string
	}{
		{
			name:     "no dang.toml, before Dang 2",
			manifest: "v0.20.6",
			engine:   "v0.20.6",
			dang:     "1.0",
		},
		{
			name:     "no dang.toml, at Dang 2",
			manifest: engine.MinimumDangV2ModuleVersion,
			engine:   engine.MinimumDangV2ModuleVersion,
			dang:     "2.1",
		},
		{
			name:     "no dang.toml, later",
			manifest: "v1.0.0-beta.20",
			engine:   "v1.0.0-beta.20",
			dang:     "2.1",
		},
		{
			name:     "no dang.toml, at Dang 2.4",
			manifest: Dang24ModuleVersion,
			engine:   Dang24ModuleVersion,
			dang:     "2.4",
		},
		{
			name:     "dang.toml without keys, before Dang 2",
			manifest: "v0.20.6",
			config:   ptr(""),
			engine:   "v0.20.6",
			dang:     "1.0",
		},
		{
			name:     "dang.toml without keys, later",
			manifest: "v0.21.9",
			config:   ptr(""),
			engine:   "v0.21.9",
			dang:     "2.1",
		},
		{
			name:     "dang.toml without keys, at Dang 2.4",
			manifest: Dang24ModuleVersion,
			config:   ptr(""),
			engine:   Dang24ModuleVersion,
			dang:     "2.4",
		},
		{
			name:     "dang version wins over the default",
			manifest: "v0.20.6",
			config:   ptr(`dang = "2.0"`),
			engine:   "v0.20.6",
			dang:     "2.0",
		},
		{
			name:     "dang version wins over the Dang 2.4 default",
			manifest: Dang24ModuleVersion,
			config:   ptr(`dang = "2.1"`),
			engine:   Dang24ModuleVersion,
			dang:     "2.1",
		},
		{
			name:     "dagger version wins over the manifest",
			manifest: "v0.20.6",
			config:   ptr("[imports.Dagger]\ndagger = \"v0.21.9\"\n"),
			engine:   "v0.21.9",
			declared: "v0.21.9",
			dang:     "2.1",
		},
		{
			name:     "a newer dagger version picks Dang 2.4",
			manifest: "v0.21.9",
			config:   ptr("[imports.Dagger]\ndagger = \"" + Dang24ModuleVersion + "\"\n"),
			engine:   Dang24ModuleVersion,
			declared: Dang24ModuleVersion,
			dang:     "2.4",
		},
		{
			name:     "an older dagger version picks the old default Dang",
			manifest: "v0.21.9",
			config:   ptr("[imports.Dagger]\ndagger = \"v0.20.6\"\n"),
			engine:   "v0.20.6",
			declared: "v0.20.6",
			dang:     "1.0",
		},
		{
			name:     "dagger = true follows the manifest",
			manifest: "v0.20.6",
			config:   ptr("[imports.Dagger]\ndagger = true\n"),
			engine:   "v0.20.6",
			dang:     "1.0",
		},
		{
			name:     "both declared",
			manifest: "v0.20.6",
			config:   ptr("dang = \"2.1\"\n\n[imports.Dagger]\ndagger = \"v1.0.0-beta.20\"\n"),
			engine:   "v1.0.0-beta.20",
			declared: "v1.0.0-beta.20",
			dang:     "2.1",
		},
		{
			name:     "declared dagger version too new",
			manifest: "v0.20.6",
			config:   ptr("[imports.Dagger]\ndagger = \"v999.0.0\"\n"),
			err:      "dang.toml: module requires dagger v999.0.0, but you have " + engine.Version,
		},
		{
			name:     "dang version too new",
			manifest: "v0.20.6",
			config:   ptr(`dang = "v99.1.2"`),
			err:      "dang.toml: module requires Dang 99.1, but this Dagger engine supports Dang " + dang.CurrentVersion.String() + " at most; upgrade Dagger",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var data []byte
			if tc.config != nil {
				data = []byte(*tc.config)
			}
			versions, err := ResolveModuleVersions(context.Background(), "dang.toml", data, tc.manifest)
			if tc.err != "" {
				require.EqualError(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.engine, versions.Engine)
			require.Equal(t, tc.declared, versions.DeclaredEngine)
			require.Equal(t, tc.dang, versions.DangString())
		})
	}
}

func TestDefaultModuleVersions(t *testing.T) {
	// Not parallel: it runs as the engine of Dang24ModuleVersion.
	withEngineVersion(t, Dang24ModuleVersion)

	// Modules written before Dang 2 ran on the Dang v1 line.
	require.Equal(t, dang.Version1_0, DefaultModuleDangVersion("v0.20.6"))
	require.Equal(t, dang.Version1_0, DefaultModuleDangVersion("v0.21.4"))
	require.Equal(t, dang.Version1_0, DefaultModuleDangVersion(engine.MinimumModuleVersion))
	// Later ones are pinned to the Dang version the engines of their version
	// evaluated them as.
	require.Equal(t, dang.Version2_1, DefaultModuleDangVersion(engine.MinimumDangV2ModuleVersion))
	require.Equal(t, dang.Version2_1, DefaultModuleDangVersion("v0.21.9"))
	require.Equal(t, dang.Version2_1, DefaultModuleDangVersion("v1.0.0-0"))
	require.Equal(t, dang.Version2_1, DefaultModuleDangVersion("v1.0.0-beta.20"))
	require.Equal(t, dang.Version2_1, DefaultModuleDangVersion("v1.0.0-beta.25"))
	// v1.0.0-beta.26 shipped Dang 2.1, and so does v1.0.0-beta.27.
	require.Equal(t, dang.Version2_1, DefaultModuleDangVersion("v1.0.0-beta.26"))
	require.Equal(t, dang.Version2_1, DefaultModuleDangVersion("v1.0.0-beta.27"))
	require.Equal(t, dang.Version2_4, DefaultModuleDangVersion(Dang24ModuleVersion))
	require.Equal(t, dang.Version2_4, DefaultModuleDangVersion(engine.Version))

	require.Equal(t, "1.0", DefaultModuleVersions("v0.20.6").DangString())
	require.Equal(t, "2.1", DefaultModuleVersions("v0.21.9").DangString())
	require.Equal(t, "2.1", DefaultModuleVersions("v1.0.0-beta.27").DangString())
	require.Equal(t, "2.4", DefaultModuleVersions(Dang24ModuleVersion).DangString())
}

// A module's Dang version follows its view, which the engine caps at its own:
// an engine before Dang24ModuleVersion serves a module declaring it, or
// v1.0.0, the view of its own version, and so Dang 2.1. A dev build of a
// version is past every prerelease gate of that version, so it serves Dang
// 2.4.
func TestDefaultModuleDangVersionCappedAtEngine(t *testing.T) {
	for _, tc := range []struct {
		engine string
		dang   dang.Version
	}{
		{engine: "v1.0.0-beta.27", dang: dang.Version2_1},
		{engine: Dang24ModuleVersion, dang: dang.Version2_4},
		{engine: "v1.0.0-dev-123", dang: dang.Version2_4},
	} {
		t.Run(tc.engine, func(t *testing.T) {
			// Not parallel: it runs as the engine of tc.engine.
			withEngineVersion(t, tc.engine)
			require.Equal(t, tc.dang, DefaultModuleDangVersion(Dang24ModuleVersion))
			require.Equal(t, tc.dang, DefaultModuleDangVersion("v1.0.0"))
			require.Equal(t, dang.Version2_1, DefaultModuleDangVersion("v1.0.0-beta.27"))
		})
	}
}

// legacyCommaSource separates top-level forms with a comma, which Dang 2.0
// accepts and Dang 2.1 does not.
const legacyCommaSource = "let legacyA = 1, let legacyB = 2\n"

func TestEntrypointRejectsInvalidDangVersion(t *testing.T) {
	t.Parallel()

	_, err := runEntrypointDirAt(t, entrypointSource(withoutSourcesMethods), "99.0")
	require.ErrorContains(t, err, "module entrypoint Dang version")
}

func TestDeclaresEntrypointSourcesAtVersion(t *testing.T) {
	t.Parallel()

	dir := writeDangFiles(t, map[string]string{
		"main.dang": entrypointSource(sourcesMember, goSDKMethods) + legacyCommaSource,
	})

	declared, err := DeclaresEntrypointSources(t.Context(), dir, "2.0")
	require.NoError(t, err)
	require.True(t, declared)

	// Parsed as the default version, the file does not parse, so it declares
	// nothing.
	declared, err = DeclaresEntrypointSources(t.Context(), dir, "")
	require.NoError(t, err)
	require.False(t, declared)

	_, err = DeclaresEntrypointSources(t.Context(), dir, "99.0")
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
