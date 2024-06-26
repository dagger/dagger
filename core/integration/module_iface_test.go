package core

import (
	"context"
	"strings"

	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

func (ModuleSuite) TestIfaceBasic(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithMountedDirectory("/work", c.Host().Directory("./testdata/modules/go/ifaces")).
		WithWorkdir("/work").
		With(daggerCall("test")).
		Sync(ctx)
	require.NoError(t, err)
}

func (ModuleSuite) TestIfaceGoSadPaths(ctx context.Context, t *testctx.T) {
	t.Run("no dagger object embed", func(ctx context.Context, t *testctx.T) {
		var logs safeBuffer
		c := connect(ctx, t, dagger.WithLogOutput(&logs))

		_, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main
type Test struct {}

type BadIface interface {
	Foo(ctx context.Context) (string, error)
}

func (m *Test) Fn() BadIface {
	return nil
}
	`,
			}).
			With(daggerFunctions()).
			Sync(ctx)
		require.Error(t, err)
		require.NoError(t, c.Close())
		require.Regexp(t, `missing method .* from DaggerObject interface, which must be embedded in interfaces used in Functions and Objects`, logs.String())
	})
}

func (ModuleSuite) TestIfaceGoDanglingInterface(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main
type Test struct {}

func (test *Test) Hello() string {
	return "hello"
}

type DanglingObject struct {}

func (obj *DanglingObject) Hello(x DanglingIface) DanglingIface {
	return x
}

type DanglingIface interface {
	DoThing() (error)
}
	`,
		}).
		Sync(ctx)
	require.NoError(t, err)

	out, err := modGen.
		With(daggerQuery(`{test{hello}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"test":{"hello":"hello"}}`, out)
}

func (ModuleSuite) TestIfaceDaggerCall(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/mallard").
		With(daggerExec("init", "--source=.", "--name=mallard", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main
type Mallard struct {}

func (m *Mallard) Quack() string {
	return "mallard quack"
}
	`,
		}).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import (
	"context"
)

type Test struct {}

type Duck interface {
	DaggerObject
	Quack(ctx context.Context) (string, error)
}

func (m *Test) GetDuck() Duck {
	return dag.Mallard()
}
	`,
		}).
		With(daggerExec("install", "./mallard")).
		With(daggerCall("get-duck", "quack")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "mallard quack", strings.TrimSpace(out))
}
