package core

// These tests cover modules authored with the Elixir SDK. They verify generated
// Elixir bindings and executing Elixir module functions.
//
// See also:
// - module_definition_test.go: SDK-neutral module API definition behavior.
// - module_type_test.go: cross-SDK custom type behavior.

import (
	"context"
	"path/filepath"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type ElixirSuite struct{}

func TestElixir(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ElixirSuite{})
}

func (ElixirSuite) TestOptionalValue(ctx context.Context, t *testctx.T) {
	t.Run("can run without a value", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCallAt(".", "echo-else")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "default value if null", out)
	})

	t.Run("can set a value", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCallAt(".", "echo-else", "--value=foo")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "foo", out)
	})

	t.Run("can use default", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCallAt(".", "echo-value")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "foo", out)
	})

	t.Run("can use value with default", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCallAt(".", "echo-value", "--value=bar")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "bar", out)
	})

	t.Run("default value in Elixir should be set", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCallAt(".", "call-echo-value")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "foo", out)
	})
}

func (ElixirSuite) TestDefaultPath(ctx context.Context, t *testctx.T) {
	t.Run("can set a path for a file", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCallAt(".", "file-name", "--file=./mix.exs")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "mix.exs", out)
	})

	t.Run("can use a default path for a file", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCallAt(".", "file-name")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "dagger.json", out)
	})

	t.Run("can use a default path for a dir", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCallAt(".", "file-names")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "defaults.ex", out)
	})
}

func (ElixirSuite) TestIgnore(ctx context.Context, t *testctx.T) {
	t.Run("without ignore", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCallAt(".", "files-no-ignore")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Contains(t, out, "dagger.json")
		require.Contains(t, out, "mix.exs")
	})

	t.Run("with ignore", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCallAt(".", "files-ignore")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Contains(t, out, "dagger.json")
		require.NotContains(t, out, "mix.exs")
	})

	t.Run("with negated ignore", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCallAt(".", "files-neg-ignore")).
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
		With(daggerCallAt(".", "foo", "message")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "bar", out)
}

func (ElixirSuite) TestReturnChildObject(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	mod := elixirModule(t, c, "objects")

	out, err := mod.
		With(daggerCallAt(".", "object-a", "message")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "Hello from A", out)

	out, err = mod.
		With(daggerCallAt(".", "object-a", "object-b", "message")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "Hello from B", out)
}

func (ElixirSuite) TestConstructorArg(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := elixirModule(t, c, "constructor-function").
		With(daggerCallAt(".", "--name", "Elixir", "greeting")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "Hello, Elixir!", out)
}

func (ElixirSuite) TestEnumArg(ctx context.Context, t *testctx.T) {
	t.Run("can use enum", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCallAt(".", "echo-enum", "--value=FOO")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "FOO", out)
	})

	t.Run("default value in Elixir should be set", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCallAt(".", "enum-value")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "FOO", out)
	})

	t.Run("can use enum with default", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "defaults").
			With(daggerCallAt(".", "enum-value", "--value=GAR")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "GAR", out)
	})

	t.Run("wrong enum value", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := elixirModule(t, c, "defaults").
			With(daggerCallAt(".", "enum-value", "--value=BAZ")).
			Stdout(ctx)
		requireErrOut(t, err, "invalid argument \"BAZ\" for \"--value\" flag: value should be one of BAR,FOO,GAR")
		requireErrOut(t, err, "Run 'dagger call enum-value --help' for usage.")
	})
}

// Ensure the module is working properly with the `Req` adapter.
func (ElixirSuite) TestReqAdapter(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := elixirModule(t, c, "req-adapter").
		With(daggerCallAt(".", "container-echo", "--string-arg", "hello-from-req-adapter", "stdout")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "hello-from-req-adapter\n", out)
}

func (ElixirSuite) TestCheck(ctx context.Context, t *testctx.T) {
	t.Run("list checks", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "hello-with-checks").
			With(daggerExec("check", "-l")).
			CombinedOutput(ctx)

		require.NoError(t, err)
		require.Contains(t, out, "passing-check")
		require.Contains(t, out, "failing-check")
		require.Contains(t, out, "passing-container")
		require.Contains(t, out, "failing-container")
	})

	t.Run("run passing checks", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "hello-with-checks").
			With(daggerExec("--progress=report", "check", "passing*")).
			CombinedOutput(ctx)

		require.NoError(t, err)
		require.Regexp(t, `passing-check.*OK`, out)
		require.Regexp(t, `passing-container.*OK`, out)
	})

	t.Run("run failing checks", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := elixirModule(t, c, "hello-with-checks").
			With(daggerExecFail("--progress=report", "check", "failing*")).
			CombinedOutput(ctx)

		require.NoError(t, err)
		require.Regexp(t, `failing-check.*ERROR`, out)
		require.Regexp(t, `failing-container.*ERROR`, out)
	})
}

func elixirModule(t *testctx.T, c *dagger.Client, moduleName string) *dagger.Container {
	t.Helper()
	modSrc, err := filepath.Abs(filepath.Join("./testdata/modules/elixir", moduleName))
	require.NoError(t, err)

	sdkSrc, err := filepath.Abs("../../sdk/elixir")
	require.NoError(t, err)

	return goGitBase(t, c).
		WithDirectory("modules/"+moduleName, c.Host().Directory(modSrc)).
		WithDirectory("sdk/elixir", c.Host().Directory(sdkSrc)).
		WithWorkdir("/work/modules/" + moduleName)
}
