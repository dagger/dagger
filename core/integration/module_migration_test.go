package core

import (
	"context"
	"strings"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceMigrationSuite) TestModuleMigrationWithoutWorkspace(ctx context.Context, t *testctx.T) {
	t.Run("preview and apply only the requested module", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		legacy := `{
  "name":"app", "source":"src",
  "sdk":{"source":"github.com/acme/sdk@v1","pin":"sdk-commit"},
  "dependencies":[{"name":"local","source":"../dep"},{"name":"remote","source":"github.com/acme/dep@v1","pin":"dep-commit"}],
  "codegen":{"automaticGitignore":false}, "include":["src/**"]
}`
		dep := `{"name":"dep","sdk":"dang"}`
		fixture := `{"name":"fixture","sdk":"go"}`
		base := workspaceBase(t, c).
			WithNewFile("app/dagger.json", legacy).
			WithNewFile("dep/dagger.json", dep).
			WithNewFile("fixtures/dagger.json", fixture)
		preview := base.With(daggerExec("module", "migrate", "app", "--no-apply"))
		out, err := preview.Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "app/dagger-module.toml")
		require.NotContains(t, out, "dagger module recommend")
		got, err := preview.File("app/dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, legacy, got)
		_, err = preview.File("app/dagger-module.toml").Contents(ctx)
		require.Error(t, err)

		migrated := base.With(daggerExec("module", "migrate", "app", "--auto-apply"))
		out, err = migrated.Stdout(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "dagger module recommend")
		require.NotContains(t, out, "dagger cloud checks on")
		data, err := migrated.File("app/dagger-module.toml").Contents(ctx)
		require.NoError(t, err)
		cfg, err := modules.ParseModuleConfigForFilename([]byte(data), workspace.ModuleConfigFileName)
		require.NoError(t, err)
		require.Equal(t, "app", cfg.Name)
		require.Equal(t, "src", cfg.Source)
		require.Equal(t, "github.com/acme/sdk@v1", cfg.SDK.Source)
		require.Equal(t, "sdk-commit", cfg.SDK.Pin)
		require.Len(t, cfg.Dependencies, 2)
		require.Equal(t, "../dep", cfg.Dependencies[0].Source)
		require.Equal(t, "dep-commit", cfg.Dependencies[1].Pin)
		require.NotNil(t, cfg.Codegen)
		require.NotNil(t, cfg.Codegen.AutomaticGitignore)
		require.False(t, *cfg.Codegen.AutomaticGitignore)
		require.Equal(t, []string{"src/**"}, cfg.Include)
		for file, original := range map[string]string{"dep/dagger.json": dep, "fixtures/dagger.json": fixture} {
			got, err := migrated.File(file).Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, original, got)
		}
		for _, file := range []string{"dagger.toml", "app/dagger.toml", "app/dagger.json", "dep/dagger-module.toml", "fixtures/dagger-module.toml"} {
			_, err := migrated.File(file).Contents(ctx)
			require.Error(t, err, "unexpected file %s", file)
		}
		out, err = migrated.With(daggerExec("module", "migrate", "app", "--auto-apply")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "No migration needed.")
	})

	t.Run("default target leaves its parent legacy workspace unchanged", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		parent := `{"toolchains":[{"name":"app","source":"./app"}]}`
		migrated := workspaceBase(t, c).
			WithNewFile("dagger.json", parent).
			With(legacyDangModule("app", "app", "App", "hello")).
			WithWorkdir("/work/app").
			With(daggerExec("module", "migrate", "--auto-apply"))
		got, err := migrated.File("/work/dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, parent, got)
		_, err = migrated.File("dagger-module.toml").Contents(ctx)
		require.NoError(t, err)
		for _, file := range []string{"/work/dagger.toml", "dagger.toml", "dagger.json"} {
			_, err := migrated.File(file).Contents(ctx)
			require.Error(t, err, "unexpected file %s", file)
		}
	})

	t.Run("workspace fields are not discarded without a workspace", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		legacy := `{"name":"app","sdk":"dang","toolchains":[{"name":"dep","source":"./dep"}]}`
		result := workspaceBase(t, c).
			WithNewFile("dagger.json", legacy).
			With(daggerExecFail("module", "migrate", "--auto-apply"))
		out, err := result.CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "configuration has workspace fields")
		got, err := result.File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, legacy, got)
		for _, file := range []string{"dagger.toml", "dagger-module.toml"} {
			_, err := result.File(file).Contents(ctx)
			require.Error(t, err, "unexpected file %s", file)
		}
	})
}

func (WorkspaceMigrationSuite) TestNativeWorkspaceModuleMigration(ctx context.Context, t *testctx.T) {
	t.Run("migration preserves unrelated native SDK entries", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		const nativeConfig = `[modules.local-sdk]
source = "go"

[sdks.local]
module = "local-sdk"
`
		migrated := workspaceBase(t, c).
			WithNewFile("dagger.toml", nativeConfig).
			With(legacyDangModule("app", "app", "App", "hello")).
			With(daggerExec("module", "migrate", "app", "--auto-apply"))
		data, err := migrated.File("dagger.toml").Contents(ctx)
		require.NoError(t, err)
		cfg, err := workspace.ParseConfig([]byte(data))
		require.NoError(t, err)
		require.Equal(t, "go", cfg.Modules["local-sdk"].Source)
		require.Equal(t, "local-sdk", cfg.SDKs["local"].Module)
	})

	t.Run("standalone module migration previews and applies in a native workspace", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		base := nativeWorkspaceBase(t, c).
			With(legacyDangModule("app", "app", "App", "hello"))
		preview := base.With(daggerExec("module", "migrate", "app", "--no-apply"))
		out, err := preview.Stdout(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "dagger module recommend")
		require.NotContains(t, out, "dagger cloud checks on")
		_, err = preview.File("app/dagger.json").Contents(ctx)
		require.NoError(t, err)
		_, err = preview.File("app/dagger-module.toml").Contents(ctx)
		require.Error(t, err)
		migrated := base.With(daggerExec("module", "migrate", "app", "--auto-apply"))
		data, err := migrated.File("dagger.toml").Contents(ctx)
		require.NoError(t, err)
		cfg, err := workspace.ParseConfig([]byte(data))
		require.NoError(t, err)
		require.Equal(t, "github.com/dagger/dang-sdk", cfg.Modules[cfg.SDKs["dang"].Module].Source)
		require.Equal(t, "dagger-dang-sdk", cfg.SDKs["dang"].Module)
		require.True(t, cfg.SDKs["dang"].Scopes["app"].IsModule)
		_, err = migrated.File("app/dagger-module.toml").Contents(ctx)
		require.NoError(t, err)
		_, err = migrated.File("app/dagger.json").Contents(ctx)
		require.Error(t, err)
	})

	t.Run("unchanged native preview does not offer onboarding", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		preview := nativeWorkspaceBase(t, c).With(daggerExec("workspace", "migrate", "--no-apply"))
		out, err := preview.Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "No migration needed.")
		require.NotContains(t, out, "dagger module recommend")
		require.NotContains(t, out, "dagger cloud checks on")
	})

	t.Run("fresh project migration is not initialization", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		migrated := workspaceBase(t, c).With(daggerExec("workspace", "migrate", "--auto-apply"))
		out, err := migrated.Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Run dagger init")
		require.NotContains(t, out, "dagger module recommend")
		_, err = migrated.File("dagger.toml").Contents(ctx)
		require.Error(t, err)
	})

	t.Run("explicit optional module is included in preview and apply", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		base := nativeWorkspaceBase(t, c).
			With(legacyDangModule("candidate", "candidate", "Candidate", "hello")).
			WithNewFile("fixtures/dagger.json", `{"name":"fixture","sdk":"dang"}`)
		preview := base.With(daggerExec("workspace", "migrate", "--module", "candidate", "--no-apply"))
		out, err := preview.Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "candidate/dagger-module.toml")
		require.NotContains(t, out, "dagger module recommend")
		_, err = preview.File("candidate/dagger.json").Contents(ctx)
		require.NoError(t, err)
		migrated := base.With(daggerExec("workspace", "migrate", "--module", "candidate", "--auto-apply"))
		_, err = migrated.File("candidate/dagger-module.toml").Contents(ctx)
		require.NoError(t, err)
		_, err = migrated.File("fixtures/dagger.json").Contents(ctx)
		require.NoError(t, err)
	})

	t.Run("workspace sweep migrates installed modules and leaves fixtures unchanged", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		fixture := `{"name":"fixture","sdk":"dang"}`
		base := workspaceBase(t, c).
			WithNewFile("dagger.toml", "[modules.app]\nsource = './app'\nentrypoint = true\n").
			With(legacyDangModule("app", "app", "App", "hello after migration")).
			WithNewFile("app/dagger.json", `{"name":"app","sdk":"dang","dependencies":[{"name":"dep","source":"../dep"}]}`).
			With(legacyDangModule("dep", "dep", "Dep", "dependency")).
			WithNewFile("fixtures/dagger.json", fixture)
		migrated := base.With(daggerExec("workspace", "migrate", "--auto-apply"))
		out, err := migrated.Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "dagger module recommend")
		require.Contains(t, out, "dagger cloud checks on")
		require.Contains(t, out, "dagger module migrate /fixtures")
		for _, dir := range []string{"app", "dep"} {
			_, err := migrated.File(dir + "/dagger-module.toml").Contents(ctx)
			require.NoError(t, err)
		}
		got, err := migrated.File("fixtures/dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, fixture, got)
		data, err := migrated.File("dagger.toml").Contents(ctx)
		require.NoError(t, err)
		cfg, err := workspace.ParseConfig([]byte(data))
		require.NoError(t, err)
		require.Len(t, cfg.SDKs, 1)
		require.Len(t, cfg.SDKs["dang"].Scopes, 2)
		require.Equal(t, []string{"./dep"}, cfg.SDKs["dang"].Scopes["app"].Clients)
		generated := migrated.With(daggerExec("generate", "--auto-apply"))
		out, err = generated.With(daggerCall("message")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello after migration", strings.TrimSpace(out))
	})

	t.Run("standalone module with workspace fields remains unchanged", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		legacy := `{"name":"app","sdk":"dang","toolchains":[{"name":"dep","source":"../dep"}]}`
		base := nativeWorkspaceBase(t, c).WithNewFile("app/dagger.json", legacy)
		result := base.With(daggerExecFail("module", "migrate", "app", "--auto-apply"))
		out, err := result.CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "configuration has workspace fields")
		got, err := result.File("app/dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, legacy, got)
	})
}
