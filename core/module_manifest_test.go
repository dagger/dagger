package core

import (
	"testing"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestModuleManifestV1Contents(t *testing.T) {
	t.Parallel()

	manifest := (&ModuleManifestV1{
		Name:          "payments",
		EngineVersion: engine.Version,
		Source:        ".",
	}).
		WithRuntime("github.com/dagger/go-runtime").
		WithSource("src").
		WithInclude("**/*.go").
		WithInclude("go.mod")

	contents, err := manifest.Contents()
	require.NoError(t, err)

	cfg, err := modules.ParseModuleConfigForFilename(contents, modules.Filename)
	require.NoError(t, err)
	require.Equal(t, "payments", cfg.Name)
	require.Equal(t, engine.Version, cfg.EngineVersion)
	require.Equal(t, "src", cfg.Source)
	require.Equal(t, "github.com/dagger/go-runtime", cfg.SDK.Source)
	require.Equal(t, []string{"**/*.go", "go.mod"}, cfg.Include)
}

func TestModuleManifestV1BuilderIsImmutable(t *testing.T) {
	t.Parallel()

	base := &ModuleManifestV1{Name: "payments", Source: "."}
	configured := base.WithRuntime("go").WithInclude("go.mod")

	require.Empty(t, base.Runtime)
	require.Empty(t, base.Include)
	require.Equal(t, "go", configured.Runtime)
	require.Equal(t, []string{"go.mod"}, configured.Include)
}

func TestModuleManifestV1ContentsRequiresIdentity(t *testing.T) {
	t.Parallel()

	_, err := (&ModuleManifestV1{Runtime: "go"}).Contents()
	require.ErrorContains(t, err, "name is required")

	_, err = (&ModuleManifestV1{Name: "payments"}).Contents()
	require.ErrorContains(t, err, "runtime is required")
}

func TestModuleManifestV2Contents(t *testing.T) {
	t.Parallel()

	manifest := (&ModuleManifestV2{Name: "hello"}).
		WithDangEntrypoint("./internal/dagger/entrypoint")

	contents, err := manifest.Contents()
	require.NoError(t, err)
	require.Equal(t, `manifestVersion = 2
name = "hello"

[entrypoint]
  kind = "dang"
  source = "./internal/dagger/entrypoint"
`, string(contents))
}

func TestModuleManifestV2EntrypointSetterReplacesPreviousValue(t *testing.T) {
	t.Parallel()

	base := &ModuleManifestV2{Name: "hello"}
	dang := base.WithDangEntrypoint("./main.dang")
	module := dang.WithModuleEntrypoint("./entrypoint")

	require.Empty(t, base.EntrypointKind)
	require.Equal(t, "dang", dang.EntrypointKind)
	require.Equal(t, "./main.dang", dang.EntrypointSource)
	require.Equal(t, "module", module.EntrypointKind)
	require.Equal(t, "./entrypoint", module.EntrypointSource)
}

func TestModuleManifestV2ContentsRequiresEntrypoint(t *testing.T) {
	t.Parallel()

	_, err := (&ModuleManifestV2{Name: "hello"}).Contents()
	require.ErrorContains(t, err, "entrypoint is required")

	_, err = (&ModuleManifestV2{Name: "hello", EntrypointKind: "other", EntrypointSource: "."}).Contents()
	require.ErrorContains(t, err, "kind must be dang or module")
}
