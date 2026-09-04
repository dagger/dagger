package core

import (
	"testing"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/sdk/sdkmeta"
	"github.com/stretchr/testify/require"
)

func TestModuleManifestTOMLContents(t *testing.T) {
	t.Parallel()

	manifest := (&ModuleManifest{Name: "payments"}).
		WithDangEntrypoint("./internal/dagger/main.dang").
		WithLegacyGoRuntime("./src", "v0.20.3").
		WithLegacyInclude("**/*.go").
		WithLegacyInclude("go.mod")

	contents, err := manifest.TOMLContents()
	require.NoError(t, err)
	require.Equal(t, `name = "payments"
engineVersion = "v0.20.3"
include = ["**/*.go", "go.mod"]
source = "./src"

[runtime]
  source = "go"

[entrypoint]
  kind = "dang"
  source = "./internal/dagger/main.dang"
`, string(contents))
	require.NotContains(t, string(contents), "manifestVersion")
}

func TestModuleManifestLegacyJSONContents(t *testing.T) {
	t.Parallel()

	manifest := (&ModuleManifest{Name: "payments"}).
		WithModuleEntrypoint("./entrypoint").
		WithLegacyPythonRuntime("./src", "v0.20.3").
		WithLegacyInclude("src/**")

	contents, err := manifest.LegacyJSONContents()
	require.NoError(t, err)

	cfg, err := modules.ParseModuleConfigForFilename(contents, modules.LegacyFilename)
	require.NoError(t, err)
	require.Equal(t, "payments", cfg.Name)
	require.Equal(t, "v0.20.3", cfg.EngineVersion)
	require.Equal(t, "python", cfg.SDK.Source)
	require.Equal(t, "./src", cfg.Source)
	require.Equal(t, []string{"src/**"}, cfg.Include)
	require.NotContains(t, string(contents), "entrypoint")
}

func TestModuleManifestLegacyRuntimes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		configure func(*ModuleManifest, string, string) *ModuleManifest
		runtime   string
	}{
		{name: "go", configure: (*ModuleManifest).WithLegacyGoRuntime, runtime: "go"},
		{name: "dang", configure: (*ModuleManifest).WithLegacyDangRuntime, runtime: "dang"},
		{name: "python", configure: (*ModuleManifest).WithLegacyPythonRuntime, runtime: "python"},
		{name: "typescript", configure: (*ModuleManifest).WithLegacyTypescriptRuntime, runtime: "typescript"},
		{name: "php", configure: (*ModuleManifest).WithLegacyPHPRuntime, runtime: "php"},
		{name: "elixir", configure: (*ModuleManifest).WithLegacyElixirRuntime, runtime: "elixir"},
		{name: "java", configure: (*ModuleManifest).WithLegacyJavaRuntime, runtime: "java"},
	}
	runtimes := make([]string, 0, len(tests))
	for _, test := range tests {
		runtimes = append(runtimes, test.runtime)
	}
	require.ElementsMatch(t, sdkmeta.Builtins, runtimes)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			manifest := test.configure(&ModuleManifest{Name: "payments"}, "", "")
			require.Equal(t, test.runtime, manifest.LegacyRuntime)
			require.Empty(t, manifest.LegacyModuleSource)
			require.NotEmpty(t, manifest.LegacyEngineVersion)
			require.NoError(t, manifest.Validate(""))
		})
	}
}

func TestModuleManifestBuilderIsImmutable(t *testing.T) {
	t.Parallel()

	empty := &ModuleManifest{}
	base := empty.WithName("payments")
	entrypoint := base.WithDangEntrypoint("./main.dang")
	fat := entrypoint.WithLegacyGoRuntime("./src", "v0.20.3").WithLegacyInclude("go.mod")
	fat = fat.WithLegacyRuntimeDependency("dep", "./dep", "")
	current := fat.WithoutLegacyFields()

	require.Empty(t, empty.Name)
	require.Equal(t, "payments", base.Name)
	require.Empty(t, base.EntrypointKind)
	require.Empty(t, entrypoint.LegacyRuntime)
	require.Equal(t, "go", fat.LegacyRuntime)
	require.Equal(t, []string{"go.mod"}, fat.LegacyInclude)
	require.Empty(t, current.LegacyRuntime)
	require.Empty(t, current.LegacyModuleSource)
	require.Empty(t, current.LegacyEngineVersion)
	require.Empty(t, current.LegacyInclude)
	require.Empty(t, current.LegacyRuntimeDependencies)
	require.Equal(t, "dang", current.EntrypointKind)
}

func TestModuleManifestLoadsAndUpdatesBothFormats(t *testing.T) {
	t.Parallel()

	manifest := &ModuleManifest{}
	require.NoError(t, manifest.LoadJSON([]byte(`{
  "name": "legacy-name",
  "engineVersion": "v0.20.3",
  "sdk": {
    "source": "go",
    "config": {"legacy": true}
  },
  "dependencies": [
    {"name": "stale", "source": "github.com/example/stale"}
  ]
}`)))
	require.NoError(t, manifest.LoadTOML([]byte(`name = "current-name"
source = "."
engineVersion = "v0.20.3"

[runtime]
  source = "go"

[[dependencies]]
  name = "keep"
  source = "github.com/example/keep"
  pin = "sha256:old"
`)))

	withoutKeep := manifest.WithoutLegacyRuntimeDependency("keep")
	cleared := manifest.WithoutLegacyRuntimeDependencies()
	updated := cleared.
		WithName("updated-name").
		WithLegacyRuntimeDependency("client", "./client", "sha256:new")

	require.Equal(t, "current-name", manifest.Name)
	require.Equal(t, "keep", manifest.LegacyRuntimeDependencies[0].Name)
	require.Empty(t, withoutKeep.LegacyRuntimeDependencies)
	require.Empty(t, cleared.LegacyRuntimeDependencies)
	for filename, contents := range map[string][]byte{
		modules.Filename:       mustModuleManifestTOMLContents(t, updated),
		modules.LegacyFilename: mustModuleManifestJSONContents(t, updated),
	} {
		cfg, err := modules.ParseModuleConfigForFilename(contents, filename)
		require.NoError(t, err)
		require.Equal(t, "updated-name", cfg.Name)
		require.Equal(t, ".", cfg.Source)
		require.Equal(t, "go", cfg.SDK.Source)
		require.Len(t, cfg.Dependencies, 1)
		require.Equal(t, "client", cfg.Dependencies[0].Name)
		require.Equal(t, "./client", cfg.Dependencies[0].Source)
		require.Equal(t, "sha256:new", cfg.Dependencies[0].Pin)
	}

	legacyContents := mustModuleManifestJSONContents(t, updated)
	require.Contains(t, string(legacyContents), `"config"`)
	require.Contains(t, string(legacyContents), `"legacy": true`)
}

func TestModuleManifestLoadsExternalRuntime(t *testing.T) {
	t.Parallel()

	manifest := &ModuleManifest{}
	require.NoError(t, manifest.LoadJSON([]byte(`{
  "name": "external-runtime",
  "engineVersion": "v0.20.3",
  "sdk": "github.com/example/sdk@v1.2.3"
}`)))

	contents, err := manifest.LegacyJSONContents()
	require.NoError(t, err)
	require.Contains(t, string(contents), "github.com/example/sdk@v1.2.3")
}

func mustModuleManifestTOMLContents(t *testing.T, manifest *ModuleManifest) []byte {
	t.Helper()
	contents, err := manifest.TOMLContents()
	require.NoError(t, err)
	return contents
}

func mustModuleManifestJSONContents(t *testing.T, manifest *ModuleManifest) []byte {
	t.Helper()
	contents, err := manifest.LegacyJSONContents()
	require.NoError(t, err)
	return contents
}

func TestModuleManifestLegacyRuntimeSetterReplacesPreviousValues(t *testing.T) {
	t.Parallel()

	goRuntime := (&ModuleManifest{Name: "hello"}).WithLegacyGoRuntime("./go", "v0.20.3")
	pythonRuntime := goRuntime.WithLegacyPythonRuntime("./python", "v0.21.0")
	defaultedRuntime := pythonRuntime.WithLegacyDangRuntime("", "")

	require.Equal(t, "go", goRuntime.LegacyRuntime)
	require.Equal(t, "./go", goRuntime.LegacyModuleSource)
	require.Equal(t, "v0.20.3", goRuntime.LegacyEngineVersion)
	require.Equal(t, "python", pythonRuntime.LegacyRuntime)
	require.Equal(t, "./python", pythonRuntime.LegacyModuleSource)
	require.Equal(t, "v0.21.0", pythonRuntime.LegacyEngineVersion)
	require.Equal(t, "dang", defaultedRuntime.LegacyRuntime)
	require.Empty(t, defaultedRuntime.LegacyModuleSource)
	require.NotEmpty(t, defaultedRuntime.LegacyEngineVersion)
}

func TestModuleManifestEntrypointSetterReplacesPreviousValue(t *testing.T) {
	t.Parallel()

	base := &ModuleManifest{Name: "hello"}
	dang := base.WithDangEntrypoint("./main.dang")
	module := dang.WithModuleEntrypoint("./entrypoint")

	require.Empty(t, base.EntrypointKind)
	require.Equal(t, "dang", dang.EntrypointKind)
	require.Equal(t, "./main.dang", dang.EntrypointSource)
	require.Equal(t, "module", module.EntrypointKind)
	require.Equal(t, "./entrypoint", module.EntrypointSource)
}

func TestModuleManifestValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		manifest *ModuleManifest
		target   string
		wantErr  string
	}{
		{
			name:     "name",
			manifest: (&ModuleManifest{}).WithLegacyGoRuntime("", ""),
			wantErr:  "name is required",
		},
		{
			name:     "loading path",
			manifest: &ModuleManifest{Name: "hello"},
			wantErr:  "requires an entrypoint or legacy runtime",
		},
		{
			name:     "partial entrypoint",
			manifest: &ModuleManifest{Name: "hello", EntrypointKind: "dang"},
			wantErr:  "kind and source must be set together",
		},
		{
			name:     "entrypoint kind",
			manifest: &ModuleManifest{Name: "hello", EntrypointKind: "other", EntrypointSource: "."},
			wantErr:  "kind must be dang or module",
		},
		{
			name: "legacy runtime",
			manifest: &ModuleManifest{
				Name:                "hello",
				LegacyRuntime:       "rust",
				LegacyEngineVersion: "v0.20.0",
			},
			wantErr: `legacy runtime "rust" is not supported`,
		},
		{
			name: "legacy engine version",
			manifest: &ModuleManifest{
				Name:          "hello",
				LegacyRuntime: "go",
			},
			wantErr: "legacy engine version is required",
		},
		{
			name:     "orphaned legacy field",
			manifest: (&ModuleManifest{Name: "hello"}).WithDangEntrypoint(".").WithLegacyInclude("src/**"),
			wantErr:  "legacy runtime is required",
		},
		{
			name:     "orphaned legacy runtime dependency",
			manifest: (&ModuleManifest{Name: "hello"}).WithDangEntrypoint(".").WithLegacyRuntimeDependency("dep", "./dep", ""),
			wantErr:  "legacy runtime is required",
		},
		{
			name: "orphaned legacy module source",
			manifest: &ModuleManifest{
				Name:               "hello",
				EntrypointKind:     "dang",
				EntrypointSource:   ".",
				LegacyModuleSource: "./src",
			},
			wantErr: "legacy runtime is required",
		},
		{
			name:     "target engine version",
			manifest: (&ModuleManifest{Name: "hello"}).WithLegacyGoRuntime("", "v1.2.0"),
			target:   "v1.1.0",
			wantErr:  "requires dagger v1.2.0, but target engine is v1.1.0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.manifest.Validate(test.target)
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestModuleManifestLegacyJSONRequiresLegacyRuntime(t *testing.T) {
	t.Parallel()

	manifest := (&ModuleManifest{Name: "hello"}).WithDangEntrypoint("./main.dang")
	_, err := manifest.LegacyJSONContents()
	require.ErrorContains(t, err, "has no legacy runtime")
}
