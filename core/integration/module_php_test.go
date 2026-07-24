package core

// These tests cover modules authored with the PHP SDK. They verify generated
// PHP bindings and executing PHP module functions.
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

type PHPSuite struct{}

func TestPHP(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(PHPSuite{})
}

func (PHPSuite) TestDefaultValue(_ context.Context, t *testctx.T) {
	t.Run("can set a value", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := phpModule(t, c, "defaults").
			With(daggerCallAt(".", "echo", "--value=hello")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "hello", out)
	})

	t.Run("can use a default value", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := phpModule(t, c, "defaults").
			With(daggerCallAt(".", "echo")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "default value", out)
	})
}

func (PHPSuite) TestScalarKind(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	module := phpModule(t, c, "scalar-kind")

	t.Run("bool func", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCallAt(".", "opposite-bool", "--arg=true")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "false", out)
	})

	t.Run("bool field", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCallAt(".", "bool-field")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "true", out)
	})

	t.Run("set fields then get bool field", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCallAt(".", "set-fields", "bool-field")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "false", out)
	})

	t.Run("float func", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCallAt(".", "half-float", "--arg=3.14")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "1.57", out)
	})

	t.Run("float field", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCallAt(".", "float-field")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "3.14", out)
	})

	t.Run("set fields, then get float field", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCallAt(".", "set-fields", "float-field")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "1.618", out)
	})

	t.Run("int func", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCallAt(".", "double-int", "--arg=418")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "836", out)
	})

	t.Run("int field", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCallAt(".", "int-field")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "1", out)
	})

	t.Run("set fields then get int field", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCallAt(".", "set-fields", "int-field")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "2", out)
	})

	t.Run("string func", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCallAt(".", "capitalize-string", "--arg=hello, func!")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello, Func!", out)
	})

	t.Run("string field", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCallAt(".", "string-field")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello, field!", out)
	})

	t.Run("set fields then get string field", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCallAt(".", "set-fields", "string-field")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "HOWDY, FIELD!", out)
	})
}

func (PHPSuite) TestListKind(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	module := phpModule(t, c, "list-kind")

	t.Run("list of bools", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCallAt(".", "opposite-bools", "--arg=true,false,true")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "false\ntrue\nfalse\n", out)
	})

	t.Run("list of floats", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCallAt(".", "half-floats", "--arg=3.7,8.87,9.81")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "1.85\n4.435\n4.905\n", out)
	})

	t.Run("list of integers", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCallAt(".", "double-ints", "--arg=1,3,7")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "2\n6\n14\n", out)
	})

	t.Run("list of strings", func(ctx context.Context, t *testctx.T) {
		out, err := module.
			With(daggerCallAt(".",
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
		out, err := module.With(daggerCallAt(".", "get-void")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", out)
	})

	t.Run("null", func(ctx context.Context, t *testctx.T) {
		out, err := module.With(daggerCallAt(".", "give-and-get-null")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", out)
	})
}

func (PHPSuite) TestObjectKind(ctx context.Context, t *testctx.T) {
	t.Run("File", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		module := phpModule(t, c, "object-kind/built-in-to-dagger")

		out, err := module.
			WithNewFile("/foo", "hello, world!").
			With(daggerCallAt(".", "capitalize-contents", "--arg=/foo", "contents")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "Hello, World!", out)
	})

	t.Run("Directory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		module := phpModule(t, c, "object-kind/built-in-to-dagger")

		out, err := module.
			WithNewFile("/foo/bar", "Hello, World!").
			With(daggerCallAt(".", "with-baz", "--arg=/foo", "entries")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "bar\nbaz\n", out)
	})
}

func (PHPSuite) TestConstructor(_ context.Context, t *testctx.T) {
	t.Run("value set", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		module := phpModule(t, c, "constructor/value-set")

		out, err := module.
			With(daggerCallAt(".", "--arg=foo", "get-constructor-arg")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo", out)
	})

	t.Run("value manipulated", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		module := phpModule(t, c, "constructor/value-manipulated")

		out, err := module.
			With(daggerCallAt(".", "--arg=true", "get-constructor-arg")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "false", out)
	})
}

func (PHPSuite) TestCheck(ctx context.Context, t *testctx.T) {
	t.Run("list checks", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := phpModule(t, c, "checks").
			With(daggerExec("check", "-l")).
			CombinedOutput(ctx)

		require.NoError(t, err)
		require.Contains(t, out, "passing")
		require.Contains(t, out, "failing")
		require.Contains(t, out, "passing-container")
		require.Contains(t, out, "failing-container")
	})

	t.Run("run passing checks", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := phpModule(t, c, "checks").
			With(daggerExec("--progress=report", "check", "passing*")).
			CombinedOutput(ctx)

		require.NoError(t, err)
		require.Regexp(t, `passing.*OK`, out)
		require.Regexp(t, `passing-container.*OK`, out)
	})

	t.Run("run failing checks", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := phpModule(t, c, "checks").
			With(daggerExecFail("--progress=report", "check", "failing*")).
			CombinedOutput(ctx)

		require.NoError(t, err)
		require.Regexp(t, `failing.*ERROR`, out)
		require.Regexp(t, `failing-container.*ERROR`, out)
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
