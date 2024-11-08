package core

import (
	"context"
	"fmt"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
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

func (ShellSuite) TestFallbackToCore(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	script := fmt.Sprintf("container | from %s | with-exec apk,add,git | with-workdir /src | with-exec git,clone,https://github.com/dagger/dagger.git,.,--depth=1 | file README.md | contents", alpineImage)
	out, err := modInit(t, c, "go", "").
		With(daggerShell(script)).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "What is Dagger?")
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

	t.Run("first command fallback to core", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerShell("container")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Container", gjson.Get(out, "_type").String())
	})

	t.Run("no module commands", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerShell(".help")).Stdout(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, ".install")
	})

	t.Run("no main object doc", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.With(daggerShell(".doc")).Sync(ctx)
		require.ErrorContains(t, err, "module not loaded")
	})
}

func (ShellSuite) TestNoLoadModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen := modInit(t, c, "go", "")

	t.Run("sanity check", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerShell(".doc")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "container-echo")
	})

	t.Run("forced no load", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.
			WithExec(
				[]string{"dagger", "shell", "--no-load", "-c", ".doc"},
				dagger.ContainerWithExecOpts{
					ExperimentalPrivilegedNesting: true,
				},
			).
			Sync(ctx)
		require.ErrorContains(t, err, "module not loaded")
	})
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
