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
