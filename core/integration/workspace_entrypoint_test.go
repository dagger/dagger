package core

import (
	"context"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestWorkspaceEntrypoint(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	original := `# keep
ignore = [
  'node_modules', # keep
]
future = true
[modules.old]
source = './old'
entrypoint = true
[modules.'tool.box']
source = './tools'
entrypoint = false
[modules.'tool.box'.as-sdk]
name = 'custom'
[env.test.modules.old.settings]
arbitrary = 'keep'
`
	// Module code is absent. Entrypoint configuration must not load it.
	base := workspaceBase(t, c).WithNewFile("dagger.toml", original)
	out, err := base.With(daggerExec("ws", "entrypoint")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "old\n", out)

	selected := base.With(daggerExec("ws", "entrypoint", "tool.box"))
	updated, err := selected.File("dagger.toml").Contents(ctx)
	require.NoError(t, err)
	expected := strings.Replace(original, "entrypoint = true\n", "", 1)
	expected = strings.Replace(expected, "entrypoint = false", "entrypoint = true", 1)
	require.Equal(t, expected, updated)
	out, err = selected.With(daggerExec("ws", "entrypoint")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "tool.box\n", out)

	unchanged, err := selected.With(daggerExec("ws", "entrypoint", "tool.box")).File("dagger.toml").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, updated, unchanged)

	cleared := selected.With(daggerExec("ws", "entrypoint", "--unset"))
	out, err = cleared.With(daggerExec("ws", "entrypoint")).Stdout(ctx)
	require.NoError(t, err)
	require.Empty(t, out)
	clearedConfig, err := cleared.File("dagger.toml").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, strings.Replace(expected, "entrypoint = true\n", "", 1), clearedConfig)
}

func (WorkspaceSuite) TestWorkspaceEntrypointErrors(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	original := `[modules.a]
source = './a'
entrypoint = true
[modules.b]
source = './b'
entrypoint = true
`
	base := workspaceBase(t, c).WithNewFile("dagger.toml", original)
	out, err := base.With(daggerExecFail("ws", "entrypoint")).CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "multiple entrypoint modules")

	for _, args := range [][]string{
		{"missing"}, {"./a"}, {"a@v2"}, {"a", "--unset"}, {"a", "b"},
	} {
		ctr := base.With(daggerExecFail(append([]string{"ws", "entrypoint"}, args...)...))
		unchanged, err := ctr.File("dagger.toml").Contents(ctx)
		require.NoError(t, err, args)
		require.Equal(t, original, unchanged, args)
	}
	// A setter repairs all existing flags in one config edit.
	repaired := base.With(daggerExec("ws", "entrypoint", "b"))
	out, err = repaired.With(daggerExec("ws", "entrypoint")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "b\n", out)
	updated, err := repaired.File("dagger.toml").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, strings.Replace(original, "entrypoint = true\n", "", 1), updated)
}

func (WorkspaceSuite) TestWorkspaceEntrypointConfigSelection(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	rootConfig := "[modules.root]\nsource = './root'\nentrypoint = true\n"
	nestedConfig := "[modules.nested]\nsource = './module'\n"
	base := workspaceBase(t, c).
		WithNewFile("dagger.toml", rootConfig).
		WithNewFile("nested/dagger.toml", nestedConfig).
		WithNewFile("nested/subdir/keep", "keep")
	selected := base.WithWorkdir("/work/nested/subdir").With(daggerExec("ws", "entrypoint", "nested"))
	out, err := selected.With(daggerExec("ws", "entrypoint")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "nested\n", out)
	unchanged, err := selected.File("/work/dagger.toml").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, rootConfig, unchanged)
	updated, err := selected.File("/work/nested/dagger.toml").Contents(ctx)
	require.NoError(t, err)
	cfg, err := workspace.ParseConfig([]byte(updated))
	require.NoError(t, err)
	require.True(t, cfg.Modules["nested"].Entrypoint)
}

func (WorkspaceSuite) TestWorkspaceEntrypointWithoutConfig(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceBase(t, c)
	out, err := base.With(daggerExec("ws", "entrypoint")).Stdout(ctx)
	require.NoError(t, err)
	require.Empty(t, out)
	cleared := base.With(daggerExec("ws", "entrypoint", "--unset"))
	_, err = cleared.Sync(ctx)
	require.NoError(t, err)
	_, err = cleared.File("dagger.toml").Contents(ctx)
	require.Error(t, err, "clearing no selection must not create a config")
	_, err = base.With(daggerExec("ws", "entrypoint", "missing")).Sync(ctx)
	require.Error(t, err)
}

func (WorkspaceSuite) TestWorkspaceEntrypointAPI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := c.Directory().
		WithNewFile("dagger.toml", "[modules.a]\nsource = './a'\nentrypoint = true\n[modules.b]\nsource = './b'\n").
		AsWorkspace()
	for _, tc := range []struct {
		name string
		ws   *dagger.Workspace
		want string
	}{
		{"select", ws.WithEntrypoint("b"), "b"},
		{"clear", ws.WithoutEntrypoint(), ""},
		{"unchanged", ws.WithEntrypoint("a"), "a"},
	} {
		data, err := tc.ws.ConfigRead(ctx)
		require.NoError(t, err, tc.name)
		cfg, err := workspace.ParseConfig([]byte(data))
		require.NoError(t, err, tc.name)
		name, err := workspace.EntrypointName(cfg)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.want, name, tc.name)
	}
	_, err := ws.WithEntrypoint("missing").ConfigRead(ctx)
	require.ErrorContains(t, err, `module "missing" is not installed`)
}
