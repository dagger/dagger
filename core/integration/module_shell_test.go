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
    return dag.Container(). From("`+golangImage+`")
}
`,
	).
		With(daggerShell(fmt.Sprintf(".core container | from %s | with-exec cat,/etc/os-release | stdout", alpineImage))).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "Alpine Linux")
}

func (ShellSuite) TestBasicGit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	script := ".git https://github.com/dagger/dagger.git | file README.md | contents"
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
