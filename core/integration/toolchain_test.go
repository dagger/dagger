package core

import (
	"context"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"

	"github.com/dagger/testctx"
)

type ToolchainSuite struct{}

func TestToolchain(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ToolchainSuite{})
}

func toolchainTestEnv(t *testctx.T, c *dagger.Client) *dagger.Container {
	return c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithExec([]string{"git", "init"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithDirectory(".", c.Host().Directory("./testdata/test-blueprint")).
		WithDirectory("app", c.Directory())
}

func (ToolchainSuite) TestToolchainConstructor(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	// Test single toolchain installation
	t.Run("use toolchain constructor", func(ctx context.Context, t *testctx.T) {
		modGen := toolchainTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init")).
			With(daggerExec("toolchain", "install", "../hello-with-constructor"))
		modGen = modGen.WithNewFile("app-config.txt", "this is the app configuration").
			WithNewFile("other-config.txt", "this is the other app configuration")
		appConfig, err := modGen.
			With(daggerExec("call", "hello", "field-config")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, appConfig, "this is the app configuration")
		appConfig, err = modGen.
			With(daggerExec("call", "hello", "--config", "other-config.txt", "field-config")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, appConfig, "this is the other app configuration")
		// Test multiple toolchain installations
		modGen = modGen.With(daggerExec("toolchain", "install", "../myblueprint-ts"))
		appConfig, err = modGen.
			With(daggerExec("call", "hello", "field-config")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, appConfig, "this is the app configuration")
		appConfig, err = modGen.
			With(daggerExec("call", "hello", "--config", "other-config.txt", "field-config")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, appConfig, "this is the other app configuration")
	})
}

func (ToolchainSuite) TestMultipleToolchains(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Run("install multiple toolchains", func(ctx context.Context, t *testctx.T) {
		modGen := toolchainTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init")).
			With(daggerExec("toolchain", "install", "../hello"))
		// Verify toolchain was installed by calling function
		out, err := modGen.
			With(daggerExec("call", "hello", "message")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from blueprint")
		// install another toolchain
		modGen = modGen.
			With(daggerExec("toolchain", "install", "../myblueprint-ts")).
			With(daggerExec("toolchain", "install", "../myblueprint-py"))
		out, err = modGen.Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "toolchain installed")
		out, err = modGen.
			With(daggerExec("call", "hello", "message")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from blueprint")
		out, err = modGen.
			With(daggerExec("call", "myblueprint-py", "hello")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from blueprint")
		out, err = modGen.
			With(daggerExec("call", "myblueprint-ts", "hello")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from blueprint")
		modGen = modGen.WithNewFile("app-config.txt", "this is the app configuration")
		appConfig, err := modGen.
			With(daggerExec("call", "hello", "app-config")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, appConfig, "this is the app configuration")
	})
}

func (ToolchainSuite) TestToolchainsWithSDK(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Run("use blueprint with sdk", func(ctx context.Context, t *testctx.T) {
		modGen := toolchainTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init", "--sdk=go")).
			With(daggerExec("toolchain", "install", "../hello"))
		// verify we can call function from our module code
		out, err := modGen.
			With(daggerExec("call", "container-echo", "--string-arg", "yoyo", "stdout")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "yoyo")
		// verify we can call a function from our blueprint
		out, err = modGen.
			With(daggerExec("call", "hello", "message")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from blueprint")
	})
}

func (ToolchainSuite) TestToolchainsWithMultipleObjects(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Run("use toolchain with multiple objects", func(ctx context.Context, t *testctx.T) {
		modGen := toolchainTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init")).
			With(daggerExec("toolchain", "install", "../hello-with-objects"))
		// verify we can call a function from our blueprint
		out, err := modGen.
			With(daggerExec("call", "hello-with-objects", "say-greeting", "hello")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Hello!")
	})
}

func (ToolchainSuite) TestToolchainsWithBlueprint(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Run("use toolchains with blueprint", func(ctx context.Context, t *testctx.T) {
		modGen := toolchainTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init", "--blueprint=../hello")).
			With(daggerExec("toolchain", "install", "../myblueprint-py"))
		// verify we can call function from our module code
		out, err := modGen.
			With(daggerExec("call", "message")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from blueprint")
		// verify we can call a function from our blueprint
		out, err = modGen.
			With(daggerExec("call", "myblueprint-py", "hello")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from blueprint")
	})
}
