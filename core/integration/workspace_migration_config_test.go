package core

import (
	"context"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceMigrationSuite) TestWorkspaceMigrateNativeConfig(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	original := `# workspace
ignore = [
  'node_modules', # keep
]
future = true

[modules.app]
source = './app'

[modules.dagger-custom-sdk]
source = './sdk' # keep

[modules.dagger-custom-sdk.as-sdk]
name = 'custom'
[[modules.dagger-custom-sdk.as-sdk.modules]]
path = './app'
[[modules.dagger-custom-sdk.as-sdk.clients]]
path = './client'
module = './app'
package = 'bindings'
`
	base := workspaceBase(t, c).
		WithNewFile("dagger.toml", original).
		WithNewFile("sdk/dagger-module.toml", "name = 'custom-sdk'\n").
		WithNewFile("app/dagger.json", `{"name":"app","sdk":"../sdk"}`).
		WithNewFile("child/dagger.json", `{"name":"child","sdk":{"source":"go"}}`).
		WithNewFile("child/main.go", "package main\ntype Child struct{}\n")

	t.Run("required module conflict leaves all files unchanged", func(ctx context.Context, t *testctx.T) {
		legacy := `{"name":"app","sdk":"go"}`
		failed := base.WithNewFile("app/dagger.json", legacy).
			With(daggerExecFail("ws", "migrate", "--auto-apply"))
		out, err := failed.CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "belongs to SDK")
		data, err := failed.File("dagger.toml").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, original, data)
		data, err = failed.File("app/dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, legacy, data)
	})

	preview := base.With(daggerExec("ws", "migrate", "--no-apply"))
	out, err := preview.CombinedOutput(ctx)
	require.NoError(t, err, out)
	require.Contains(t, out, "dagger.toml")
	unchanged, err := preview.File("dagger.toml").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, original, unchanged)
	legacyApp, err := preview.File("app/dagger.json").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, `{"name":"app","sdk":"../sdk"}`, legacyApp)

	applied := preview.With(daggerExec("ws", "migrate", "--auto-apply"))
	out, err = applied.CombinedOutput(ctx)
	require.NoError(t, err, out)
	updated, err := applied.File("dagger.toml").Contents(ctx)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(updated, strings.Split(original, "[modules.dagger-custom-sdk.as-sdk]")[0]))
	require.NotContains(t, updated, "as-sdk")
	cfg, err := workspace.ParseConfig([]byte(updated))
	require.NoError(t, err)
	require.Equal(t, "dagger-custom-sdk", cfg.SDKs["custom"].Module)
	require.True(t, cfg.SDKs["custom"].Scopes["./app"].IsModule)
	require.Equal(t, []string{"./app"}, cfg.SDKs["custom"].Scopes["./client"].Clients)
	require.Equal(t, map[string]any{"package": "bindings"}, cfg.SDKs["custom"].Scopes["./client"].Settings)
	nativeApp, err := applied.File("app/dagger-module.toml").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, nativeApp, `name = "app"`)
	_, err = applied.File("app/dagger.json").Contents(ctx)
	require.Error(t, err)
	legacyChild, err := applied.File("child/dagger.json").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, `{"name":"child","sdk":{"source":"go"}}`, legacyChild)

	again := applied.With(daggerExec("ws", "migrate", "--auto-apply"))
	repeated, err := again.File("dagger.toml").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, updated, repeated)
	empty, err := again.WithExec([]string{"dagger", "query"}, dagger.ContainerWithExecOpts{
		Stdin:                         `{currentWorkspace { migrate { changes { isEmpty } } }}`,
		ExperimentalPrivilegedNesting: true,
	}).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, empty, `"isEmpty": true`)
}

func (WorkspaceMigrationSuite) TestWorkspaceUnsupportedConfigWarning(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	original := `[modules.provider]
source = './sdk'
future = true
[modules.provider.as-sdk]
name = 'custom'
[modules.provider.settings]
arbitrary = { nested = true }
`
	ctr := workspaceBase(t, c).
		WithNewFile("dagger.toml", original).
		With(daggerExec("ws", "config"))
	out, err := ctr.CombinedOutput(ctx)
	require.NoError(t, err, out)
	require.Contains(t, out, "dagger.toml:4:1: unsupported field modules.provider.as-sdk is ignored")
	require.Contains(t, out, "dagger ws migrate")
	require.Contains(t, out, "unsupported field modules.provider.future")
	require.NotContains(t, out, "unsupported field modules.provider.settings")
	unchanged, err := ctr.File("dagger.toml").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, original, unchanged)
}

// TestWorkspaceMigrateSDKScopeOnlyModule covers a module that the workspace
// reaches only through an SDK's module list: no [modules.*] install entry
// points at it and no installed module depends on it. Migration must still
// convert it and record its name and clients on the scope.
func (WorkspaceMigrationSuite) TestWorkspaceMigrateSDKScopeOnlyModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	original := `[modules.dagger-custom-sdk]
source = './sdk'

[modules.dagger-custom-sdk.as-sdk]
name = 'custom'
[[modules.dagger-custom-sdk.as-sdk.modules]]
path = './tools/reporter'
`
	applied := workspaceBase(t, c).
		WithNewFile("dagger.toml", original).
		WithNewFile("sdk/dagger-module.toml", "name = 'custom-sdk'\n").
		WithNewFile("tools/reporter/dagger.json", `{"name":"reporter","sdk":"../../sdk","dependencies":[{"name":"helper","source":"../helper"}]}`).
		WithNewFile("tools/helper/dagger.json", `{"name":"helper","sdk":"../../sdk"}`).
		With(daggerExec("ws", "migrate", "--auto-apply"))

	out, err := applied.CombinedOutput(ctx)
	require.NoError(t, err, out)

	updated, err := applied.File("dagger.toml").Contents(ctx)
	require.NoError(t, err)
	require.NotContains(t, updated, "as-sdk")
	cfg, err := workspace.ParseConfig([]byte(updated))
	require.NoError(t, err)
	scope := cfg.SDKs["custom"].Scopes["./tools/reporter"]
	require.True(t, scope.IsModule)
	require.Equal(t, "reporter", scope.Name, updated)
	require.Equal(t, []string{"./tools/helper"}, scope.Clients, updated)

	native, err := applied.File("tools/reporter/dagger-module.toml").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, native, `name = "reporter"`)
	_, err = applied.File("tools/reporter/dagger.json").Contents(ctx)
	require.Error(t, err)
	_, err = applied.File("tools/helper/dagger-module.toml").Contents(ctx)
	require.NoError(t, err)
}
