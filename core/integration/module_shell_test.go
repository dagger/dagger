package core

import (
	"context"
	"fmt"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type ShellSuite struct{}

func TestShell(t *testing.T) {
	testctx.Run(testCtx, t, ShellSuite{}, Middleware()...)
}

func daggerShell(script string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec([]string{"dagger", "shell", "-c", script})
	}
}

func daggerShellNoLoad(script string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec([]string{"dagger", "shell", "--no-load", "-c", script})
	}
}

func (ShellSuite) TestDefaultToModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := modInit(t, c, "go", `package main

import (
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) Container() *dagger.Container {
	return dag.Container(). From("`+alpineImage+`")
}
`,
	).
		With(daggerShell("container | with-exec cat,/etc/os-release | stdout")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "Alpine Linux")
}

func (ShellSuite) TestForceCore(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := modInit(t, c, "go", `package main

import (
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) Container() *dagger.Container {
	return dag.Container().From("`+golangImage+`")
}
`,
	).
		With(daggerShell(fmt.Sprintf(".container | from %s | with-exec cat,/etc/os-release | stdout", alpineImage))).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "Alpine Linux")
}

func (ShellSuite) TestNoModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen := daggerCliBase(t, c)

	t.Run("first command no fallback to core", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.With(daggerShell("container")).Sync(ctx)
		requireErrOut(t, err, "module not loaded")
	})

	t.Run("module builtin does not work", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.With(daggerShell(".config")).Sync(ctx)
		requireErrOut(t, err, "module not loaded")
	})

	t.Run("no main object doc", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.With(daggerShell(".config")).Sync(ctx)
		requireErrOut(t, err, "module not loaded")
	})
}

func (ShellSuite) TestNoLoadModule(ctx context.Context, t *testctx.T) {
	t.Run("sanity check", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := modInit(t, c, "go", "").
			With(daggerShell(".doc")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "container-echo")
	})

	t.Run("forced no load", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		_, err := modInit(t, c, "go", "").
			With(daggerShellNoLoad(".config")).
			Sync(ctx)
		requireErrOut(t, err, "module not loaded")
	})

	t.Run("dynamically loaded", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := modInit(t, c, "go", "").
			With(daggerShellNoLoad(".load | .use; .doc")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "container-echo")
	})

	t.Run("stateless load", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := modInit(t, c, "go", "").
			With(daggerShellNoLoad(".load | .doc")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "container-echo")
	})
}

func (ShellSuite) TestLoadAnotherModule(ctx context.Context, t *testctx.T) {
	test := `package main

type Test struct{}

func (m *Test) Bar() string {
	return "testbar"
}
`

	foo := `package main

func New() *Foo {
	return &Foo{
		Bar: "foobar",
	}
}

type Foo struct{
	Bar string
}
`
	t.Run("main object", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := modInit(t, c, "go", test).
			With(daggerExec("init", "--sdk=go", "--source=foo", "foo")).
			With(sdkSourceAt("foo", "go", foo)).
			With(daggerShell(".load foo")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"_type": "Foo", "bar": "foobar"}`, out)
	})

	t.Run("stateful", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := modInit(t, c, "go", test).
			With(daggerExec("init", "--sdk=go", "--source=foo", "foo")).
			With(sdkSourceAt("foo", "go", foo)).
			With(daggerShell(".load foo | .use; bar")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "foobar")
	})

	t.Run("stateless", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		modGen := modInit(t, c, "go", test).
			With(daggerExec("init", "--sdk=go", "--source=foo", "foo")).
			With(sdkSourceAt("foo", "go", foo))

		out, err := modGen.
			With(daggerShell(".load foo | bar")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foobar", out)

		out, err = modGen.
			With(daggerShell("bar")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "testbar", out)
	})
}

func (ShellSuite) TestNotExists(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	_, err := modInit(t, c, "go", "").
		With(daggerShell("container")).
		Sync(ctx)
	requireErrOut(t, err, "no such function")
}

func (ShellSuite) TestIntegerArg(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	script := ".container | with-exposed-port 80 | exposed-ports | port"
	out, err := daggerCliBase(t, c).
		With(daggerShell(script)).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "80\n", out)
}

func (ShellSuite) TestExport(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	script := ".directory | with-new-file foo bar | export mydir"
	out, err := daggerCliBase(t, c).
		With(daggerShell(script)).
		File("mydir/foo").
		Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "bar", out)
}

func (ShellSuite) TestBasicGit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	script := ".git https://github.com/dagger/dagger | head | tree | file README.md | contents"
	out, err := daggerCliBase(t, c).
		With(withModInit("go", "")).
		With(daggerShell(script)).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "What is Dagger?")
}

func (ShellSuite) TestBasicModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	script := "container-echo hello-world-im-here | stdout"
	out, err := daggerCliBase(t, c).
		With(withModInit("go", "")).
		With(daggerShell(script)).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "hello-world-im-here")
}

func (ShellSuite) TestPassingID(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	source := `package main

import "context"

type Test struct{}

func (m *Test) DirectoryID(ctx context.Context) (string, error) {
	id, err := dag.Directory().WithNewFile("foo", "bar").ID(ctx)
	return string(id), err
}
`
	script := ".load-directory-from-id $(directory-id) | file foo | contents"

	out, err := modInit(t, c, "go", source).
		With(daggerShell(script)).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "bar", out)
}

func (ShellSuite) TestModuleDoc(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	source := `// This is a test module

package main

// The entrypoint for the module
func New(foo string, bar string) *Test {
	return &Test{
		Foo: foo,
		Bar: bar,
	}
}

type Test struct{
	Foo string
	Bar string
}

// Some function
func (m *Test) FooBar() string {
	return m.Foo+m.Bar
}
`
	out, err := modInit(t, c, "go", source).
		With(daggerShell(".doc")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "\n  test\n  \n  This is a test module")
	require.Contains(t, out, "Usage: .config <foo> <bar>\n  \n  The entrypoint for the module")
	require.Regexp(t, "foo-bar +Some function", out)
}

func (ShellSuite) TestInstall(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := modInit(t, c, "go", "").
		With(daggerExec("init", "--sdk=go", "dep")).
		With(daggerShell(".install dep")).
		WithExec([]string{"grep", "dep", "dagger.json"}).
		Sync(ctx)

	require.NoError(t, err)
}
