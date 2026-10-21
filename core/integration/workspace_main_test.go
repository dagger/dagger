package core

import (
	"context"
	"strings"

	"dagger.io/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestWorkspaceMain(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	original := `# keep
ignore = [
  'node_modules', # keep
]
[modules.old]
source = './old'
main = true
[modules.'tool.box']
source = './tools'
main = false
[modules.'tool.box'.as-sdk]
name = 'custom'
[env.test.modules.old.settings]
arbitrary = 'keep'
`
	// Module code is absent. Main configuration must not load it.
	base := workspaceBase(t, c).WithNewFile("dagger.toml", original)
	out, err := base.With(daggerExec("ws", "main")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "old\n", out)

	selected := base.With(daggerExec("ws", "main", "tool.box"))
	updated, err := selected.File("dagger.toml").Contents(ctx)
	require.NoError(t, err)
	expected := strings.Replace(original, "main = true\n", "", 1)
	expected = strings.Replace(expected, "main = false", "main = true", 1)
	require.Equal(t, expected, updated)
	out, err = selected.With(daggerExec("ws", "main")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "tool.box\n", out)

	unchanged, err := selected.With(daggerExec("ws", "main", "tool.box")).File("dagger.toml").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, updated, unchanged)

	cleared := selected.With(daggerExec("ws", "main", "--unset"))
	out, err = cleared.With(daggerExec("ws", "main")).Stdout(ctx)
	require.NoError(t, err)
	require.Empty(t, out)
	clearedConfig, err := cleared.File("dagger.toml").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, strings.Replace(expected, "main = true\n", "", 1), clearedConfig)
}

func (WorkspaceSuite) TestWorkspaceMainErrors(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	original := `[modules.a]
source = './a'
main = true
[modules.b]
source = './b'
main = true
`
	base := workspaceBase(t, c).WithNewFile("dagger.toml", original)
	out, err := base.With(daggerExecFail("ws", "main")).CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "multiple workspace main modules")

	for _, args := range [][]string{
		{"missing"}, {"./a"}, {"a@v2"}, {"a", "--unset"}, {"a", "b"},
	} {
		ctr := base.With(daggerExecFail(append([]string{"ws", "main"}, args...)...))
		unchanged, err := ctr.File("dagger.toml").Contents(ctx)
		require.NoError(t, err, args)
		require.Equal(t, original, unchanged, args)
	}
	// A setter repairs all existing flags in one config edit.
	repaired := base.With(daggerExec("ws", "main", "b"))
	out, err = repaired.With(daggerExec("ws", "main")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "b\n", out)
	updated, err := repaired.File("dagger.toml").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, strings.Replace(original, "main = true\n", "", 1), updated)
}

func (WorkspaceSuite) TestWorkspaceMainConfigSelection(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	rootConfig := "[modules.root]\nsource = './root'\nmain = true\n"
	nestedConfig := "[modules.nested]\nsource = './module'\n"
	base := workspaceBase(t, c).
		WithNewFile("dagger.toml", rootConfig).
		WithNewFile("nested/dagger.toml", nestedConfig).
		WithNewFile("nested/subdir/keep", "keep")
	selected := base.WithWorkdir("/work/nested/subdir").With(daggerExec("ws", "main", "nested"))
	out, err := selected.With(daggerExec("ws", "main")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "nested\n", out)
	unchanged, err := selected.File("/work/dagger.toml").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, rootConfig, unchanged)
	updated, err := selected.File("/work/nested/dagger.toml").Contents(ctx)
	require.NoError(t, err)
	cfg, err := workspace.ParseConfig([]byte(updated))
	require.NoError(t, err)
	require.True(t, cfg.Modules["nested"].Main)
}

func (WorkspaceSuite) TestWorkspaceMainWithoutConfig(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceBase(t, c)
	out, err := base.With(daggerExec("ws", "main")).Stdout(ctx)
	require.NoError(t, err)
	require.Empty(t, out)
	cleared := base.With(daggerExec("ws", "main", "--unset"))
	_, err = cleared.Sync(ctx)
	require.NoError(t, err)
	_, err = cleared.File("dagger.toml").Contents(ctx)
	require.Error(t, err, "clearing no selection must not create a config")
	_, err = base.With(daggerExec("ws", "main", "missing")).Sync(ctx)
	require.Error(t, err)
}

func (WorkspaceSuite) TestWorkspaceMainAPI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", "[modules.a]\nsource = './a'\nmain = true\n[modules.b]\nsource = './b'\n").
		AsWorkspace()
	for _, tc := range []struct {
		name string
		ws   *core.Workspace
		want string
	}{
		{"select", ws.WithMain("b"), "b"},
		{"clear", ws.WithoutMain(), ""},
		{"unchanged", ws.WithMain("a"), "a"},
	} {
		data, err := tc.ws.ConfigRead(ctx)
		require.NoError(t, err, tc.name)
		cfg, err := workspace.ParseConfig([]byte(data))
		require.NoError(t, err, tc.name)
		name, err := workspace.MainName(cfg)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.want, name, tc.name)
		name, err = tc.ws.Main(ctx)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.want, name, tc.name)
	}
	_, err := core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", "[modules.a]\nsource = './a'\nmain = true\n[modules.b]\nsource = './b'\nmain = true\n").
		AsWorkspace().
		Main(ctx)
	require.ErrorContains(t, err, "multiple workspace main modules")
	_, err = ws.WithMain("missing").ConfigRead(ctx)
	require.ErrorContains(t, err, `module "missing" is not installed`)
}
