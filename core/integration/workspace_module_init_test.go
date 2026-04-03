package core

import (
	"context"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func initWorkspaceDangModule(ctx context.Context, t *testctx.T, ctr *dagger.Container, name string) *dagger.Container {
	t.Helper()

	initCtr := ctr.WithExec([]string{"dagger", "module", "init", "--sdk=dang", "--name=" + name}, dagger.ContainerWithExecOpts{
		Expect:                        dagger.ReturnTypeAny,
		ExperimentalPrivilegedNesting: true,
	})
	initCode, err := initCtr.ExitCode(ctx)
	require.NoError(t, err)
	initOut, err := initCtr.Stdout(ctx)
	require.NoError(t, err)
	initErr, err := initCtr.Stderr(ctx)
	require.NoError(t, err)
	require.Zero(t, initCode, "stdout:\n%s\nstderr:\n%s", initOut, initErr)
	require.Contains(t, initOut, `Created module "`+name+`"`)
	require.Contains(t, initOut, `.dagger/modules/`+name)

	return initCtr
}

func (WorkspaceSuite) TestWorkspaceModuleInitCommand(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceBase(t, c)

	t.Run("initialized workspace creates a config-owned module", func(ctx context.Context, t *testctx.T) {
		ctr := initWorkspaceDangModule(ctx, t, base.With(daggerExec("workspace", "init")), "mymod").
			WithNewFile(".dagger/modules/mymod/main.dang", `
type Mymod {
  pub greet: String! {
    "hello workspace"
  }
}
`)

		djson, err := ctr.WithExec([]string{"cat", ".dagger/modules/mymod/dagger.json"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, djson, `"name": "mymod"`)

		cfg, err := ctr.WithExec([]string{"cat", ".dagger/config.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, cfg, `source = "modules/mymod"`)

		_, err = ctr.WithExec([]string{"test", "!", "-f", ".dagger/modules/mymod/LICENSE"}).Sync(ctx)
		require.NoError(t, err)

		_, err = ctr.WithExec([]string{"test", "!", "-f", "dagger.json"}).Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("workspace root loads config-owned modules", func(ctx context.Context, t *testctx.T) {
		ctr := initWorkspaceDangModule(ctx, t, base.With(daggerExec("workspace", "init")), "mymod").
			WithNewFile(".dagger/modules/mymod/main.dang", `
type Mymod {
  pub greet: String! {
    "hello workspace"
  }
}
`)

		out, err := ctr.With(daggerFunctions()).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "mymod")

		out, err = ctr.With(daggerCall("mymod", "greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello workspace", strings.TrimSpace(out))
	})

	t.Run("explicit path keeps standalone init inside a workspace", func(ctx context.Context, t *testctx.T) {
		ctr := base.
			With(daggerExec("workspace", "init")).
			WithExec([]string{"mkdir", "-p", "submod"}).
			With(daggerExec("module", "init", "--sdk=dang", "--name=standalone", "./submod")).
			WithNewFile("submod/main.dang", `
type Standalone {
  pub greet: String! {
    "hello standalone"
  }
}
`)

		djson, err := ctr.WithExec([]string{"cat", "submod/dagger.json"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, djson, `"name": "standalone"`)

		cfg, err := ctr.WithExec([]string{"cat", ".dagger/config.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.NotContains(t, cfg, "standalone")

		_, err = ctr.WithExec([]string{"test", "!", "-f", ".dagger/modules/standalone/dagger.json"}).Sync(ctx)
		require.NoError(t, err)

		out, err := ctr.With(daggerCallAt("./submod", "greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello standalone", strings.TrimSpace(out))
	})
}
