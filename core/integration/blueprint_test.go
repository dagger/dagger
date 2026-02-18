package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/testctx"
)

type BlueprintSuite struct{}

func TestBlueprint(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(BlueprintSuite{})
}

func (BlueprintSuite) TestBlueprintUseLocal(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	// Test basic blueprint installation via dagger install --blueprint
	t.Run("use local blueprint", func(ctx context.Context, t *testctx.T) {
		modGen := c.Container().
			From(alpineImage).
			WithExec([]string{"apk", "add", "git"}).
			WithExec([]string{"git", "init"}).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithDirectory(".", c.Host().Directory("./testdata/test-blueprint")).
			WithWorkdir("app").
			With(daggerExec("install", "--blueprint", "../hello"))
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
			name:          "install typescript blueprint",
			blueprintPath: "../myblueprint-ts",
		},
		{
			name:          "install python blueprint",
			blueprintPath: "../myblueprint-py",
		},
	} {
		c := connect(ctx, t)
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			modGen := c.Container().
				From(alpineImage).
				WithExec([]string{"apk", "add", "git"}).
				WithExec([]string{"git", "init"}).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithDirectory(".", c.Host().Directory("./testdata/test-blueprint")).
				WithWorkdir("app").
				With(daggerExec("install", "--blueprint", tc.blueprintPath))
			// Verify blueprint was installed by calling function
			out, err := modGen.
				With(daggerExec("call", "hello")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "hello from blueprint")
		})
	}
}
