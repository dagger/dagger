package core

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (GeneratorsSuite) TestSDKModuleInitControls(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	sdkPath, err := filepath.Abs("testdata/sdks/module-max-workspace-writer")
	require.NoError(t, err)
	const prefix = `# preserve this list
ignore = [
  'node_modules', # keep
]
future = true
`
	const config = prefix + `[modules.writer]
source = '../sdk'

[sdks.test]
module = 'writer'
`
	base := goGitBase(t, c).
		WithDirectory("/work/sdk", c.Host().Directory(sdkPath)).
		WithNewFile("/work/app/dagger.toml", config).
		WithWorkdir("/work/app").
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", testCLIBinPath).
		With(nonNestedDevEngine(c))
	for _, tc := range []struct {
		name                                 string
		args                                 []string
		moduleName, source, message          string
		installed, entrypoint, oldEntrypoint bool
	}{
		{name: "defaults", moduleName: "app-dev", source: "../generated/app-dev", installed: true, entrypoint: true, message: `Automatically installed module "app-dev" as entrypoint.`},
		{name: "explicit name", args: []string{"--name=demo"}, moduleName: "demo", source: "../generated/demo", installed: true, message: `Automatically installed module "demo".`},
		{name: "explicit path", args: []string{"--path=custom"}, moduleName: "custom", source: "custom", message: "Initialized module custom\nCustom path; module was not installed."},
		{name: "install custom path", args: []string{"--path=custom", "--install"}, moduleName: "custom", source: "custom", installed: true, message: `Installed module "custom".`},
		{name: "explicit install implicit entrypoint", args: []string{"--install"}, moduleName: "app-dev", source: "../generated/app-dev", installed: true, entrypoint: true, message: "Installed module \"app-dev\".\nAutomatically selected module \"app-dev\" as entrypoint."},
		{name: "disable install", args: []string{"--install=false"}, moduleName: "app-dev", source: "../generated/app-dev", message: "Initialized module \"app-dev\".\nModule was not installed."},
		{name: "disable entrypoint", args: []string{"--entrypoint=false"}, moduleName: "app-dev", source: "../generated/app-dev", installed: true, message: `Automatically installed module "app-dev".`},
		{name: "replace entrypoint", args: []string{"--path=custom", "--name=demo", "--entrypoint"}, moduleName: "demo", source: "custom", installed: true, entrypoint: true, oldEntrypoint: true, message: `Installed module "demo" as entrypoint.`},
		{name: "named init keeps entrypoint", args: []string{"--name=demo"}, moduleName: "demo", source: "../generated/demo", installed: true, oldEntrypoint: true, message: `Automatically installed module "demo".`},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			ctr := base
			if tc.oldEntrypoint {
				ctr = ctr.WithNewFile("/work/app/dagger.toml", config+"\n[modules.old]\nsource = '../sdk'\nentrypoint = true\n")
			}
			initialized := ctr.With(daggerNonNestedExec(append([]string{"mod", "init", "test", "-y"}, tc.args...)...))
			out, err := initialized.CombinedOutput(ctx)
			require.NoError(t, err, out)
			require.Contains(t, out, tc.message)
			updated, err := initialized.File("/work/app/dagger.toml").Contents(ctx)
			require.NoError(t, err)
			require.True(t, strings.HasPrefix(updated, prefix), updated)
			cfg, err := workspace.ParseConfig([]byte(updated))
			require.NoError(t, err)
			entry, installed := cfg.Modules[tc.moduleName]
			require.Equal(t, tc.installed, installed)
			require.Equal(t, tc.entrypoint, entry.Entrypoint)
			if installed {
				require.Equal(t, tc.source, entry.Source)
			}
			if tc.oldEntrypoint {
				require.Equal(t, !tc.entrypoint, cfg.Modules["old"].Entrypoint)
			}
			scope := cfg.SDKs["test"].Scopes[tc.source]
			require.True(t, scope.IsModule)
			moduleConfig, err := initialized.File(filepath.Join("/work/app", tc.source, "dagger-module.toml")).Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, moduleConfig, `name = "`+tc.moduleName+`"`)
		})
	}
	t.Run("API distinguishes omitted and false controls", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []struct {
			args                  string
			installed, entrypoint bool
		}{
			{installed: true, entrypoint: true},
			{args: ", install: false"},
			{args: ", entrypoint: false", installed: true},
		} {
			query := `query { currentWorkspace { withInitModule(sdk: "test"` + tc.args + `) { configRead } } }`
			queried := base.WithNewFile("/query.graphql", query).With(daggerNonNestedExec("api", "query", "--doc=/query.graphql"))
			out, err := queried.Stdout(ctx)
			require.NoError(t, err, out)
			var result struct {
				CurrentWorkspace struct{ WithInitModule struct{ ConfigRead string } }
			}
			require.NoError(t, json.Unmarshal([]byte(out), &result))
			cfg, err := workspace.ParseConfig([]byte(result.CurrentWorkspace.WithInitModule.ConfigRead))
			require.NoError(t, err)
			entry, installed := cfg.Modules["app-dev"]
			require.Equal(t, tc.installed, installed, tc.args)
			require.Equal(t, tc.entrypoint, entry.Entrypoint, tc.args)
			unchanged, err := queried.File("/work/app/dagger.toml").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, config, unchanged)
		}
	})
	t.Run("controls keep an existing scope name", func(ctx context.Context, t *testctx.T) {
		initialized := base.With(daggerNonNestedExec("mod", "init", "test", "--path=custom", "--name=saved", "-y"))
		repeated := initialized.With(daggerNonNestedExec("mod", "init", "test", "--path=custom", "--install=false", "-y"))
		moduleConfig, err := repeated.File("/work/app/custom/dagger-module.toml").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, moduleConfig, `name = "saved"`)
	})
	t.Run("rejected controls leave files unchanged", func(ctx context.Context, t *testctx.T) {
		for _, args := range [][]string{
			{"--install=false", "--entrypoint"},
			{}, {"--install"},
		} {
			original := config + "\n[modules.old]\nsource = '../sdk'\nentrypoint = true\n"
			failed := base.WithNewFile("/work/app/dagger.toml", original).
				With(daggerNonNestedExecFail(append([]string{"mod", "init", "test", "-y"}, args...)...))
			out, err := failed.CombinedOutput(ctx)
			require.NoError(t, err, out)
			if len(args) == 2 {
				require.Contains(t, out, "--install=false cannot be combined with --entrypoint")
			} else {
				require.Contains(t, out, `workspace already has entrypoint module "old"`)
			}
			require.NotContains(t, out, "installed module")
			updated, err := failed.File("/work/app/dagger.toml").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, original, updated)
			exists, err := failed.Exists(ctx, "/work/generated/app-dev/dagger-module.toml")
			require.NoError(t, err)
			require.False(t, exists)
		}
	})
	t.Run("preview has no success message or writes", func(ctx context.Context, t *testctx.T) {
		preview := base.With(daggerNonNestedExec("mod", "init", "test", "--no-apply"))
		out, err := preview.CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "Generated changes were not applied (--no-apply).")
		require.NotContains(t, out, "Automatically installed")
		updated, err := preview.File("/work/app/dagger.toml").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, config, updated)
		exists, err := preview.Exists(ctx, "/work/generated/app-dev/dagger-module.toml")
		require.NoError(t, err)
		require.False(t, exists)
	})
	t.Run("controls retain inferred name when SDK chooses a different basename", func(ctx context.Context, t *testctx.T) {
		source, err := c.Host().File(filepath.Join(sdkPath, "main.dang")).Contents(ctx)
		require.NoError(t, err)
		for _, flag := range []string{"--install=false", "--entrypoint=false"} {
			initialized := base.WithNewFile("/work/sdk/main.dang", strings.Replace(source, `"generated/" + name`, `"generated/api"`, 1)).
				With(daggerNonNestedExec("mod", "init", "test", flag, "-y"))
			moduleConfig, err := initialized.File("/work/generated/api/dagger-module.toml").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, moduleConfig, `name = "app-dev"`)
			regenerated := initialized.With(daggerNonNestedExec("generate", "-y"))
			moduleConfig, err = regenerated.File("/work/generated/api/dagger-module.toml").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, moduleConfig, `name = "app-dev"`)
		}
	})
}
