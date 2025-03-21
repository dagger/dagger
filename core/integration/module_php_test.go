package core

import (
	"context"
	"path/filepath"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type PHPSuite struct{}

func TestPHP(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(PHPSuite{})
}

func (PHPSuite) TestInit(ctx context.Context, t *testctx.T) {
	t.Run("from local", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		sdkSrc, err := filepath.Abs("../../sdk/php/")
		require.NoError(t, err)

		out, err := goGitBase(t, c).
			WithDirectory("/work/sdk/php", c.Host().Directory(sdkSrc)).
			With(daggerExec(
				"init",
				"--name=bare",
				"--sdk=./sdk/php")).
			With(daggerCall("container-echo", "--string-arg", "hello", "stdout")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})

	t.Run("from upstream", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(daggerExec(
				"init",
				"--name=bare",
				"--sdk=github.com/dagger/dagger/sdk/php")).
			With(daggerCall("container-echo", "--string-arg", "hello", "stdout")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})

	t.Run("from alias", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(daggerExec(
				"init",
				"--name=bare",
				"--sdk=php")).
			With(daggerCall("container-echo", "--string-arg", "hello", "stdout")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})

	t.Run("from alias with ref", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(daggerExec(
				"init",
				"--name=bare",
				"--sdk=php@main")).
			With(daggerCall("container-echo", "--string-arg", "hello", "stdout")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})
}

func (PHPSuite) TestDefaultValue(_ context.Context, t *testctx.T) {
	t.Run("can set a value", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := phpModule(t, c, "defaults").
			With(daggerCall("echo", "--value=hello")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "hello", out)
	})

	t.Run("can use a default value", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := phpModule(t, c, "defaults").
			With(daggerCall("echo")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "default value", out)
	})
}

func (PHPSuite) TestScalarKind(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	module := phpModule(t, c, "scalar-kind")

	t.Run("bool", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCall("opposite-bool", "--arg=true")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "false", out)
	})

	t.Run("float", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCall("half-float", "--arg=3.14")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "1.57", out)
	})

	t.Run("integer", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCall("double-int", "--arg=418")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "836", out)
	})

	t.Run("string", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCall("capitalize-string", "--arg=hello, world!")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello, World!", out)
	})
}

func (PHPSuite) TestListKind(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	module := phpModule(t, c, "list-kind")

	t.Run("list of bools", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCall("opposite-bools", "--arg=true,false,true")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "false\ntrue\nfalse\n", out)
	})

	t.Run("list of floats", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCall("half-floats", "--arg=3.7,8.87,9.81")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "1.85\n4.435\n4.905\n", out)
	})

	t.Run("list of integers", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCall("double-ints", "--arg=1,3,7")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "2\n6\n14\n", out)
	})

	t.Run("list of strings", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCall(
				"capitalize-strings",
				"--arg=hello,world!,howdy,planet!")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello\nWorld!\nHowdy\nPlanet!\n", out)
	})
}

func (PHPSuite) TestVoidKind(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	module := phpModule(t, c, "void-kind")

	t.Run("void", func(ctx context.Context, t *testctx.T) {
		out, err := module.With(daggerCall("get-void")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", out)
	})

	t.Run("null", func(ctx context.Context, t *testctx.T) {
		out, err := module.With(daggerCall("give-and-get-null")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", out)
	})
}

func phpModule(t *testctx.T, c *dagger.Client, moduleName string) *dagger.Container {
	t.Helper()
	modSrc, err := filepath.Abs(filepath.Join("./testdata/modules/php", moduleName))
	require.NoError(t, err)

	sdkSrc, err := filepath.Abs("../../sdk/php")
	require.NoError(t, err)

	return goGitBase(t, c).
		WithDirectory("modules/"+moduleName, c.Host().Directory(modSrc)).
		WithDirectory("sdk/php", c.Host().Directory(sdkSrc)).
		WithWorkdir("/work/modules/" + moduleName)
}
