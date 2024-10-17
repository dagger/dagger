package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type UnInstallSuite struct{}

func TestUnInstall(t *testing.T) {
	testctx.Run(testCtx, t, UnInstallSuite{}, Middleware()...)
}

func (UnInstallSuite) TestUninstallLocalDep(ctx context.Context, t *testctx.T) {
	t.Run("uninstall a dependency currently used in module", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().
			From("alpine:latest").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/bar").
			With(daggerExec("init", "--sdk=go", "--name=bar", "--source=.")).
			WithWorkdir("/work").
			With(daggerExec("init", "--sdk=go", "--name=foo", "--source=.")).
			With(daggerExec("install", "./bar")).
			WithNewFile("main.go", `package main

import (
	"context"
)

type Foo struct{}

func (f *Foo) ContainerEcho(ctx context.Context, input string) (string, error) {
	return dag.Bar().ContainerEcho(input).Stdout(ctx)
}
`)

		daggerjson, err := ctr.File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, daggerjson, "bar")

		daggerjson, err = ctr.With(daggerExec("uninstall", "bar")).
			File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.NotContains(t, daggerjson, "bar")
	})

	t.Run("uninstall a dependency configured in dagger.json", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().
			From("alpine:latest").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/bar").
			With(daggerExec("init", "--sdk=go", "--name=bar", "--source=.")).
			WithWorkdir("/work").
			With(daggerExec("init", "--sdk=go", "--name=foo", "--source=.")).
			With(daggerExec("install", "./bar"))

		daggerjson, err := ctr.File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, daggerjson, "bar")

		daggerjson, err = ctr.With(daggerExec("uninstall", "bar")).
			File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.NotContains(t, daggerjson, "bar")
	})

	t.Run("uninstall a dependency configured in dagger.json using relative path syntax", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().
			From("alpine:latest").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/bar").
			With(daggerExec("init", "--sdk=go", "--name=bar", "--source=.")).
			WithWorkdir("/work").
			With(daggerExec("init", "--sdk=go", "--name=foo", "--source=.")).
			With(daggerExec("install", "./bar"))

		daggerjson, err := ctr.File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, daggerjson, "bar")

		daggerjson, err = ctr.With(daggerExec("uninstall", "./bar")).
			File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.NotContains(t, daggerjson, "bar")
	})

	t.Run("uninstall a dependency not configured in dagger.json", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := c.Container().
			From("alpine:latest").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/bar").
			With(daggerExec("init", "--sdk=go", "--name=bar", "--source=.")).
			WithWorkdir("/work").
			With(daggerExec("init", "--sdk=go", "--name=foo", "--source=.")).
			With(daggerExec("uninstall", "bar")).
			File("dagger.json").Contents(ctx)
		require.NoError(t, err)
	})

	// this one currently fails - do we need to handle this?
	t.Run("dependency source is removed before calling uninstall", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().
			From("alpine:latest").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/bar").
			With(daggerExec("init", "--sdk=go", "--name=bar", "--source=.")).
			WithWorkdir("/work").
			With(daggerExec("init", "--sdk=go", "--name=foo", "--source=.")).
			With(daggerExec("install", "./bar"))

		daggerjson, err := ctr.File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, daggerjson, "bar")

		daggerjson, err = ctr.
			WithoutDirectory("/work/bar").
			With(daggerExec("uninstall", "bar")).
			File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.NotContains(t, daggerjson, "bar")
	})
}

func (UnInstallSuite) TestUninstallGitRefDep(ctx context.Context, t *testctx.T) {
	t.Run("uninstall a dependency configured in dagger.json with version number", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().
			From("alpine:latest").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--sdk=go", "--name=foo", "--source=.")).
			With(daggerExec("install", "github.com/shykes/daggerverse/hello@v0.3.0"))

		daggerjson, err := ctr.File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, daggerjson, "hello")

		daggerjson, err = ctr.With(daggerExec("uninstall", "github.com/shykes/daggerverse/hello@v0.3.0")).
			File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.NotContains(t, daggerjson, "hello")
	})

	t.Run("uninstall a dependency configured in dagger.json without version number", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().
			From("alpine:latest").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--sdk=go", "--name=foo", "--source=.")).
			With(daggerExec("install", "github.com/shykes/daggerverse/hello"))

		daggerjson, err := ctr.File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, daggerjson, "hello")

		daggerjson, err = ctr.With(daggerExec("uninstall", "github.com/shykes/daggerverse/hello@v0.3.0")).
			File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.NotContains(t, daggerjson, "hello")
	})

	t.Run("uninstall a dependency not configured in dagger.json", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().
			From("alpine:latest").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--sdk=go", "--name=foo", "--source=."))

		_, err := ctr.With(daggerExec("uninstall", "github.com/shykes/daggerverse/hello@v0.3.0")).
			File("dagger.json").Contents(ctx)
		require.NoError(t, err)
	})

	// this one is currently failing - I think this should be fixed
	t.Run("uninstall a dependency by name", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().
			From("alpine:latest").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--sdk=go", "--name=foo", "--source=.")).
			With(daggerExec("install", "github.com/shykes/daggerverse/hello"))

		daggerjson, err := ctr.File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, daggerjson, "hello")

		_, err = ctr.With(daggerExec("uninstall", "hello")).
			File("dagger.json").Contents(ctx)
		require.NoError(t, err)

		daggerjson, err = ctr.File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.NotContains(t, daggerjson, "hello")
	})
}
