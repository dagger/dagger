package core

import (
	"context"
	"path/filepath"
	"testing"

	dagger "github.com/dagger/dagger/internal/testutil/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type ElixirSuite struct{}

func TestElixir(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ElixirSuite{})
}

func (ElixirSuite) TestInit(ctx context.Context, t *testctx.T) {
	t.Run("from local", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		sdkSrc, err := filepath.Abs("sdk/elixir")
		require.NoError(t, err)

		out, err := goGitBase(t, c).
			WithDirectory("/work/sdk/elixir", c.Host().Directory(sdkSrc)).
			With(daggerExec(
				"init",
				"--name=bare",
				"--sdk=./sdk/elixir")).
			With(daggerCall("container-echo", "--string-arg", "hello", "stdout")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})

	t.Run("from upstream", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=github.com/dagger/dagger/sdk/elixir"))

		out, err := modGen.
			With(daggerCall("container-echo", "--string-arg=hello", "stdout")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})

	t.Run("from alias", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=elixir"))

		out, err := modGen.
			With(daggerCall("container-echo", "--string-arg=hello", "stdout")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})

	t.Run("from alias with ref", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=elixir@main"))

		out, err := modGen.
			With(daggerCall("container-echo", "--string-arg=hello", "stdout")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})
}

func (ElixirSuite) TestOptionalValue(ctx context.Context, t *testctx.T) {
	t.Run("can run without a value", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCall("echo-else")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "default value if null", out)
	})

	t.Run("can set a value", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCall("echo-else", "--value=foo")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "foo", out)
	})

	t.Run("can use default", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCall("echo-value")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "foo", out)
	})

	t.Run("can use value with default", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCall("echo-value", "--value=bar")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "bar", out)
	})

	t.Run("default value in Elixir should be set", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCall("call-echo-value")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "foo", out)
	})
}

func (ElixirSuite) TestDefaultPath(ctx context.Context, t *testctx.T) {
	t.Run("can set a path for a file", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCall("file-name", "--file=./mix.exs")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "mix.exs", out)
	})

	t.Run("can use a default path for a file", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCall("file-name")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "dagger.json", out)
	})

	t.Run("can use a default path for a dir", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCall("file-names")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "defaults.ex", out)
	})
}

func (ElixirSuite) TestIgnore(ctx context.Context, t *testctx.T) {
	t.Run("without ignore", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCall("files-no-ignore")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Contains(t, out, "dagger.json")
		require.Contains(t, out, "mix.exs")
	})

	t.Run("with ignore", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCall("files-ignore")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Contains(t, out, "dagger.json")
		require.NotContains(t, out, "mix.exs")
	})

	t.Run("with negated ignore", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCall("files-neg-ignore")).
			Stdout(ctx)

		require.NoError(t, err)
		require.NotContains(t, out, "dagger.json")
		require.NotContains(t, out, "mix.exs")
		require.Contains(t, out, "lib")
	})
}

func (ElixirSuite) TestReturnSelf(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := elixirModule(t, c, "self-object").
		With(daggerCall("foo", "message")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "bar", out)
}

func (ElixirSuite) TestReturnChildObject(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	mod := elixirModule(t, c, "objects")

	out, err := mod.
		With(daggerCall("object-a", "message")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "Hello from A", out)

	out, err = mod.
		With(daggerCall("object-a", "object-b", "message")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "Hello from B", out)
}

func (ElixirSuite) TestConstructorArg(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := elixirModule(t, c, "constructor-function").
		With(daggerCall("--name", "Elixir", "greeting")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "Hello, Elixir!", out)
}

// Ensure the module is working properly with the `Req` adapter.
func (ElixirSuite) TestReqAdapter(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := elixirModule(t, c, "req-adapter").
		With(daggerCall("container-echo", "--string-arg", "hello-from-req-adapter", "stdout")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "hello-from-req-adapter\n", out)
}

func elixirModule(t *testctx.T, c *dagger.Client, moduleName string) *dagger.Container {
	t.Helper()
	modSrc, err := filepath.Abs(filepath.Join("core/integration/testdata/modules/elixir", moduleName))
	require.NoError(t, err)

	sdkSrc, err := filepath.Abs("sdk/elixir")
	require.NoError(t, err)

	return goGitBase(t, c).
		WithDirectory("modules/"+moduleName, c.Host().Directory(modSrc)).
		WithDirectory("sdk/elixir", c.Host().Directory(sdkSrc)).
		WithWorkdir("/work/modules/" + moduleName)
}
