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
		return c.WithExec([]string{"dagger", "shell", "-c", script}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func daggerShellNoMod(script string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec([]string{"dagger", "shell", "--no-mod", "-c", script}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
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
		With(daggerShell(fmt.Sprintf(".stdlib | container | from %s | with-exec cat,/etc/os-release | stdout", alpineImage))).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "Alpine Linux")
}

func (ShellSuite) TestNoModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen := daggerCliBase(t, c)

	t.Run("module builtin does not work", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.With(daggerShell(".deps")).Sync(ctx)
		requireErrOut(t, err, "module not loaded")
	})

	t.Run("no default module doc", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.With(daggerShell(".doc .")).Sync(ctx)
		requireErrOut(t, err, "not found")
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
			With(daggerShellNoMod(".deps")).
			Sync(ctx)
		requireErrOut(t, err, "module not loaded")
	})

	t.Run("dynamically loaded", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := modInit(t, c, "go", "").
			With(daggerShellNoMod(".use .; .doc")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "container-echo")
	})

	t.Run("stateless load", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := modInit(t, c, "go", "").
			With(daggerShellNoMod(". | .doc container-echo")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "echoes whatever string argument")
	})

	t.Run("stateless .doc load", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := modInit(t, c, "go", "").
			With(daggerShellNoMod(".doc .")).
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
			With(daggerShell("foo")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"_type": "Foo", "bar": "foobar"}`, out)
	})

	t.Run("stateful", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := modInit(t, c, "go", test).
			With(daggerExec("init", "--sdk=go", "--source=foo", "foo")).
			With(sdkSourceAt("foo", "go", foo)).
			With(daggerShell(".use foo; bar")).
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
			With(daggerShell("foo | bar")).
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
		With(daggerShell("load-container-from-id")).
		Sync(ctx)
	requireErrOut(t, err, "not found")
}

func (ShellSuite) TestIntegerArg(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	script := "container | with-exposed-port 80 | exposed-ports | port"
	out, err := daggerCliBase(t, c).
		With(daggerShell(script)).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "80\n", out)
}

func (ShellSuite) TestExport(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	script := "directory | with-new-file foo bar | export mydir"
	out, err := daggerCliBase(t, c).
		With(daggerShell(script)).
		File("mydir/foo").
		Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "bar", out)
}

func (ShellSuite) TestBasicGit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	script := "git https://github.com/dagger/dagger | head | tree | file README.md | contents"
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
	script := ".core | load-directory-from-id $(directory-id) | file foo | contents"

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
	require.Contains(t, out, "Usage: ./. <foo> <bar>\n  \n  The entrypoint for the module")
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
