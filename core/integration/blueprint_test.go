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
			With(daggerExec("init", "--blueprint=../myblueprint"))
		// Verify blueprint was installed by calling function
		out, err := modGen.
			With(daggerExec("call", "hello")).
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

func (BlueprintSuite) TestBlueprintWithDependency(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Run("use a blueprint which has a dependency", func(ctx context.Context, t *testctx.T) {
		modGen := blueprintTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init", "--blueprint=../myblueprint-with-dep"))
		// Verify blueprint was installed by calling function
		out, err := modGen.
			With(daggerExec("call", "hello")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from blueprint")
	})
}

func (BlueprintSuite) TestBlueprintTypescript(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Run("use a blueprint which has a dependency", func(ctx context.Context, t *testctx.T) {
		modGen := blueprintTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init", "--blueprint=../myblueprint-ts"))
		// Verify blueprint was installed by calling function
		out, err := modGen.
			With(daggerExec("call", "hello")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from blueprint")
	})
}

func (BlueprintSuite) TestBlueprintNoSDK(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Run("init with --sdk and --blueprint", func(ctx context.Context, t *testctx.T) {
		modGen := blueprintTestEnv(t, c).
			WithWorkdir("app").
			WithExec(
				[]string{"dagger", "init", "--sdk=go", "--blueprint=../myblueprint"},
				dagger.ContainerWithExecOpts{
					ExperimentalPrivilegedNesting: true,
					Expect:                        dagger.ReturnTypeFailure,
				},
			)
		stderr, err := modGen.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, "--sdk")
		require.Contains(t, stderr, "--blueprint")
	})
}
