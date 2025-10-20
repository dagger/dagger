package core

import (
	"context"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"

	"github.com/dagger/testctx"
)

type BlueprintSuite struct{}

func TestBlueprint(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(BlueprintSuite{})
}

func blueprintTestEnv(t *testctx.T, c *dagger.Client) *dagger.Container {
	return c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithExec([]string{"git", "init"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithDirectory(".", c.Host().Directory("./testdata/test-blueprint")).
		WithDirectory("app", c.Directory())
}

func (BlueprintSuite) TestBlueprintUseLocal(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	// Test basic blueprint installation
	t.Run("use local blueprint", func(ctx context.Context, t *testctx.T) {
		modGen := blueprintTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init", "--blueprint=../hello"))
		// Verify blueprint was installed by calling function
		out, err := modGen.
			With(daggerExec("call", "message")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from blueprint")
		blueprintConfig, err := modGen.
			With(daggerExec("call", "blueprint-config")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, blueprintConfig, "this is the blueprint configuration")
		modGen = modGen.WithNewFile("app-config.txt", "this is the app configuration")
		appConfig, err := modGen.
			With(daggerExec("call", "app-config")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, appConfig, "this is the app configuration")
	})
}

func (BlueprintSuite) TestToolchainConstructor(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	// Test single toolchain installation
	t.Run("use toolchain constructor", func(ctx context.Context, t *testctx.T) {
		modGen := blueprintTestEnv(t, c).
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

func (BlueprintSuite) TestBlueprintInit(ctx context.Context, t *testctx.T) {
	type testCase struct {
		name          string
		blueprintPath string
	}

	for _, tc := range []testCase{
		{
			name:          "use a blueprint which has a dependency",
			blueprintPath: "../myblueprint-with-dep",
		},
		{
			name:          "init with typescript blueprint",
			blueprintPath: "../myblueprint-ts",
		},
		{
			name:          "init with python blueprint",
			blueprintPath: "../myblueprint-py",
		},
	} {
		c := connect(ctx, t)
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			modGen := blueprintTestEnv(t, c).
				WithWorkdir("app").
				With(daggerExec("init", "--blueprint="+tc.blueprintPath))
			// Verify blueprint was installed by calling function
			out, err := modGen.
				With(daggerExec("call", "hello")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "hello from blueprint")
		})
	}
}

func (BlueprintSuite) TestMultipleToolchains(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Run("install multiple toolchains", func(ctx context.Context, t *testctx.T) {
		modGen := blueprintTestEnv(t, c).
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

func (BlueprintSuite) TestToolchainsWithSDK(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Run("use blueprint with sdk", func(ctx context.Context, t *testctx.T) {
		modGen := blueprintTestEnv(t, c).
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

func (BlueprintSuite) TestToolchainsWithBlueprint(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Run("use blueprint with sdk", func(ctx context.Context, t *testctx.T) {
		modGen := blueprintTestEnv(t, c).
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
