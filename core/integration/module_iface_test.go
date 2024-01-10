package core

import (
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestModuleIfaceBasic(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	_, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithMountedDirectory("/work", c.Host().Directory("./testdata/modules/go/ifaces")).
		WithWorkdir("/work").
		With(daggerCall("test")).
		Sync(ctx)
	require.NoError(t, err)
}

func TestModuleIfaceGoSadPaths(t *testing.T) {
	t.Parallel()

	t.Run("no dagger object embed", func(t *testing.T) {
		t.Parallel()
		var logs safeBuffer
		c, ctx := connect(t, dagger.WithLogOutput(&logs))

		_, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
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
		require.Regexp(t, `missing method .* from DaggerObject interface, which must be embedded in interfaces used in Functions and Objects`, logs.String())
	})
}

func TestModuleIfaceDaggerCall(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	out, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/mallard").
		With(daggerExec("mod", "init", "--name=mallard", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main
type Mallard struct {}

func (m *Mallard) Quack() string {
	return "mallard quack"
}
	`,
		}).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
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
		With(daggerExec("mod", "install", "./mallard")).
		With(daggerCall("get-duck", "quack")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "mallard quack", strings.TrimSpace(out))
}
